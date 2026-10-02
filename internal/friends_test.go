package internal

// Тесты системы друзей. Проверяем то, что ломается тихо:
//  * код друга нельзя подделать/спутать (парсер и нормализация адреса);
//  * ключ действительно защищает «дверь» (без ключа — 401);
//  * слияние строго аддитивно: чужие данные добавляются, наши не портятся;
//  * повторный обмен не плодит дубли (комментарии дедуплицируются).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestParseFriendCodeRoundTrip(t *testing.T) {
	// Ключ — 64 hex-символа, как выдаёт newFriendKey.
	key := strings.Repeat("ab", 32)
	// Правильная форма кода: префикс, адрес со схемой, ключ, имя.
	code := friendCodePrefix + ":https://26.4.5.6:3000:" + key + ":Vasya"
	addr, gotKey, name, err := ParseFriendCode(code)
	if err != nil {
		t.Fatalf("ParseFriendCode: %v", err)
	}
	if addr != "https://26.4.5.6:3000" {
		t.Errorf("адрес = %q", addr)
	}
	if gotKey != key {
		t.Errorf("ключ не совпал")
	}
	if name != "Vasya" {
		t.Errorf("имя = %q", name)
	}
}

// Код почти всегда копируют через мессенджер или выделяют в браузере, и по
// пути в нём появляются пробелы/переносы. Раньше такой код молча отклонялся
// как «нераспознанный» — хотя человек скопировал его целиком и правильно.
func TestParseFriendCodeToleratesCopyPasteNoise(t *testing.T) {
	key := strings.Repeat("ab", 32)
	clean := friendCodePrefix + ":https://26.4.5.6:3000:" + key + ":Вася Пупкин"

	// 1) Перенос строки где-то в середине.
	noisy := friendCodePrefix + ":https://26.4.5.6\n:3000:" + key + ":Вася"
	addr, gotKey, _, err := ParseFriendCode(noisy)
	if err != nil {
		t.Fatalf("перенос строки в коде: %v", err)
	}
	if addr != "https://26.4.5.6:3000" || gotKey != key {
		t.Errorf("после переноса: addr=%q key=%q", addr, gotKey)
	}

	// 2) Пробелы в схеме и в ключе (чат мог разбить строку).
	spaced := friendCodePrefix + " :https ://26.4.5.6:3000:" +
		key[:32] + " " + key[32:] + ":Вася"
	if _, _, _, err := ParseFriendCode(spaced); err != nil {
		t.Errorf("пробелы в коде: %v", err)
	}

	// 3) Пробелы в имени друга — допустимы, имя их сохраняет.
	_, _, name, err := ParseFriendCode(clean)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Вася Пупкин" {
		t.Errorf("имя = %q, пробелы в имени должны сохраняться", name)
	}
}

func TestParseFriendCodeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"", "привет", "briefly-friend-v2:https://a:1:" + strings.Repeat("ab", 32),
		// Нет ключа.
		friendCodePrefix + ":https://a:1",
		// Ключ не 64 hex.
		friendCodePrefix + ":https://a:1:shortkey:Vasya",
		// Недопустимая схема.
		friendCodePrefix + ":file:///etc/passwd:" + strings.Repeat("ab", 32),
	} {
		if _, _, _, err := ParseFriendCode(bad); err == nil {
			t.Errorf("ParseFriendCode(%q) принял мусор", bad)
		}
	}
}

func TestNormalizeFriendURLDropsTrailingSlashAndCase(t *testing.T) {
	got, err := normalizeFriendURL("HTTPS://26.4.5.6:3000/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://26.4.5.6:3000" {
		t.Errorf("нормализация = %q", got)
	}
	// Без схемы подставляем https — так удобнее вводить вручную.
	got, err = normalizeFriendURL("26.4.5.6:3000")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://26.4.5.6:3000" {
		t.Errorf("без схемы = %q", got)
	}
}

