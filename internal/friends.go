package internal

// Friends — обмен отметками между инстансами better34 без общего сервера.
//
// Модель: каждый инстанс сам себе и база, и хранилище. Синхронизация идёт
// напрямую между компьютерами: наш код сам ходит к другу по указанному адресу
// и спрашивает, что у него нового, а затем отдаёт своё. Обмен ТОЛЬКО
// дополняет (merge) — лайки, коллекции и комментарии соединяются, но никогда
// не удаляются и не перезаписываются.
//
// Доступ: адрес + общий ключ (ключ хранится у обоих). Ключ передаётся один
// раз при добавлении — вручную, кодом вида briefly-friend-v1:<url>:<key>.
// Поэтому добавление не требует ни DNS, ни белого IP, ни посредника: подойдёт
// любой способ добраться до соседа (Radmin VPN, Tailscale, локальная сеть).
//
// Ключ — bearer-токен, поэтому сравнивается в постоянном времени и
// ограничивает и чтение, и запись: чужие лайки видны, но подделать свои нельзя.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	friendsDir        = "data/accounts/friends"
	friendsLegacyFile = "data/friends.json"
	// friendCodePrefix — версия формата кода друга. Меняем при несовместимых
	// изменениях, чтобы старый код не распарсился молча в мусор.
	friendCodePrefix = "briefly-friend-v1"
	// Капы на входящие данные: друг — недоверенный источник, и без ограничений
	// один запрос может вогнать в память мегабайты.
	maxFriends       = 64
	maxFriendLikes   = 5000
	maxFriendCmts    = 500
	maxFriendColls   = 200
	maxFriendCollsPC = 2000
	maxFriendText    = 500
	maxFriendName    = 64
	friendSyncPeriod = 5 * time.Minute
)

var (
	ErrFriendCodeInvalid = errors.New("код друга не распознан")
	ErrFriendSelf        = errors.New("нельзя добавить самого себя")
	ErrFriendExists      = errors.New("этот друг уже добавлен")
	ErrFriendNotFound    = errors.New("друг не найден")
	ErrFriendAuth        = errors.New("неверный ключ друга")
	ErrFriendBody        = errors.New("некорректные данные от друга")
)

// Friend — запись в списке друзей: куда ходить и каким ключом.
type Friend struct {
	// URL — адрес инстанса друга, включая схему ("https://26.4.5.6:3000").
	URL string `json:"url"`
	// Key — ключ ДРУГА, выданный им в его friend-коде. Им мы авторизуемся у
	// друга в обе стороны: и чтобы забрать его данные, и чтобы отдать свои.
	// Отдельного «моего ключа для друга» не нужно: симметрия и так работает
	// (у друга в его записи лежит наш ключ, он приходит с его запросом).
	Key       string `json:"key"`
	Nickname  string `json:"nickname,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Username  string `json:"username,omitempty"`
	AddedAt   string `json:"added_at"`
	LastSync  string `json:"last_sync,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// ID — устойчивый идентификатор записи (в UI, в SSE, в логах). Считаем от
// нормализованного адреса: повторное добавление того же адреса не плодит дубли.
func (f *Friend) ID() string {
	sum := sha256.Sum256([]byte(f.URL))
	return hex.EncodeToString(sum[:])[:12]
}

// FriendsFile — содержимое файла друзей пользователя. Друзья, как и лайки,
// per-user аспект, поэтому живут рядом с профилем.
type FriendsFile struct {
	Version int      `json:"version"`
	MyKey   string   `json:"my_key"`
	MyName  string   `json:"my_name,omitempty"`
	Friends []Friend `json:"friends"`
}

// FriendStore — потокобезопасное хранилище списка друзей.
type FriendStore struct {
	mu   sync.RWMutex
	path string
	byID map[string]*Friend
}

func friendsPathFor(username string) string {
	if username == "" {
		return friendsLegacyFile
	}
	return filepath.Join(friendsDir, username+".json")
}

var (
	friendStoresMu sync.Mutex
	friendStores   = map[string]*FriendStore{}
)

// GetFriendStore возвращает хранилище друзей пользователя. Создаётся один раз
// на пользователя и живёт до перезапуска: фоновый синк ходит каждые пять минут
// и не должен каждый раз перечитывать файл.
func GetFriendStore(username string) *FriendStore {
	friendStoresMu.Lock()
	defer friendStoresMu.Unlock()
	if s, ok := friendStores[username]; ok {
		return s
	}
	s := &FriendStore{path: friendsPathFor(username), byID: map[string]*Friend{}}
	s.load()
	friendStores[username] = s
	return s
}

func (s *FriendStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f FriendsFile
	if json.Unmarshal(data, &f) != nil {
		return
	}
	for i := range f.Friends {
		fr := f.Friends[i]
		if fr.URL == "" || fr.Key == "" {
			continue
		}
		if fr.AddedAt == "" {
			fr.AddedAt = time.Now().UTC().Format(time.RFC3339)
		}
		cp := fr
		s.byID[fr.ID()] = &cp
	}
}

func newFriendKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// myKeyLocked читает наш ключ из файла. Ключ должен быть стабильным между
// перезапусками, иначе друзья перестанут стучаться. Вызывается под mu.
func (s *FriendStore) myKeyLocked() string {
	if data, err := os.ReadFile(s.path); err == nil {
		var f FriendsFile
		if json.Unmarshal(data, &f) == nil && f.MyKey != "" {
			return f.MyKey
		}
	}
	return ""
}

// MyKey возвращает наш ключ, создавая его при первом обращении.
func (s *FriendStore) MyKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k := s.myKeyLocked(); k != "" {
		return k
	}
	key := newFriendKey()
	if err := s.saveLocked(key, s.myNameLocked()); err != nil {
		return key
	}
	return key
}

