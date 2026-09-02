package internal

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const (
	accountsDir       = "data/accounts"
	usersFile         = "data/accounts/users.json"
	sessionsFile      = "data/accounts/sessions.json"
	legacyProfileFile = "data/profile.json"
	sessionCookieName = "briefly_session"
	sessionTTL        = 30 * 24 * time.Hour
	maxAvatarLen      = 3 << 20
)

// SessionCookieName — имя httpOnly-куки сессии.
const SessionCookieName = sessionCookieName

var (
	usernameRe      = regexp.MustCompile(`^[a-zA-Z0-9_]{3,24}$`)
	ErrUserExists   = errors.New("пользователь уже существует")
	ErrBadUsername  = errors.New("логин: 3-24 символа, только буквы, цифры и _")
	ErrBadPassword  = errors.New("пароль: минимум 6 символов")
	ErrBadLogin     = errors.New("неверный логин или пароль")
	ErrAvatarTooBig = errors.New("аватар слишком большой (макс. 3 МБ)")
	ErrBadAvatar    = errors.New("аватар: только data:image/* или http(s) URL")
)

// briefToken — токен из BRIEFLY_TOKEN (режим одного пользователя на удалённом
// хосте). Передаётся через SetBriefToken из main.
var briefToken string

// SetBriefToken задаёт legacy-токен (BRIEFLY_TOKEN).
func SetBriefToken(t string) { briefToken = t }

// TokenMatches проверяет, что запрос пришёл с валидным legacy-токеном.
// Пустой токен (локальный режим) всегда возвращает false — там действуют сессии.
func TokenMatches(c *gin.Context) bool {
	if briefToken == "" {
		return false
	}
	if t := c.GetHeader("X-Briefly-Token"); t != "" {
		return subtle.ConstantTimeCompare([]byte(t), []byte(briefToken)) == 1
	}
	if b := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "); len(b) < len(c.GetHeader("Authorization")) {
		return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(b)), []byte(briefToken)) == 1
	}
	if t := c.Query("token"); t != "" {
		return subtle.ConstantTimeCompare([]byte(t), []byte(briefToken)) == 1
	}
	return false
}

