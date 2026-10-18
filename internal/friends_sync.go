package internal

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

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

	// Скрытия и теги показываем в профиле друга: по ним видно вкус лучше,
	// чем по одним лайкам. Порядок стабильный (сортировкой) — иначе список
	// прыгал бы при каждом обмене и вкладка друзей мерцала бы.
	hidden := make([]int, 0, len(p.HiddenPosts))
	for id, v := range p.HiddenPosts {
		if v {
			hidden = append(hidden, id)
		}
	}
	sort.Ints(hidden)
	if len(hidden) > maxFriendLikes {
		hidden = hidden[:maxFriendLikes]
	}

	return &friendPayload{
		Version: friendPayloadVersion, App: "briefly", Instance: instance,
		User: user, Nickname: nickname, Avatar: avatar,
		SyncedAt: time.Now().UTC().Format(time.RFC3339),
		Likes:    likes, Collections: colls, Comments: comments,
		HiddenPosts:  hidden,
		FavTags:      sortedTagKeys(p.FavTags),
		HiddenTags:   sortedTagKeys(p.HiddenTags),
		DislikedTags: topDislikedTags(p.RecDisliked),
	}
}

// topDislikedTags отдаёт теги с обратной связью по убыванию счётчика.
// При равенстве сортируем по имени, иначе порядок скачет между обменами.
func topDislikedTags(m map[string]int) []friendDislikedTag {
	if len(m) == 0 {
		return nil
	}
	out := make([]friendDislikedTag, 0, len(m))
	for tag, n := range m {
		if n > 0 && strings.TrimSpace(tag) != "" {
			out = append(out, friendDislikedTag{Tag: truncateRunes(tag, 80), Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	if len(out) > maxFriendDislikedTags {
		out = out[:maxFriendDislikedTags]
	}
	return out
}

// sortedTagKeys возвращает ключи карты тегов в стабильном порядке.
func sortedTagKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v && strings.TrimSpace(k) != "" {
			out = append(out, truncateRunes(k, 80))
		}
	}
	sort.Strings(out)
	return out
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// applyFriendPayload вливает данные друга в наш профиль. Строго ADDITIVE по
// комментариям: ничего не удаляем и не перезаписываем — иначе один сбой у
// друга стёр бы нашу библиотеку.
//
// Как и лайки, КОЛЛЕКЦИИ друга в наши альбомы не вливаются: альбом — личное,
// как и вкус. Раньше их слияли по имени, и чужие подборки оказывались в моём
// списке альбомов («Моё» друга сливалась с «Моё» моим). Теперь чужие
// коллекции живут там, где им место, — на странице друга (снимок,
// noteSnapshot), отдельно от моих.
//
// ЛАЙКИ друга в «Мои лайки» не попадают: они запоминаются как чужие
// (FriendLikes) и показываются на странице друга (снимок, noteSnapshot).
// И — важно — мы НИЧЕГО из своего при обмене не удаляем. Раньше здесь стояло
// «убрать из LikedPosts, если поста нет в OwnLikes», и для профилей, созданных
// до разделения лайков (OwnLikes пуст), это сносило всю историю пользователя:
// его собственные лайки приходили от друга эхом (старая версия сливала списки) и
// считались чужими. Теперь правило обратное: что есть в LikedPosts — наше
// (миграция в Profile.load), а OwnLikes лишь помечает это для будущих обменов.
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
	if p.FriendLikes == nil {
		p.FriendLikes = make(map[int]bool)
	}
	if p.OwnLikes == nil {
		p.OwnLikes = make(map[int]bool)
	}
	for _, l := range in.Likes {
		if l.PostID <= 0 {
			continue
		}
		// Новый для нас лайк друга — это «пришло от друзей» в статистике обмена.
		// Считаем по FriendLikes, а не по LikedPosts: иначе каждый обмен
		// рапортовал бы заново обо всех лайках друга.
		if !p.FriendLikes[l.PostID] {
			p.FriendLikes[l.PostID] = true
			st.Likes++
		}
		// В «Мои лайки» чужое не входит, но и НИЧЕГО из своего мы не теряем.
		// Раньше здесь стояло delete(p.LikedPosts, ...) для постов без OwnLikes —
		// и для профилей, созданных до разделения лайков (OwnLikes пуст), это
		// удаляло всю историю пользователя: его же лайки приходили от друга
		// эхом и считались чужими. Теперь наоборот: что лежит в LikedPosts —
		// наше, и мы лишь помечаем это OwnLikes (миграция в Profile.load).
		// Лайки, которых у нас нет, в профиль не добавляются: они видны на
		// странице друга (снимок, noteSnapshot).
		if p.LikedPosts[l.PostID] && !p.OwnLikes[l.PostID] {
			p.OwnLikes[l.PostID] = true
		}
	}
	// Коллекции друга в мои альбомы НЕ вливаем (см. док к функции): альбом —
	// личное, и слияние по имени подмешивало чужие подборки к своим. Считаем
	// только, сколько коллекций пришло, — для статистики обмена и логов; сами
	// они хранятся в снимке (noteSnapshot) и видны на странице друга.
	for _, fc := range in.Collections {
		if truncateRunes(fc.Name, 80) == "" {
			continue
		}
		st.Collections++
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

// ── HTTP-обмен между инстансами ──────────────────────────────────────────────

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
	return friendRequestCtx(context.Background(), method, url, key, from, body)
}

// friendRequestCtx — то же с контекстом: обмен по требованию (открытие профиля
// друга) ограничен по времени, иначе мёртвый друг держал бы вьювер на спиннере,
// пока отдаёт свой 20-секундный таймаут.
func friendRequestCtx(ctx context.Context, method, url, key, from string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
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
	return fetchFromFriendCtx(context.Background(), f)
}

func fetchFromFriendCtx(ctx context.Context, f Friend) (*friendPayload, error) {
	resp, err := friendRequestCtx(ctx, http.MethodGet, f.URL+"/api/friend/share", f.Key, f.URL, nil)
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
	return sendToFriendCtx(context.Background(), f, p)
}

func sendToFriendCtx(ctx context.Context, f Friend, p *friendPayload) (int, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	// Авторизуемся ключом ДРУГА (f.Key): он же выдал его в своём friend-коде,
	// поэтому на приёме friendByKey() узнает владельца ключа.
	resp, err := friendRequestCtx(ctx, http.MethodPost, f.URL+"/api/friend/ingest", f.Key, p.Instance, body)
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
	return syncOneFriendCtx(context.Background(), f, instance, user, nickname, avatar, p)
}

func syncOneFriendCtx(ctx context.Context, f Friend, instance, user, nickname, avatar string, p *Profile) (mergeStats, error) {
	var st mergeStats
	if theirs, err := fetchFromFriendCtx(ctx, f); err == nil {
		got, err := applyFriendPayload(theirs, f.URL, p)
		if err != nil {
			return st, err
		}
		st = got
		// Снимок его вкусов сохраняем ДО отправки своих данных: он нужен,
		// чтобы показать профиль друга, — и он теперь единственное место, где
		// живут его лайки (в наш профиль они не вливаются).
		GetFriendStore(user).noteSnapshot(f.ID(), snapshotFrom(theirs))
	} else {
		// Не рвём обмен из-за одной стороны: свои данные всё равно отдадим,
		// иначе один временно выключенный друг «замораживал» бы обмен.
		st.Likes = -1
	}
	out := buildFriendPayload(instance, user, nickname, avatar, p)
	if _, err := sendToFriendCtx(ctx, f, out); err != nil {
		if st.Likes < 0 {
			return st, err
		}
	}
	return st, nil
}

// ── Фоновая синхронизация ────────────────────────────────────────────────────
// (фоновой синхронизации больше нет: обмен идёт по требованию — при открытии
// профиля друга и по кнопке «Обновить». См. SyncOneFriendCtx.)

// friendIdentity — кто мы для друзей: логин, ник и аватар из аккаунта.
func friendIdentity(username string) (nickname, avatar string) {
	if u, ok := GetAccounts().Get(username); ok {
		return u.Nickname, u.Avatar
	}
	return "", ""
}

// SyncOneFriendCtx прогоняет обмен с одним другом (по id) и записывает
// результат в запись: LastSync или текст ошибки. Именно эта функция — единственный
// путь обмена со временем жизни запроса, который задаёт вызывающий (открытие
// профиля, кнопка «Обновить»).
//
// Ошибка не стирает данные: снимок друга остаётся прежним, а UI покажет причину
// в строке «обмен» — молчаливая пустая страница была бы хуже старого списка.
func SyncOneFriendCtx(ctx context.Context, username, id, selfURL string) (mergeStats, error) {
	store := GetFriendStore(username)
	f, ok := store.Get(id)
	if !ok {
		return mergeStats{}, ErrFriendNotFound
	}
	p := GetAccounts().Profile(username)
	nickname, avatar := friendIdentity(username)
	st, err := syncOneFriendCtx(ctx, f, selfURL, username, nickname, avatar, p)
	msg := ""
	if err != nil {
		msg = err.Error()
		log.Printf("friends: sync %s failed: %v", f.URL, err)
	}
	store.noteSync(id, msg)
	return st, err
}

// SyncAllFriends прогоняет обмен со всеми друзьями пользователя — только по
// явной кнопке. Фонового цикла больше нет: обмен без запроса пользователя
// означал, что данные друга тихо меняются у него перед глазами.
//
// Ошибка одного не мешает остальным: иначе один выключенный друг блокировал бы
// обмен со всеми.
func SyncAllFriends(username, selfURL string) map[string]mergeStats {
	out := make(map[string]mergeStats)
	for _, f := range GetFriendStore(username).List() {
		ctx, cancel := context.WithTimeout(context.Background(), friendSyncBudget)
		st, err := SyncOneFriendCtx(ctx, username, f.ID(), selfURL)
		cancel()
		if err != nil {
			continue
		}
		out[f.ID()] = st
	}
	return out
}

// StartFriendSyncLoop больше не существует: обмен с друзьями идёт только по
// требованию — при открытии профиля друга (GET /api/friends/:id/profile) и по
// кнопке «Обновить» (POST /api/friends/sync/:id). Фоновый цикл каждые пять
// минут менял данные друга у пользователя перед глазами без всякого запроса с
// его стороны; теперь он видит актуальное ровно тогда, когда сам это попросил.