func (s *FriendStore) myNameLocked() string {
	if data, err := os.ReadFile(s.path); err == nil {
		var f FriendsFile
		if json.Unmarshal(data, &f) == nil {
			return f.MyName
		}
	}
	return ""
}

// saveLocked пишет файл. Порядок записей стабильный: иначе каждое сохранение
// переставляло бы их и засоряло diff/бэкапы.
func (s *FriendStore) saveLocked(myKey, myName string) error {
	f := FriendsFile{Version: 1, MyKey: myKey, MyName: myName, Friends: []Friend{}}
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		f.Friends = append(f.Friends, *s.byID[id])
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	// 0600: в файле лежат bearer-ключи, читать их должен только процесс.
	return atomicWriteFile(s.path, data, 0o600)
}

func (s *FriendStore) save() error {
	return s.saveLocked(s.myKeyLocked(), s.myNameLocked())
}

// normalizeFriendURL приводит адрес к виду scheme://host[:port] без слеша и
// query. Без этого "https://a:3000/" и "https://a:3000" стали бы разными
// друзьями. Токен в URL не принимаем — он утёк бы в access-логи.
func normalizeFriendURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrFriendCodeInvalid
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", ErrFriendCodeInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrFriendCodeInvalid
	}
	if u.Host == "" {
		return "", ErrFriendCodeInvalid
	}
	if u.User != nil {
		return "", ErrFriendCodeInvalid
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// EncodeFriendCode собирает код для передачи другу — «визитку».
// Формат: briefly-friend-v1:<url>:<key>[:<имя>].
func (s *FriendStore) EncodeFriendCode(username, nickname, selfURL string) (string, error) {
	base, err := normalizeFriendURL(selfURL)
	if err != nil {
		return "", err
	}
	name := nickname
	if name == "" {
		name = username
	}
	if utf8.RuneCountInString(name) > maxFriendName {
		name = string([]rune(name)[:maxFriendName])
	}
	// Имя может содержать двоеточие — заменяем, иначе разбор соберёт его в хвост.
	name = strings.ReplaceAll(name, ":", " ")
	return fmt.Sprintf("%s:%s:%s:%s", friendCodePrefix, base, s.MyKey(), name), nil
}

// ParseFriendCode разбирает код друга.
//
// Код устроен как префикс и далее части, разделённые двоеточием:
//
//	briefly-friend-v1 : https : //host : port : <key64> : <имя>
//
// Ключ узнаётся однозначно: это 64 hex-символа без двоеточий. Всё, что между
// префиксом и ключом, склеивается обратно в адрес (схема «https://» сама
// содержит двоеточие, поэтому резать по нему нельзя).
func ParseFriendCode(code string) (addr, key, name string, err error) {
	// Код часто копируют через мессенджер или выделяют в браузере, и по пути
	// в нём появляются переносы строк и пробелы. Переносы — всегда мусор,
	// а пробелы допустимы только в имени друга: их мы сохраняем, из адреса и
	// ключа вычищаем. Иначе «почти правильный» код молча отклонялся бы.
	code = strings.NewReplacer("\r", "", "\n", "", "\t", "").Replace(code)
	parts := strings.Split(strings.TrimSpace(code), ":")
	if len(parts) < 4 {
		return "", "", "", ErrFriendCodeInvalid
	}
	// Пробелы — мусор от копирования. Вычищаем их из префикса, адреса и ключа,
	// но НЕ из имени: оно идёт после ключа, и пробелы в нём законны
	// («Вася Пупкин» не должен превращаться в «ВасяПупкин»).
	clean := func(s string) string { return strings.Join(strings.Fields(s), "") }
	parts[0] = clean(parts[0])
	if parts[0] != friendCodePrefix {
		return "", "", "", ErrFriendCodeInvalid
	}
	keyIdx := -1
	for i := 1; i < len(parts); i++ {
		parts[i] = clean(parts[i])
		// Ключ узнаётся однозначно: это 64 hex-символа без двоеточий.
		if i > 1 && len(parts[i]) == 64 && isHex(parts[i]) {
			keyIdx = i
			break
		}
	}
	if keyIdx < 0 {
		return "", "", "", ErrFriendCodeInvalid
	}
	raw := strings.Join(parts[1:keyIdx], ":")
	norm, err := normalizeFriendURL(raw)
	if err != nil {
		return "", "", "", ErrFriendCodeInvalid
	}
	key = parts[keyIdx]
	if keyIdx+1 < len(parts) {
		name = parts[keyIdx+1]
	}
	if utf8.RuneCountInString(name) > maxFriendName {
		name = string([]rune(name)[:maxFriendName])
	}
	return norm, key, name, nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// AddFriend добавляет друга по коду. selfURL — наш адрес: кладём его записи,
// иначе друг не будет знать, куда прислать своё в ответ.
func (s *FriendStore) AddFriend(code, selfURL, username, nickname string) (*Friend, error) {
	addr, key, name, err := ParseFriendCode(code)
	if err != nil {
		return nil, err
	}
	normSelf, err := normalizeFriendURL(selfURL)
	if err != nil {
		return nil, err
	}
	if addr == normSelf {
		return nil, ErrFriendSelf
	}
	// MyKey() берёт mu.Lock, поэтому вызываем ДО блокировки — иначе вторичный
	// захват того же мьютекса приведёт к дедлоку.
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byID) >= maxFriends {
		return nil, fmt.Errorf("лимит друзей: %d", maxFriends)
	}
	fr := Friend{
		URL:      addr,
		Key:      key,
		Nickname: name,
		Username: username,
		AddedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, exists := s.byID[fr.ID()]; exists {
		return nil, ErrFriendExists
	}
	cp := fr
	s.byID[fr.ID()] = &cp
	if err := s.saveLocked(s.myKeyLocked(), defaultFriendName(username, nickname)); err != nil {
		delete(s.byID, fr.ID())
		return nil, err
	}
	return &cp, nil
}

func defaultFriendName(username, nickname string) string {
	if nickname != "" {
		return nickname
	}
	return username
}

// List возвращает копию списка: наружу отдаём данные, а не внутренние
// указатели, иначе обработчик изменил бы запись в обход блокировки.
func (s *FriendStore) List() []Friend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Friend, 0, len(s.byID))
	for _, f := range s.byID {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

func (s *FriendStore) Get(id string) (Friend, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.byID[id]
	if !ok {
		return Friend{}, false
	}
	return *f, true
}

// Remove удаляет друга.
func (s *FriendStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return ErrFriendNotFound
	}
	delete(s.byID, id)
	return s.save()
}

// SetMyName обновляет имя, которое видят друзья.
func (s *FriendStore) SetMyName(name string) error {
	if utf8.RuneCountInString(name) > maxFriendName {
		name = string([]rune(name)[:maxFriendName])
	}
	name = strings.ReplaceAll(strings.TrimSpace(name), ":", " ")
	s.mu.Lock()
	defer s.mu.Unlock()
	myKey := s.myKeyLocked()
	if myKey == "" {
		myKey = newFriendKey()
	}
	return s.saveLocked(myKey, name)
}

// Authorize проверяет ключ входящего запроса от друга. Сравнение в постоянном
// времени: иначе по задержке можно подбирать ключ побайтово.
func (s *FriendStore) Authorize(got string) bool {
	return s.MatchesKey(got)
}

// MatchesKey — как Authorize, но БЕЗ создания ключа. Нужна при поиске по
// аккаунтам: перебор всех пользователей не должен создавать файлы друзей
// каждому, кто ни разу не открывал вкладку.
func (s *FriendStore) MatchesKey(got string) bool {
	got = strings.TrimSpace(got)
	if got == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.myKeyLocked())) == 1
}