func TestFriendStoreAddListRemove(t *testing.T) {
	dir := t.TempDir()
	s := &FriendStore{path: filepath.Join(dir, "f.json"), byID: map[string]*Friend{}}
	key := strings.Repeat("cd", 32)
	code := friendCodePrefix + ":https://26.9.9.9:3000:" + key + ":Друг"

	f, err := s.AddFriend(code, "https://26.1.1.1:3000", "me", "Я")
	if err != nil {
		t.Fatalf("AddFriend: %v", err)
	}
	if f.URL != "https://26.9.9.9:3000" {
		t.Errorf("URL = %q", f.URL)
	}
	if len(s.List()) != 1 {
		t.Fatalf("список не пополнился")
	}
	// Повторное добавление того же адреса — ошибка, а не дубль.
	if _, err := s.AddFriend(code, "https://26.1.1.1:3000", "me", "Я"); err != ErrFriendExists {
		t.Errorf("повторное добавление = %v, ждали ErrFriendExists", err)
	}
	// Друг не должен совпасть с нами.
	if _, err := s.AddFriend(friendCodePrefix+":https://26.1.1.1:3000:"+strings.Repeat("ef", 32), "https://26.1.1.1:3000", "me", "Я"); err != ErrFriendSelf {
		t.Errorf("добавление себя = %v", err)
	}

	myKey := s.MyKey()
	if myKey == "" || len(myKey) != 64 {
		t.Fatalf("ключ = %q", myKey)
	}
	if !s.Authorize(myKey) {
		t.Error("свой ключ не принят")
	}
	if s.Authorize(key) {
		t.Error("чужой ключ принят — дыра в авторизации")
	}
	if s.Authorize("") {
		t.Error("пустой ключ принят")
	}
	// Ключ стабилен между вызовами и переживает перезапуск (новая store).
	if s.MyKey() != myKey {
		t.Error("ключ нестабилен между вызовами")
	}
	reloaded := &FriendStore{path: s.path, byID: map[string]*Friend{}}
	reloaded.load()
	if reloaded.MyKey() != myKey {
		t.Error("после перезагрузки ключ изменился — друзья перестанут стучаться")
	}
	if len(reloaded.List()) != 1 {
		t.Errorf("после перезагрузки список = %d", len(reloaded.List()))
	}
}