type Account struct {
	Username  string `json:"username"`
	PassHash  string `json:"pass_hash"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	CreatedAt string `json:"created_at"`
}

type sessionEntry struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Accounts struct {
	mu       sync.RWMutex
	dir      string
	users    map[string]*Account
	sessions map[string]sessionEntry
	profiles map[string]*Profile
}

var (
	accountsOnce sync.Once
	accountsInst *Accounts
)

func GetAccounts() *Accounts {
	accountsOnce.Do(func() {
		accountsInst = newAccounts(accountsDir)
	})
	return accountsInst
}

func newAccounts(dir string) *Accounts {
	a := &Accounts{
		dir:      dir,
		users:    make(map[string]*Account),
		sessions: make(map[string]sessionEntry),
		profiles: make(map[string]*Profile),
	}
	a.load()
	return a
}

func (a *Accounts) load() {
	_ = os.MkdirAll(a.dir, 0755)
	if data, err := os.ReadFile(usersFile); err == nil {
		var users []*Account
		if json.Unmarshal(data, &users) == nil {
			for _, u := range users {
				a.users[u.Username] = u
			}
		}
	}
	if data, err := os.ReadFile(sessionsFile); err == nil {
		var sessions map[string]sessionEntry
		if json.Unmarshal(data, &sessions) == nil {
			now := time.Now()
			for t, s := range sessions {
				if now.Before(s.ExpiresAt) {
					a.sessions[t] = s
				}
			}
		}
	}
}

func (a *Accounts) saveUsers() error {
	a.mu.RLock()
	users := make([]*Account, 0, len(a.users))
	for _, u := range a.users {
		users = append(users, u)
	}
	a.mu.RUnlock()
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(usersFile, data, 0600)
}

func (a *Accounts) saveSessions() error {
	a.mu.RLock()
	sessions := make(map[string]sessionEntry, len(a.sessions))
	for t, s := range a.sessions {
		sessions[t] = s
	}
	a.mu.RUnlock()
	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(sessionsFile, data, 0600)
}

func (a *Accounts) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.users)
}

func (a *Accounts) Get(username string) (*Account, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	u, ok := a.users[username]
	return u, ok
}

func (a *Accounts) Users() []*Account {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Account, 0, len(a.users))
	for _, u := range a.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// IsAdmin: администратор — первый зарегистрированный пользователь
// (минимальный CreatedAt). Производное свойство: миграция существующих
// данных не нужна, а потеря/порча поля роли ничего не ломает.
func (a *Accounts) IsAdmin(username string) bool {
	users := a.Users()
	if len(users) == 0 {
		return false
	}
	return users[0].Username == username
}

func validateUsername(name string) error {
	name = strings.TrimSpace(name)
	if !usernameRe.MatchString(name) {
		return ErrBadUsername
	}
	return nil
}

func validatePassword(pass string) error {
	if len(pass) < 6 {
		return ErrBadPassword
	}
	return nil
}

// Register создаёт аккаунт. Первый зарегистрированный пользователь наследует
// данные старого единого профиля (data/profile.json).
func (a *Accounts) Register(username, password string) (*Account, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if _, ok := a.users[username]; ok {
		a.mu.Unlock()
		return nil, ErrUserExists
	}
	first := len(a.users) == 0
	user := &Account{
		Username:  username,
		Nickname:  username,
		PassHash:  string(hash),
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	a.users[username] = user
	a.mu.Unlock()

	if err := a.saveUsers(); err != nil {
		// Откатываем in-memory вставку: иначе юзер «существует» до
		// рестарта, но отсутствует в users.json (рассинхрон).
		a.mu.Lock()
		delete(a.users, username)
		a.mu.Unlock()
		return nil, err
	}
	if first {
		a.migrateLegacyProfile(username)
	}
	return user, nil
}

func (a *Accounts) Authenticate(username, password string) (*Account, error) {
	username = strings.TrimSpace(strings.ToLower(username))
	a.mu.RLock()
	user, ok := a.users[username]
	a.mu.RUnlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(user.PassHash), []byte(password)) != nil {
		return nil, ErrBadLogin
	}
	return user, nil
}

// ChangePassword меняет пароль, предварительно проверив текущий.
func (a *Accounts) ChangePassword(username, current, newPass string) error {
	if err := validatePassword(newPass); err != nil {
		return err
	}
	username = strings.TrimSpace(strings.ToLower(username))
	a.mu.RLock()
	user, ok := a.users[username]
	a.mu.RUnlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(user.PassHash), []byte(current)) != nil {
		return ErrBadLogin
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a.mu.Lock()
	user.PassHash = string(hash)
	a.mu.Unlock()
	return a.saveUsers()
}

// RevokeOtherSessions удаляет все сессии пользователя, кроме keepToken
// (пустая строка — отозвать все). Возвращает число отозванных сессий.
func (a *Accounts) RevokeOtherSessions(username, keepToken string) int {
	username = strings.TrimSpace(strings.ToLower(username))
	a.mu.Lock()
	removed := 0
	for t, s := range a.sessions {
		if s.Username == username && t != keepToken {
			delete(a.sessions, t)
			removed++
		}
	}
	a.mu.Unlock()
	if removed > 0 {
		_ = a.saveSessions()
	}
	return removed
}

func (a *Accounts) CreateSession(username string) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(buf)
	expires := time.Now().Add(sessionTTL)
	a.mu.Lock()
	// Заодно выметаем просроченные: иначе мапа и sessions.json растут
	// бесконечно у клиентов, которые не делают logout.
	a.pruneExpiredSessionsLocked(expires.Add(-sessionTTL))
	a.sessions[token] = sessionEntry{Username: username, ExpiresAt: expires}
	a.mu.Unlock()
	_ = a.saveSessions()
	return token, expires, nil
}

// pruneExpiredSessionsLocked удаляет истёкшие сессии (вызывать под a.mu).
func (a *Accounts) pruneExpiredSessionsLocked(now time.Time) {
	for t, s := range a.sessions {
		if now.After(s.ExpiresAt) {
			delete(a.sessions, t)
		}
	}
}

func (a *Accounts) UserBySession(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	a.mu.RLock()
	s, ok := a.sessions[token]
	a.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().After(s.ExpiresAt) {
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
		_ = a.saveSessions()
		return "", false
	}
	return s.Username, true
}

func (a *Accounts) DeleteSession(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
	_ = a.saveSessions()
}

// validAvatar пропускает только data:image/... и абсолютные http(s)-URL:
// аватар вставляется в <img src> у всех клиентов, прочие схемы
// (javascript:, data:text/html и т.п.) — вектор XSS.
func validAvatar(avatar string) bool {
	if strings.HasPrefix(avatar, "data:image/") {
		return true
	}
	u, err := url.Parse(avatar)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func (a *Accounts) SetMeta(username, nickname, avatar string) error {
	if len(avatar) > maxAvatarLen {
		return ErrAvatarTooBig
	}
	if avatar != "" && !validAvatar(avatar) {
		return ErrBadAvatar
	}
	a.mu.Lock()
	user, ok := a.users[username]
	if ok {
		user.Nickname = strings.TrimSpace(nickname)
		user.Avatar = avatar
	}
	a.mu.Unlock()
	if !ok {
		return ErrBadLogin
	}
	return a.saveUsers()
}

// Profile возвращает профиль (лайки/пресеты/теги) конкретного пользователя.
// Файл: data/accounts/<username>/profile.json
func (a *Accounts) Profile(username string) *Profile {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.profiles[username]; ok {
		return p
	}
	p := NewProfile(filepath.Join(a.dir, username, "profile.json"))
	a.profiles[username] = p
	return p
}

// migrateLegacyProfile переносит данные старого единого профиля в аккаунт
// первого пользователя и удаляет исходник, чтобы данные не задваивались.
func (a *Accounts) migrateLegacyProfile(username string) {
	data, err := os.ReadFile(legacyProfileFile)
	if err != nil {
		return
	}
	p := a.Profile(username)
	p.mu.Lock()
	var legacy Profile
	if json.Unmarshal(data, &legacy) == nil {
		p.LikedPosts = legacy.LikedPosts
		p.HiddenPosts = legacy.HiddenPosts
		p.Presets = legacy.Presets
		p.FavTags = legacy.FavTags
		p.HiddenTags = legacy.HiddenTags
	}
	p.mu.Unlock()
	_ = p.Save()
	os.Rename(legacyProfileFile, legacyProfileFile+".bak")
}

// ProfileFor возвращает профиль по запросу: для залогиненного пользователя —
// его личный, для token-запросов — legacy-профиль.
func ProfileFor(c *gin.Context) *Profile {
	if u := c.GetString("briefly_user"); u != "" {
		return GetAccounts().Profile(u)
	}
	return GetProfile()
}