// AuthorizeFriendKey ищет владельца ключа среди всех аккаунтов.
//
// Запрос от чужого инстанса приходит БЕЗ сессии (cookie у него нет), и по
// одному заголовку нельзя понять, чей это ключ. Поэтому перебираем
// аккаунты: их единицы, сравнение ключа — constant-time, а сам ключ имеет
// 32 байта энтропии, так что перебор по сети бессмыслен.
func AuthorizeFriendKey(got string) bool {
	got = strings.TrimSpace(got)
	if got == "" {
		return false
	}
	accs := GetAccounts()
	if accs.Count() == 0 {
		// Режим без аккаунтов: единственное хранилище — legacy.
		return GetFriendStore("").MatchesKey(got)
	}
	for _, u := range accs.Users() {
		if GetFriendStore(u.Username).MatchesKey(got) {
			return true
		}
	}
	return false
}

// noteSync записывает результат последнего обмена (для UI).
func (s *FriendStore) noteSync(id string, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.byID[id]
	if !ok {
		return
	}
	f.LastSync = time.Now().UTC().Format(time.RFC3339)
	f.LastError = errMsg
	// Ошибку обрезаем: иначе текст от чужого сервера попадёт в файл и в UI.
	if len(f.LastError) > 200 {
		f.LastError = f.LastError[:200]
	}
	_ = s.save()
}