func TestFriendStoreRemove(t *testing.T) {
	dir := t.TempDir()
	s := &FriendStore{path: filepath.Join(dir, "f.json"), byID: map[string]*Friend{}}
	code := friendCodePrefix + ":https://26.9.9.9:3000:" + strings.Repeat("cd", 32) + ":X"
	f, err := s.AddFriend(code, "https://26.1.1.1:3000", "me", "Я")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(f.ID()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(s.List()) != 0 {
		t.Error("друг не удалён")
	}
	if err := s.Remove(f.ID()); err != ErrFriendNotFound {
		t.Errorf("повторное удаление = %v", err)
	}
}

// testProfile — чистый профиль в отдельном файле (тесты не должны трогать
// боевой data/profile.json).
func testProfile(t *testing.T) *Profile {
	t.Helper()
	p := NewProfile(filepath.Join(t.TempDir(), "profile.json"))
	p.mu.Lock()
	p.LikedPosts = map[int]bool{1: true, 2: true}
	p.LikedAt = map[int]int64{1: 100, 2: 200}
	// 1 и 2 лайкнуты лично нами: именно OwnLikes отличает мои лайки от чужих
	// (лайки друга в LikedPosts не попадают — см. applyFriendPayload).
	p.OwnLikes = map[int]bool{1: true, 2: true}
	p.FriendLikes = map[int]bool{}
	p.HiddenPosts = map[int]bool{99: true}
	p.Presets = []QueryPreset{}
	p.FavTags = map[string]bool{"cat": true}
	p.HiddenTags = map[string]bool{}
	p.RecDisliked = map[string]int{}
	p.Collections = []Collection{{ID: "c1", Name: "Моё", Posts: []int{1, 2}}}
	p.mu.Unlock()
	return p
}

func TestApplyFriendPayloadIsAdditive(t *testing.T) {
	p := testProfile(t)
	inst := "https://26.2.2.2:3000"
	in := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", Instance: inst, User: "vasya",
		Likes: []friendLike{{PostID: 3, LikedAt: 300}, {PostID: 1, LikedAt: 50}},
		Collections: []friendCollection{
			{Name: "Моё", Posts: []int{2, 4, 4}}, // совпадает с моим именем — но в мои альбомы не вливается
			{Name: "Общее", Posts: []int{7, 8}},
		},
	}
	st, err := applyFriendPayload(in, inst, p)
	if err != nil {
		t.Fatalf("applyFriendPayload: %v", err)
	}
	// Оба лайка для нас новые как «лайки друга»: 3 его, а 1 — общий (мы тоже
	// лайкнули). Статистика считает именно пришедшее, а не слитое в наш профиль.
	if st.Likes != 2 {
		t.Errorf("новых лайков от друга = %d, ждали 2", st.Likes)
	}
	// Главное: чужой лайк НЕ становится моим — он виден на странице друга, но
	// не в «Моих лайках» (иначе профиль показывал бы вкусы друга).
	if p.LikedPosts[3] {
		t.Error("лайк друга попал в мои лайки")
	}
	if !p.FriendLikes[3] {
		t.Error("лайк друга не учтён как чужой")
	}
	// А свой лайк на посте, который нравится и другу, обязан остаться.
	if !p.LikedPosts[1] {
		t.Error("мой лайк 1 вычищен чужим обменом")
	}
	if !p.OwnLikes[1] {
		t.Error("мой лайк потерял метку OwnLikes")
	}
	// Коллекции друга в мои альбомы НЕ вливаются (как и лайки): остаётся
	// ровно своя «Моё» с прежними постами, чужое «Общее» не подмешивается.
	if len(p.Collections) != 1 {
		t.Fatalf("коллекций = %d, ждали 1 — чужие коллекции не должны входить в мои альбомы", len(p.Collections))
	}
	mine := p.Collections[0]
	if mine.Name != "Моё" || len(mine.Posts) != 2 || mine.Posts[0] != 1 || mine.Posts[1] != 2 {
		t.Errorf("своя коллекция = %+v, ждали «Моё» {1,2} без изменений", mine)
	}
	// Статистика при этом честно считает, сколько коллекций ПРИШЛО (для
	// ответа/логов): данные дошли — они в снимке друга, просто не в моём списке.
	if st.Collections != 2 {
		t.Errorf("получено коллекций = %d, ждали 2", st.Collections)
	}
	// Скрытия и избранные теги обменом НЕ передаются — наши не должны страдать.
	if !p.HiddenPosts[99] {
		t.Error("наши скрытия пропали")
	}
	if !p.FavTags["cat"] {
		t.Error("наши избранные теги пропали")
	}
	// Метка времени моего лайка обменом не трогается вообще: раньше метку друга
	// могли записать поверх, и лента «Лайки» прыгала при синхронизации.
	if p.LikedAt[1] != 100 {
		t.Errorf("liked_at[1] = %d, обмен перетёр мою метку", p.LikedAt[1])
	}
	if _, ok := p.LikedAt[3]; ok {
		t.Errorf("чужому лайку записана метка времени: %v", p.LikedAt)
	}
}

// Регрессия: обмен НИКОГДА не удаляет наши лайки.
//
// Профиль, созданный до разделения лайков: в LikedPosts лежит история, OwnLikes
// пуст. Раньше обмен считал такие посты чужими (поста нет в OwnLikes) и удалял
// их — у реального пользователя это снесло все 555 лайков за один обмен с другом,
// чей список пришёл эхом. Теперь OwnLikes просто достраивается.
func TestApplyFriendPayloadKeepsLikesOfLegacyProfile(t *testing.T) {
	p := testProfile(t)
	p.mu.Lock()
	// До разделения лайков: лайки есть, метки «свой» — нет.
	p.OwnLikes = map[int]bool{}
	p.LikedAt = map[int]int64{1: 100, 2: 200}
	p.mu.Unlock()

	inst := "https://26.2.2.2:3000"
	in := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", Instance: inst, User: "vasya",
		// Друг прислал ровно наши лайки (эхо старой версии) — плюс свой.
		Likes: []friendLike{{PostID: 1, LikedAt: 1}, {PostID: 2, LikedAt: 2}, {PostID: 777, LikedAt: 3}},
	}
	if _, err := applyFriendPayload(in, inst, p); err != nil {
		t.Fatalf("applyFriendPayload: %v", err)
	}
	if !p.LikedPosts[1] || !p.LikedPosts[2] {
		t.Fatalf("обмен удалил наши лайки: %v (профиль до разделения OwnLikes)", p.LikedPosts)
	}
	if p.LikedAt[1] != 100 || p.LikedAt[2] != 200 {
		t.Errorf("обмен стёр время лайка: liked_at=%v", p.LikedAt)
	}
	// Чужой лайк, которого у нас не было, в «Мои лайки» не попадает.
	if p.LikedPosts[777] {
		t.Error("лайк друга добавлен в мои лайки")
	}
	if !p.FriendLikes[777] {
		t.Error("лайк друга не помечен как чужой")
	}
	// А наши — помечены своими, чтобы следующий обмен не путался.
	if !p.OwnLikes[1] || !p.OwnLikes[2] {
		t.Errorf("миграция OwnLikes не сработала: %v", p.OwnLikes)
	}
}

