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
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

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
	// Копия структуры; Snapshot остаётся общим указателем. Это безопасно:
	// снимок после создания не меняется, noteSnapshot подменяет сам указатель.
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

// noteSnapshot сохраняет профиль друга, полученный при обмене. Отдельный метод
// от noteSync, потому что вызывается по горячему пути обмена: снимок нужен
// всегда, а LastSync/LastError общий.
//
// Замена целиком, а не слияние: снимок — зеркало того, что друг прислал сейчас.
// Накапливать его было бы неверно, ведь friend-данные приходят только
// аддитивно, а лайки друга могли быть уже сняты у него.
func (s *FriendStore) noteSnapshot(id string, snap *FriendSnapshot) {
	if snap == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.byID[id]
	if !ok {
		return
	}
	f.Snapshot = snap
	_ = s.save()
}

// snapshotFrom переводит payload друга в снимок для UI.
func snapshotFrom(in *friendPayload) *FriendSnapshot {
	if in == nil {
		return nil
	}
	snap := &FriendSnapshot{
		SyncedAt: in.SyncedAt,
		Nickname: in.Nickname,
		FavTags:  in.FavTags,
	}
	// Лайки: временные метки не нужны, UI рисует сетку по id.
	for _, l := range in.Likes {
		if l.PostID > 0 {
			snap.Likes = append(snap.Likes, l.PostID)
		}
	}
	// «Дизлайк» у нас — это скрытие поста плюс штраф его тегам. Скрытые посты
	// идёт в Disliked, штрафные теги — в DislikedTags: вместе они и есть
	// «что другу не нравится». Его скрытые ТЕГИ не показываем — это личный
	// фильтр ленты, а не вкус, и в профиле другу он ни к чему.
	snap.Disliked = in.HiddenPosts
	snap.DislikedTags = in.DislikedTags
	for _, c := range in.Collections {
		snap.Collections = append(snap.Collections, FriendSnapshotColl{
			Name: c.Name, Count: len(c.Posts),
		})
	}
	seen := make(map[int]bool)
	for _, c := range in.Comments {
		if c.PostID > 0 && !seen[c.PostID] {
			seen[c.PostID] = true
			snap.CommentPosts = append(snap.CommentPosts, c.PostID)
		}
	}
	return snap
}