// ── Что именно обмениваем ────────────────────────────────────────────────────

// friendLike — отметка «нравится» с временем (порядок лайков важен:
// сортировка вкладки «Лайки» идёт по liked_at).
type friendLike struct {
	PostID  int   `json:"post_id"`
	LikedAt int64 `json:"liked_at,omitempty"`
}

// friendCollection — коллекция друга. Совпадает с нашей Collection, но id
// переезжает: коллекции объединяются по имени, иначе у каждого свой id и
// общий альбом не собрался бы.
type friendCollection struct {
	Name      string `json:"name"`
	Posts     []int  `json:"posts,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// friendComment — комментарий друга. Username здесь чужой логин, поэтому в
// нашей БД он хранится с префиксом instance (см. friendCommentUsername),
// иначе «vasya» с чужого сайта слился бы с нашим «vasya».
type friendComment struct {
	PostID    int    `json:"post_id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// friendPayload — то, чем два инстанса обмениваются. Асимметрично урезанное:
// чужие скрытия и рекомендации нам не нужны (это личное), а вот лайки,
// коллекции и комментарии — ровно то, за что мы зовём «друзей».
type friendPayload struct {
	Version     int                `json:"version"`
	App         string             `json:"app"`
	Instance    string             `json:"instance"`
	User        string             `json:"user"`
	Nickname    string             `json:"nickname,omitempty"`
	Avatar      string             `json:"avatar,omitempty"`
	SyncedAt    string             `json:"synced_at"`
	Likes       []friendLike       `json:"likes"`
	Collections []friendCollection `json:"collections,omitempty"`
	Comments    []friendComment    `json:"comments,omitempty"`
}

const friendPayloadVersion = 1

// friendCommentUsername отделяет чужие комментарии от наших: без префикса
// логины с разных инстансов склеились бы, и удалить чужой комментарий было
// бы невозможно (id чужой БД не совпадает с нашим).
func friendCommentUsername(instance, username string) string {
	return instance + ":" + username
}

// isFriendComment отделяет наш собственный комментарий от пришедшего от друга.
func isFriendComment(username string) bool { return strings.Contains(username, ":") }

// buildFriendPayload собирает то, чем мы делимся с другом: только свои лайки,
// коллекции и комментарии. Скрытия и рекомендации НЕ делим — это личное.
func buildFriendPayload(instance, user, nickname, avatar string, p *Profile) *friendPayload {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Комментарии: только свои, с обрезкой по лимиту — у человека с историей
	// в тысячи комментариев иначе мы бы забирали по мегабайту каждые 5 минут.
	comments := make([]friendComment, 0, 16)
	for _, cm := range GetDB().CommentsByUser(user) {
		if len(comments) >= maxFriendCmts {
			break
		}
		text := truncateRunes(cm.Text, maxFriendText)
		if text == "" {
			continue
		}
		comments = append(comments, friendComment{
			PostID: cm.PostID, Username: user, Nickname: nickname,
			Avatar: avatar, Text: text, CreatedAt: cm.CreatedAt,
		})
	}

	// Лайки сортируем по времени: разрез по лимиту тогда оставляет самые
	// свежие, а не случайные.
	likes := make([]friendLike, 0, len(p.LikedPosts))
	for id, liked := range p.LikedPosts {
		if liked && id > 0 {
			likes = append(likes, friendLike{PostID: id, LikedAt: p.LikedAt[id]})
		}
	}
	sort.Slice(likes, func(i, j int) bool { return likes[i].LikedAt > likes[j].LikedAt })
	if len(likes) > maxFriendLikes {
		likes = likes[:maxFriendLikes]
	}

	colls := make([]friendCollection, 0, len(p.Collections))
	for _, c := range p.Collections {
		if len(colls) >= maxFriendColls {
			break
		}
		posts := c.Posts
		if len(posts) > maxFriendCollsPC {
			posts = posts[:maxFriendCollsPC]
		}
		colls = append(colls, friendCollection{Name: c.Name, Posts: posts, CreatedAt: c.CreatedAt})
	}

	return &friendPayload{
		Version: friendPayloadVersion, App: "briefly", Instance: instance,
		User: user, Nickname: nickname, Avatar: avatar,
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Likes:    likes, Collections: colls, Comments: comments,
	}
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// mergeStats — что реально добавилось при обмене (для ответа и логов).
type mergeStats struct {
	Likes       int `json:"likes"`
	Collections int `json:"collections"`
	Comments    int `json:"comments"`
}

func (m mergeStats) total() int { return m.Likes + m.Collections + m.Comments }

// applyFriendPayload вливает данные друга в наш профиль. Строго ADDITIVE:
// ничего не удаляем и не перезаписываем — иначе один сбой у друга стёр бы
// нашу библиотеку.
//
// instance — адрес ОТПРАВИТЕЛЯ, взятый из нашего списка друзей (он пришёл с
// friend-кодом, поэтому подменить его без ключа нельзя). Он же служит
// пространством имён для чужих комментариев: логины с разных инстансов не
// сливаются. Поле instance в теле запроса здесь намеренно не сверяется: там
// адрес, каким sender видит СЕБЯ, а в запросе Host — адрес получателя.
func applyFriendPayload(in *friendPayload, instance string, p *Profile) (mergeStats, error) {
	var st mergeStats
	if in == nil || in.Version != friendPayloadVersion {
		return st, ErrFriendBody
	}
	if instance == "" {
		return st, ErrFriendBody
	}
	// Общий вес ограничиваем: иначе друг пришлёт 100k записей разом.
	if len(in.Likes) > maxFriendLikes*4 || len(in.Collections) > maxFriendColls*4 ||
		len(in.Comments) > maxFriendCmts*4 {
		return st, ErrFriendBody
	}

	p.mu.Lock()
	for _, l := range in.Likes {
		if l.PostID <= 0 {
			continue
		}
		if !p.LikedPosts[l.PostID] {
			p.LikedPosts[l.PostID] = true
			st.Likes++
		}
		if p.LikedAt == nil {
			p.LikedAt = make(map[int]int64)
		}
		// Более раннюю метку не перетираем: иначе каждый обмен «омолаживал»
		// бы пост и лента «Лайки» прыгала бы при синхронизации.
		if l.LikedAt > p.LikedAt[l.PostID] {
			p.LikedAt[l.PostID] = l.LikedAt
		}
	}
	// Коллекции объединяем по имени без учёта регистра: общий альбом должен
	// быть один, а не по копии на каждого друга.
	for _, fc := range in.Collections {
		name := truncateRunes(fc.Name, 80)
		if name == "" {
			continue
		}
		idx := -1
		for i, c := range p.Collections {
			if strings.EqualFold(c.Name, name) {
				idx = i
				break
			}
		}
		if idx < 0 {
			p.Collections = append(p.Collections, Collection{
				ID: newPresetID(), Name: name,
				Posts: dedupeInts(fc.Posts, maxFriendCollsPC), CreatedAt: fc.CreatedAt,
			})
			st.Collections++
			continue
		}
		if mergePosts(&p.Collections[idx].Posts, fc.Posts, maxFriendCollsPC) {
			st.Collections++
		}
	}
	p.mu.Unlock()

	// Комментарии. Дедуп по (instance, username, post_id, text): повторный
	// обмен не плодит копии, а локальные id у нас свои.
	db := GetDB()
	for _, fc := range in.Comments {
		if fc.PostID <= 0 || fc.Username == "" {
			continue
		}
		text := truncateRunes(fc.Text, maxFriendText)
		if text == "" {
			continue
		}
		created := fc.CreatedAt
		if created == "" {
			created = time.Now().UTC().Format(time.RFC3339)
		}
		if db.AddFriendCommentOnce(friendCommentUsername(instance, fc.Username), fc.PostID, text, created) {
			st.Comments++
		}
	}

	if err := p.Save(); err != nil {
		return st, err
	}
	return st, nil
}

func dedupeInts(in []int, max int) []int {
	seen := make(map[int]bool, len(in))
	out := make([]int, 0, len(in))
	for _, v := range in {
		if v <= 0 || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= max {
			break
		}
	}
	return out
}

// mergePosts дописывает новые id в конец, сохраняя порядок. true — если
// что-то добавилось.
func mergePosts(dst *[]int, src []int, max int) bool {
	seen := make(map[int]bool, len(*dst))
	for _, v := range *dst {
		seen[v] = true
	}
	added := false
	for _, v := range src {
		if v <= 0 || seen[v] || len(*dst) >= max {
			continue
		}
		seen[v] = true
		*dst = append(*dst, v)
		added = true
	}
	return added
}

// ── HTTP-обмен между инстансами ──────────────────────────────────────────────

// friendClientTimeout короче обычного: синк идёт в фоне и не должен висеть
// на мёртвом друге дольше, чем цикл опроса.
const friendClientTimeout = 20 * time.Second

// friendHTTPClient не проверяет сертификат: у каждого инстанса свой
// self-signed (tls.go), CA нет. Безопасность обеспечивается НЕ TLS, а тем,
// что канал бесполезен без ключа друга (он в заголовке и проверяется на
// приёме). Адрес и ключ — то, что реально защищает обмен.
var friendHTTPClient = &http.Client{
	Timeout: friendClientTimeout,
	Transport: &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // см. комментарий выше
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     60 * time.Second,
	},
	// Редиректы запрещены: иначе чужой сервер увёл бы наш bearer-ключ на
	// сторонний адрес (утечка ключа через 302).
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// friendRequest делает запрос к инстансу друга. from — адрес ОТПРАВИТЕЛЯ:
// получатель сверяет его со своим списком, чтобы понять, кто пришёл (ключ
// один на пользователя и сам по себе друга не различает).
func friendRequest(method, url, key, from string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Ключ — в заголовке, а не в query: query попадает в access-логи и в
	// историю браузера (так же поступает accounts.go для SSE).
	req.Header.Set("X-Briefly-Friend-Key", key)
	req.Header.Set("X-Briefly-Friend-From", from)
	req.Header.Set("User-Agent", "briefly-friends/1")
	return friendHTTPClient.Do(req)
}

// friendStatusErr превращает код ответа чужого инстанса в машинный код для UI.
//
// Раньше здесь было «друг ответил 403», и пользователь видел голый номер без
// единого слова о причине. 403 на приёме почти всегда означает одно и то же:
// нас не добавили в ответ (friend_not_known), потому что добавление должно быть
// взаимным. Код отдаём текстом, чтобы UI перевёл его на язык пользователя.
func friendStatusErr(op string, status int, errCode string) error {
	switch {
	case status == http.StatusForbidden:
		switch errCode {
		case "friend_not_known":
			return fmt.Errorf("friend_no_back")
		case "host_not_allowed":
			return fmt.Errorf("friend_host_blocked")
		}
		return fmt.Errorf("friend_forbidden")
	case status == http.StatusUnauthorized:
		return fmt.Errorf("friend_bad_key")
	case status == http.StatusRequestEntityTooLarge:
		return fmt.Errorf("friend_too_big")
	case status >= 500:
		return fmt.Errorf("friend_server_error")
	default:
		return fmt.Errorf("friend_status_%d", status)
	}
}

// friendErrCode достаёт код из тела ответа (в нём всегда {"error": ...}).
func friendErrCode(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return body.Error
}

// fetchFromFriend забирает payload друга.
func fetchFromFriend(f Friend) (*friendPayload, error) {
	resp, err := friendRequest(http.MethodGet, f.URL+"/api/friend/share", f.Key, f.URL, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Читаем с ограничением: сервер друга может быть сломан или злонамерен.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, friendStatusErr("share", resp.StatusCode, friendErrCode(raw))
	}
	var out friendPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, ErrFriendBody
	}
	return &out, nil
}

// sendToFriend отдаёт наш payload другу.
func sendToFriend(f Friend, p *friendPayload) (int, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	// Авторизуемся ключом ДРУГА (f.Key): он же выдал его в своём friend-коде,
	// поэтому на приёме friendByKey() узнает владельца ключа.
	resp, err := friendRequest(http.MethodPost, f.URL+"/api/friend/ingest", f.Key, p.Instance, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Код ошибки читаем из тела: по нему UI скажет точную причину, а не «403».
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return 0, friendStatusErr("ingest", resp.StatusCode, friendErrCode(raw))
	}
	return len(body), nil
}

// syncOneFriend выполняет полный обмен с одним другом: сначала тянем его
// данные, потом отдаём свои. Порядок важен: так наши лайки попадают к нему
// уже после того, как мы учли его, — при асинхронной встречной синхронизации
// это убирает взаимное «эхо».
func syncOneFriend(f Friend, instance, user, nickname, avatar string, p *Profile) (mergeStats, error) {
	var st mergeStats
	if theirs, err := fetchFromFriend(f); err == nil {
		got, err := applyFriendPayload(theirs, f.URL, p)
		if err != nil {
			return st, err
		}
		st = got
	} else {
		// Не рвём обмен из-за одной стороны: свои данные всё равно отдадим,
		// иначе один временно выключенный друг «замораживал» бы обмен.
		st.Likes = -1
	}
	out := buildFriendPayload(instance, user, nickname, avatar, p)
	if _, err := sendToFriend(f, out); err != nil {
		if st.Likes < 0 {
			return st, err
		}
	}
	return st, nil
}

// ── Фоновая синхронизация ────────────────────────────────────────────────────

// friendIdentity — кто мы для друзей: логин, ник и аватар из аккаунта.
func friendIdentity(username string) (nickname, avatar string) {
	if u, ok := GetAccounts().Get(username); ok {
		return u.Nickname, u.Avatar
	}
	return "", ""
}

// SyncAllFriends прогоняет обмен со всеми друзьями пользователя. Ошибка
// одного не мешает остальным: иначе один выключенный друг блокировал бы
// обмен со всеми.
func SyncAllFriends(username, selfURL string) map[string]mergeStats {
	store := GetFriendStore(username)
	list := store.List()
	out := make(map[string]mergeStats, len(list))
	if len(list) == 0 {
		return out
	}
	p := GetAccounts().Profile(username)
	nickname, avatar := friendIdentity(username)
	for _, f := range list {
		st, err := syncOneFriend(f, selfURL, username, nickname, avatar, p)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		store.noteSync(f.ID(), msg)
		if err != nil {
			log.Printf("friends: sync %s failed: %v", f.URL, err)
			continue
		}
		out[f.ID()] = st
		if st.total() > 0 {
			// Клиенту нужен сигнал: он перерисует лайки/коллекции.
			publishSSE(map[string]any{"type": "friends", "id": f.ID()})
		}
	}
	return out
}

// ── Определение собственного адреса ──────────────────────────────────────────

// selfURLProvider вычисляет адрес, по которому нас видят друзья. Задаётся из
// main, где известны scheme/порт и есть доступ к автоопределению по сетевым
// картам. Значение нужно ДВУМ местам: фоновому циклу синка и friend-коду.
var selfURLProvider func() string

// SetSelfURLProvider задаёт источник адреса (см. DetectSelfURL).
func SetSelfURLProvider(f func() string) { selfURLProvider = f }

// SelfURL возвращает адрес для друзей, а если он не определён — пустую строку.
func SelfURL() string {
	if selfURLProvider == nil {
		return ""
	}
	return selfURLProvider()
}

// DetectSelfURL возвращает адрес, по которому нас видят друзья. Адрес нужен
// в двух местах: в friend-коде (его человек передаёт вручную) и в payload
// (instance — ключ для чужих комментариев).
//
// Выбираем НЕ 127.0.0.1: друг по нему нас не увидит. Приоритет:
//  1. адрес из BRIEFLY_FRIENDS_URL — явное переопределение пользователем;
//  2. адрес виртуальной VPN-сети (Radmin 26.x, Tailscale 100.x) — именно
//     так друзья соединяются в описанной схеме;
//  3. любой не-loopback адрес сетевой карты (обычная локальная сеть);
//  4. LAN-адрес, каким нас уже видно (запасной вариант).
func DetectSelfURL(scheme, port, listenAddr string) string {
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_FRIENDS_URL")); v != "" {
		if norm, err := normalizeFriendURL(v); err == nil {
			return norm
		}
	}
	iface := os.Getenv("BRIEFLY_FRIENDS_IFACE")
	candidates := externalIPv4(iface)
	if len(candidates) == 0 {
		// Ничего не нашли: отдаём как есть, пусть другу видно локальный адрес
		// из настроек. Лучше неудачный адрес, чем пустой — цикл это учтёт.
		if listenAddr != "" {
			h, _, err := net.SplitHostPort(listenAddr)
			if err == nil {
				return scheme + "://" + net.JoinHostPort(h, port)
			}
		}
		return ""
	}
	return scheme + "://" + net.JoinHostPort(candidates[0], port)
}