// Повторный обмен не рапортует о тех же лайках заново и не тащит чужое в «Мои
// лайки» даже если я снял свой лайк: пост остаётся чужим (FriendLikes).
func TestApplyFriendPayloadIsIdempotentForLikes(t *testing.T) {
	p := testProfile(t)
	inst := "https://26.2.2.2:3000"
	in := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", Instance: inst, User: "vasya",
		Likes: []friendLike{{PostID: 3, LikedAt: 300}},
	}
	if st, err := applyFriendPayload(in, inst, p); err != nil || st.Likes != 1 {
		t.Fatalf("первый обмен: st=%+v err=%v, ждали один новый лайк", st, err)
	}
	if st, err := applyFriendPayload(in, inst, p); err != nil || st.Likes != 0 {
		t.Fatalf("повторный обмен: st=%+v err=%v, новых лайков быть не должно", st, err)
	}
	if p.LikedPosts[3] {
		t.Error("чужой лайк вернулся в мои лайки после повторного обмена")
	}
}

func TestApplyFriendPayloadRejectsBadInput(t *testing.T) {
	p := testProfile(t)
	inst := "https://26.2.2.2:3000"
	// Пустой адрес отправителя — брать его неоткуда: значит, ключ не наш.
	if _, err := applyFriendPayload(&friendPayload{Version: friendPayloadVersion}, "", p); err != ErrFriendBody {
		t.Errorf("пустой instance принят: %v", err)
	}
	// Не та версия формата.
	in := &friendPayload{Version: 99, Instance: inst, Likes: []friendLike{{PostID: 5}}}
	if _, err := applyFriendPayload(in, inst, p); err != ErrFriendBody {
		t.Errorf("чужая версия принята: %v", err)
	}
	if p.LikedPosts[5] {
		t.Error("лайк отклонённого payload всё же записался")
	}
	// instance в теле может быть чужим (sender видит себя своим адресом, а
	// Host в запросе — адрес получателя), поэтому его НЕ сверяем: сверяем
	// адрес из нашего списка друзей, который передан вторым аргументом.
	in = &friendPayload{
		Version: friendPayloadVersion, Instance: "https://что-то-else:3000",
		Likes: []friendLike{{PostID: 6}},
	}
	if _, err := applyFriendPayload(in, inst, p); err != nil {
		t.Errorf("различающийся instance отклонён: %v", err)
	}
	if !p.FriendLikes[6] {
		t.Error("валидный payload с чужим instance не применён")
	}
	if p.LikedPosts[6] {
		t.Error("лайк друга из payload попал в мои лайки")
	}
}

func TestApplyFriendPayloadCapsOversizedInput(t *testing.T) {
	p := testProfile(t)
	inst := "https://26.2.2.2:3000"
	in := &friendPayload{Version: friendPayloadVersion, Instance: inst}
	for i := 0; i < maxFriendLikes*4+10; i++ {
		in.Likes = append(in.Likes, friendLike{PostID: i + 1})
	}
	if _, err := applyFriendPayload(in, inst, p); err != ErrFriendBody {
		t.Errorf("раздутый payload принят: %v", err)
	}
}

// friendTestServer — инстанс-друг для HTTP-тестов: проверяет ключ в
// заголовке и отдаёт заранее заданный payload.
func friendTestServer(t *testing.T, wantKey string, payload *friendPayload, gotPayload *friendPayload) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Briefly-Friend-Key") != wantKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/friend/share":
			json.NewEncoder(w).Encode(payload)
		case "/api/friend/ingest":
			if json.NewDecoder(r.Body).Decode(gotPayload) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSyncOneFriendBothDirections(t *testing.T) {
	theirs := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", User: "vasya",
		Likes: []friendLike{{PostID: 42, LikedAt: 5}},
	}
	var sent friendPayload
	// Ключ, которым мы авторизуемся У друга: он же выдал его в своём коде.
	theirKey := strings.Repeat("11", 32)
	srv := friendTestServer(t, theirKey, theirs, &sent)
	defer srv.Close()
	// instance обязан совпадать с адресом, с КОГОГО пришёл запрос (этим же
	// проверяется подмена Host): тестовый сервер живёт на localhost, поэтому
	// друг «заявляет» о себе именно этот адрес — ровно так же, как настоящий
	// инстанс отдаёт адрес, по которому к нему пришли.
	theirs.Instance = srv.URL

	p := testProfile(t)
	p.mu.Lock()
	p.LikedPosts[43] = true
	p.LikedAt[43] = 7
	p.mu.Unlock()

	f := Friend{URL: srv.URL, Key: theirKey}
	st, err := syncOneFriend(f, "https://26.1.1.1:3000", "me", "Я", "", p)
	if err != nil {
		t.Fatalf("syncOneFriend: %v", err)
	}
	if !p.FriendLikes[42] {
		t.Error("лайк друга не доехал до профиля")
	}
	if p.LikedPosts[42] {
		t.Error("лайк друга влился в мои лайки")
	}
	if st.Likes != 1 {
		t.Errorf("статистика = %+v, ждали один новый лайк", st)
	}
	// Другу уходят ТОЛЬКО мои лайки: чужой лайк не считается нашим, поэтому и
	// пересылать его обратно нечего (раньше эхо гасило встречный обмен, теперь
	// эхо невозможно в принципе).
	if !containsLike(sent.Likes, 43) {
		t.Error("наш лайк не отправлен другу")
	}
	if containsLike(sent.Likes, 42) {
		t.Error("лайк друга отправлен обратно как наш")
	}
}

func containsLike(ls []friendLike, id int) bool {
	for _, l := range ls {
		if l.PostID == id {
			return true
		}
	}
	return false
}

// ── Обмен по требованию (открытие профиля / кнопка «Обновить») ───────────────

// isolateFriendsData уводит data/ во временный каталог и пересоздаёт
// синглтоны: SyncOneFriendCtx ходит в аккаунты (ник, аватар) и legacy-профиль,
// а тесты не должны трогать боевой каталог.
func isolateFriendsData(t *testing.T) {
	t.Helper()
	oldWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	resetGlobalTestState(t)
}

// registerOnDemandStore кладёт хранилище друзей в глобальную карту: иначе
// GetFriendStore создал бы пустое новое и не нашло бы нашего друга.
func registerOnDemandStore(t *testing.T, username string) *FriendStore {
	t.Helper()
	s := &FriendStore{path: filepath.Join(t.TempDir(), "f.json"), byID: map[string]*Friend{}}
	friendStoresMu.Lock()
	friendStores[username] = s
	friendStoresMu.Unlock()
	t.Cleanup(func() {
		friendStoresMu.Lock()
		delete(friendStores, username)
		friendStoresMu.Unlock()
	})
	return s
}

func addTestFriend(t *testing.T, s *FriendStore, url, key, name string) Friend {
	t.Helper()
	fr, err := s.AddFriend(friendCodePrefix+":"+url+":"+key+":"+name, "https://26.1.1.1:3000", "me", "Я")
	if err != nil {
		t.Fatalf("AddFriend: %v", err)
	}
	return *fr
}