// externalIPv4 собирает адреса сетевых карт: сначала VPN-подобные, потом
// остальные. Loopback и link-local пропускаем — по ним друга не достать.
func externalIPv4(wantIface string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var vpn, rest []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if wantIface != "" && ifc.Name != wantIface {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
				continue
			}
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			s := v4.String()
			// 26.x — Radmin VPN, 100.64–100.127 — CGNAT-диапазон, который
			// используют Tailscale/ZeroTier. Такие адреса заведомо «наши»
			// в схеме с VPN, поэтому они приоритетнее домашнего 192.168.x.
			if strings.HasPrefix(s, "26.") || (strings.HasPrefix(s, "100.") && s >= "100.64." && s <= "100.127.") {
				vpn = append(vpn, s)
			} else {
				rest = append(rest, s)
			}
		}
	}
	return append(vpn, rest...)
}

// StartFriendSyncLoop запускает фоновый обмен. Инстансы-друзья работают
// независимо и могут быть выключены, поэтому синк и должен быть фоновым:
// пользователю не нужно ничего нажимать.
func StartFriendSyncLoop() {
	go func() {
		// Первый проход — через минуту после старта: даём сети и TLS
		// подняться, иначе первая же попытка уйдёт в пустоту. Дальше — раз
		// в friendSyncPeriod.
		time.Sleep(time.Minute)
		t := time.NewTicker(friendSyncPeriod)
		defer t.Stop()
		for range t.C {
			self := SelfURL()
			if self == "" {
				continue
			}
			accs := GetAccounts()
			for _, u := range accs.Users() {
				SyncAllFriends(u.Username, self)
			}
		}
	}()
}