// Обмен по требованию: снимок друга сохранён, наши лайки ушли, LastSync записан.
func TestSyncOneFriendOnDemand(t *testing.T) {
	isolateFriendsData(t)
	theirs := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", User: "vasya",
		Likes:       []friendLike{{PostID: 4242, LikedAt: 300}},
		Collections: []friendCollection{{Name: "его альбом", Posts: []int{4242}}},
	}
	var got friendPayload
	key := strings.Repeat("55", 32)
	srv := friendTestServer(t, key, theirs, &got)
	defer srv.Close()

	store := registerOnDemandStore(t, "demand")
	fr := addTestFriend(t, store, srv.URL, key, "Вася")

	ctx, cancel := context.WithTimeout(context.Background(), friendSyncBudget)
	defer cancel()
	st, err := SyncOneFriendCtx(ctx, "demand", fr.ID(), "https://26.1.1.1:3000")
	if err != nil {
		t.Fatalf("SyncOneFriendCtx: %v", err)
	}
	if st.Likes != 1 {
		t.Errorf("статистика = %+v, ждали один новый лайк", st)
	}
	cur, _ := store.Get(fr.ID())
	if cur.Snapshot == nil || len(cur.Snapshot.Likes) != 1 || cur.Snapshot.Likes[0] != 4242 {
		t.Fatalf("снимок = %+v, ждали его лайк 4242", cur.Snapshot)
	}
	// Альбом друга — в его снимке и только там (в наших альбомах его быть не должно).
	if len(cur.Snapshot.Collections) != 1 || cur.Snapshot.Collections[0].Name != "его альбом" {
		t.Errorf("коллекции снимка = %+v", cur.Snapshot.Collections)
	}
	// Наш payload другу ушёл целиком (заполненность — из профиля АККАУНТА, см.
	// SyncOneFriendCtx; содержимое проверено в TestSyncOneFriendBothDirections).
	if got.Version != friendPayloadVersion {
		t.Errorf("другу ушёл мусор вместо payload: version=%d", got.Version)
	}
	if cur.LastSync == "" {
		t.Error("LastSync не записан: в профиле не покажется «обмен: когда»")
	}
	if cur.LastError != "" {
		t.Errorf("после успешного обмена записана ошибка: %q", cur.LastError)
	}
}

// Мёртвый друг не должен держать открытие профиля: дедлайн обрывает обмен, причина
// попадает в LastError (её покажет UI), а снимок остаётся прежним — лучше старые
// данные, чем пустая страница.
func TestSyncOneFriendOnDemandRespectsDeadline(t *testing.T) {
	isolateFriendsData(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	store := registerOnDemandStore(t, "slow")
	fr := addTestFriend(t, store, slow.URL, strings.Repeat("66", 32), "Медленный")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := SyncOneFriendCtx(ctx, "slow", fr.ID(), "https://26.1.1.1:3000"); err == nil {
		t.Fatal("обмен с недоступным другом должен вернуть ошибку")
	}
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Errorf("обмен шёл %v — дедлайн открытия профиля не сработал", d)
	}
	cur, _ := store.Get(fr.ID())
	if cur.LastError == "" {
		t.Error("причина не записана в LastError: UI не сможет объяснить сбой")
	}
	if cur.Snapshot != nil {
		t.Error("снимок записан при неудачном обмене — профиль друга должен остаться прежним")
	}
}

// Открытие профиля друга само обновляет его данные: синк живёт в обработчике
// GET /friends/:id/profile (фонового цикла больше нет).
func TestFriendProfileSyncsOnOpen(t *testing.T) {
	isolateFriendsData(t)
	theirs := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", User: "vasya",
		Likes: []friendLike{{PostID: 4242, LikedAt: 300}},
	}
	var got friendPayload
	key := strings.Repeat("77", 32)
	srv := friendTestServer(t, key, theirs, &got)
	defer srv.Close()

	store := registerOnDemandStore(t, "demand")
	fr := addTestFriend(t, store, srv.URL, key, "Вася")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/friends/:id/profile", func(c *gin.Context) {
		c.Set("briefly_user", "demand")
		NewHandler().FriendProfile(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/friends/"+fr.ID()+"/profile", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("код %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Friend struct {
			Likes []int `json:"likes"`
		} `json:"friend"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if len(out.Friend.Likes) != 1 || out.Friend.Likes[0] != 4242 {
		t.Errorf("открытие профиля показало %v, ждали свежие лайки друга [4242]", out.Friend.Likes)
	}
	cur, _ := store.Get(fr.ID())
	if cur.Snapshot == nil || len(cur.Snapshot.Likes) != 1 {
		t.Error("снимок друга не сохранён при открытии профиля")
	}
}

// Кнопка «Обновить» (POST /friends/sync/:id) обменивается с ОДНИМ другом и
// отдаёт свежий профиль в том же ответе — второй запрос клиенту не нужен.
func TestSyncOneFriendEndpointReturnsFreshProfile(t *testing.T) {
	isolateFriendsData(t)
	theirs := &friendPayload{
		Version: friendPayloadVersion, App: "briefly", User: "vasya",
		Likes: []friendLike{{PostID: 909, LikedAt: 1}},
	}
	var got friendPayload
	key := strings.Repeat("88", 32)
	srv := friendTestServer(t, key, theirs, &got)
	defer srv.Close()

	store := registerOnDemandStore(t, "demand")
	fr := addTestFriend(t, store, srv.URL, key, "Вася")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/friends/sync/:id", func(c *gin.Context) {
		c.Set("briefly_user", "demand")
		NewHandler().SyncOneFriend(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/friends/sync/"+fr.ID(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("код %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		OK     bool `json:"ok"`
		Friend struct {
			Likes []int `json:"likes"`
		} `json:"friend"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK {
		t.Error("обмен по кнопке должен быть успешным: ok=false")
	}
	if len(out.Friend.Likes) != 1 || out.Friend.Likes[0] != 909 {
		t.Errorf("ответ не содержит свежего снимка: %v", out.Friend.Likes)
	}

	// Несуществующий друг — ошибка, а не пустой успех.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/friends/sync/нет-такого", nil))
	if w.Code == http.StatusOK {
		t.Error("обмен с несуществующим другом не должен отвечать 200")
	}
}

// Несуществующий друг — ошибка, а не пустой профиль и не паника.
func TestSyncOneFriendOnDemandUnknownID(t *testing.T) {
	isolateFriendsData(t)
	registerOnDemandStore(t, "ghost")
	if _, err := SyncOneFriendCtx(context.Background(), "ghost", "нет-такого", "https://26.1.1.1:3000"); err != ErrFriendNotFound {
		t.Errorf("несуществующий друг: %v, ждали ErrFriendNotFound", err)
	}
}

func TestSyncOneFriendToleratesDeadFriend(t *testing.T) {
	// Друг выключен: наши данные не должны пострадать.
	p := testProfile(t)
	p.mu.Lock()
	p.LikedPosts[77] = true
	p.mu.Unlock()
	dead := Friend{URL: "http://127.0.0.1:1", Key: strings.Repeat("22", 32)}
	//nolint:errcheck // интересует сохранность данных, а не код ошибки
	syncOneFriend(dead, "https://26.1.1.1:3000", "me", "Я", "", p)
	if !p.LikedPosts[77] {
		t.Error("наш лайк пропал при неудачном синке")
	}
	if !p.LikedPosts[1] {
		t.Error("профиль повреждён неудачным синком")
	}
}

func TestDetectSelfURLOverride(t *testing.T) {
	// Явное переопределение пользователем выигрывает у автоопределения.
	t.Setenv("BRIEFLY_FRIENDS_URL", "https://26.77.77.77:3000/")
	if got := DetectSelfURL("https", "3000", "0.0.0.0:3000"); got != "https://26.77.77.77:3000" {
		t.Errorf("DetectSelfURL = %q", got)
	}
	// Метод без совпадений — адрес не выдумываем. Переопределение из
	// BRIEFLY_FRIENDS_URL сбрасываем: оно по замыслу сильнее фильтра по
	// интерфейсу, иначе проверка ниже проверяла бы не то.
	t.Setenv("BRIEFLY_FRIENDS_URL", "")
	t.Setenv("BRIEFLY_FRIENDS_IFACE", "нет-такой-карты")
	if u := DetectSelfURL("https", "3000", ""); u != "" {
		t.Errorf("при отсутствии карт ожидался пустой адрес, получено %q", u)
	}
}

// Код друга должен нести адрес, по которому нас ВИДНО (Radmin/Tailscale или
// BRIEFLY_FRIENDS_URL), а не тот, по которому открыт браузер. Иначе код,
// отданный другу, содержал бы localhost — и синк молча не работал бы.
func TestFriendCodeUsesPublicAddress(t *testing.T) {
	dir := t.TempDir()
	s := &FriendStore{path: filepath.Join(dir, "f.json"), byID: map[string]*Friend{}}

	// Что видит друг.
	SetSelfURLProvider(func() string { return "https://26.7.7.7:3000" })
	t.Cleanup(func() { SetSelfURLProvider(nil) })
	if SelfURL() != "https://26.7.7.7:3000" {
		t.Fatalf("SelfURL = %q", SelfURL())
	}

	code, err := s.EncodeFriendCode("me", "Я", SelfURL())
	if err != nil {
		t.Fatal(err)
	}
	addr, _, _, err := ParseFriendCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "https://26.7.7.7:3000" {
		t.Errorf("в коде адрес %q, ждали публичный https://26.7.7.7:3000", addr)
	}
	if strings.Contains(code, "localhost") || strings.Contains(code, "127.0.0.1") {
		t.Errorf("код содержит локальный адрес: %q", code)
	}
}

// friendPublicURL обязан отдавать адрес из SelfURL(), даже когда браузер
// открыт по localhost. Это и есть источник «странного добавления»: код
// выглядел правильно, но у друга в нём оказывался localhost, и синк не шёл.
func TestFriendPublicURLIgnoresRequestHost(t *testing.T) {
	SetSelfURLProvider(func() string { return "https://26.8.8.8:3000" })
	t.Cleanup(func() { SetSelfURLProvider(nil) })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/friends/code", nil)
	c.Request.Host = "localhost:3000"

	got := friendPublicURL(c)
	if got != "https://26.8.8.8:3000" {
		t.Errorf("friendPublicURL = %q при Host=localhost, ждали публичный адрес", got)
	}
	// Без провайдера — откатываемся на адрес запроса, чтобы код был хоть какой-то.
	SetSelfURLProvider(func() string { return "" })
	if got := friendPublicURL(c); got != "http://localhost:3000" {
		t.Errorf("без провайдера = %q, ждали адрес запроса", got)
	}
}

// MyFriendCode должен отдавать код с ПУБЛИЧНЫМ адресом, даже если браузер
// открыт по localhost. Регрессия: обработчик звал friendSelfURL(c), и друг
// получал код с localhost — в списке он появлялся, а обмен не шёл.
func TestMyFriendCodeReturnsPublicAddress(t *testing.T) {
	SetSelfURLProvider(func() string { return "https://26.9.9.9:3000" })
	t.Cleanup(func() { SetSelfURLProvider(nil) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/friends/code", (&Handler{}).MyFriendCode)
	req := httptest.NewRequest(http.MethodGet, "/api/friends/code", nil)
	req.Host = "localhost:3000" // браузер открыт локально
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("код ответа = %d, тело: %s", w.Code, w.Body.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, w.Body.String())
	}
	if !strings.Contains(out.Code, "26.9.9.9") {
		t.Errorf("в коде нет публичного адреса: %q", out.Code)
	}
	if strings.Contains(out.Code, "localhost") {
		t.Errorf("код содержит localhost: %q", out.Code)
	}
	// Код должен разбираться обратно в тот же адрес.
	addr, _, _, err := ParseFriendCode(out.Code)
	if err != nil {
		t.Fatalf("ParseFriendCode: %v", err)
	}
	if addr != "https://26.9.9.9:3000" {
		t.Errorf("адрес из кода = %q, ждали https://26.9.9.9:3000", addr)
	}
}

func TestFriendsFilePermissions(t *testing.T) {
	// В файле лежат bearer-ключи: права должны быть 0600 (Windows права
	// не применяет, поэтому проверка только на unix).
	dir := t.TempDir()
	s := &FriendStore{path: filepath.Join(dir, "f.json"), byID: map[string]*Friend{}}
	s.MyKey()
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if os.PathSeparator == '/' && info.Mode().Perm() != 0o600 {
		t.Errorf("права = %v, ждали 0600", info.Mode().Perm())
	}
}

func TestFriendCommentUsernameIsNamespaced(t *testing.T) {
	// Чужой логин должен отличаться от нашего, иначе «vasya» с чужого
	// инстанса слился бы с нашим «vasya» и его нельзя было бы удалить.
	if friendCommentUsername("https://a:3000", "vasya") == "vasya" {
		t.Error("чужой логин не отделён от нашего")
	}
	if !isFriendComment(friendCommentUsername("https://a:3000", "vasya")) {
		t.Error("isFriendComment не узнал чужой логин")
	}
	if isFriendComment("vasya") {
		t.Error("свой логин принят за чужой")
	}
}
