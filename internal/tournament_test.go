package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func tournamentTestDB(t *testing.T, n int, withScore bool) *PostDB {
	t.Helper()
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { db.Close() })
	for i := 1; i <= n; i++ {
		score := 0
		if withScore {
			score = i * 7
		}
		db.UpsertMeta(&Post{ID: i, Tags: fmt.Sprintf("tag%d", i%3), Score: score,
			FileURL: fmt.Sprintf("https://x/%d.jpg", i)})
		db.SetDownloaded(i, fmt.Sprintf("data/save/%d.jpg", i), fmt.Sprintf("thumbs/%d.jpg", i))
	}
	return db
}

// Разбор пресета раундов: вне диапазона — ошибка, а не молчаливое усечение.
func TestTournamentRoundsPreset(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"", 3, true}, {"2", 2, true}, {"3", 3, true}, {"5", 5, true},
		{"1", 0, false}, {"6", 0, false}, {"abc", 0, false}, {"-1", 0, false}, {"0", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseTournamentRounds(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("rounds=%q: получено (%d, %v), ожидалось (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Структура сетки: каждый следующий уровень вдвое короче, в финале ровно одна
// пара, слоты изначально пустые (-1), число участников сходится к 2^round.
func TestEmptyTournamentBracketShape(t *testing.T) {
	for rounds := tournamentMinRounds; rounds <= tournamentMaxRounds; rounds++ {
		b := emptyTournamentBracket(rounds)
		if len(b) != rounds {
			t.Fatalf("rounds=%d: уровней %d, ожидалось %d", rounds, len(b), rounds)
		}
		total := 0
		for r, level := range b {
			want := 1 << (rounds - r - 1)
			if len(level) != want {
				t.Errorf("rounds=%d уровень=%d: пар %d, ожидалось %d", rounds, r, len(level), want)
			}
			for i, pair := range level {
				if len(pair) != 2 {
					t.Errorf("rounds=%d уровень=%d пара=%d: сторон %d, ожидалось 2", rounds, r, i, len(pair))
				}
				for side, slot := range pair {
					if slot != -1 {
						t.Errorf("rounds=%d уровень=%d пара=%d сторона=%d: слот %d, ожидался -1 (пусто)",
							rounds, r, i, side, slot)
					}
				}
			}
		}
		for _, level := range b {
			// Именно пар, НЕ участников: сетка на 8 = 4 пары первого круга,
			// участников вдвое больше. Проверяем геометрию, а не «количество».
			total += len(level)
		}
		// Число ПАР во всей сетке = size-1 (каждый матч выводит одного).
		if want := (1 << rounds) - 1; total != want {
			t.Errorf("rounds=%d: пар в сетке %d, ожидалось %d", rounds, total, want)
		}
	}
}

// Оффлайн-источник: набирает size участников, у всех непустой thumb и есть
// score (иначе на финале нечего показать как эталон), повторов нет.
func TestTournamentOfflinePicksScoredPosts(t *testing.T) {
	db := tournamentTestDB(t, 40, true)
	posts, available := tournamentOffline(db, nil, "", 8, nil)
	if len(posts) != 8 {
		t.Fatalf("получено %d участников, ожидалось 8 (всего доступно %d)", len(posts), available)
	}
	if available != 40 {
		t.Errorf("available=%d, ожидалось 40", available)
	}
	seen := map[int]bool{}
	for i, p := range posts {
		if p.ID <= 0 {
			t.Errorf("участник %d: id=%d", i, p.ID)
		}
		if seen[p.ID] {
			t.Errorf("повтор участника %d в одной сетке", p.ID)
		}
		seen[p.ID] = true
		if p.Score <= 0 {
			t.Errorf("участник %d: score=%d, ожидался > 0", p.ID, p.Score)
		}
		if p.Thumb == "" || p.FileURL == "" {
			t.Errorf("участник %d: пустой thumb/file_url", p.ID)
		}
	}
}

// Без оценки буры в турнир участвовать нельзя: эталон на финале недостижим,
// иначе финал показал бы «эталон: 0» и игра потеряла бы смысл.
func TestTournamentOfflineExcludesZeroScore(t *testing.T) {
	db := tournamentTestDB(t, 20, false)
	posts, available := tournamentOffline(db, nil, "", 4, nil)
	if len(posts) != 0 {
		t.Errorf("получено %d участников из постов без score, ожидалось 0", len(posts))
	}
	if available != 0 {
		t.Errorf("available=%d, ожидалось 0 (под score>0 подходит никто)", available)
	}
}

// Библиотека меньше запрошенного размера — отдаём что есть, чтобы клиент мог
// сказать «турнир на 8 не помещается, доступно N».
func TestTournamentOfflineRespectsLibrarySize(t *testing.T) {
	db := tournamentTestDB(t, 6, true)
	posts, available := tournamentOffline(db, nil, "", 8, nil)
	if len(posts) != 6 || available != 6 {
		t.Errorf("получено %d (available %d), ожидалось 6 — библиотека меньше размера сетки",
			len(posts), available)
	}
}

// Скрытые теги профиля не должны попадать в сетку.
func TestTournamentOfflineRespectsHiddenTags(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	for i := 1; i <= 10; i++ {
		tags := "keep"
		if i%2 == 0 {
			tags += " spoiler"
		}
		db.UpsertMeta(&Post{ID: i, Tags: tags, Score: i, FileURL: "https://x/a.jpg"})
		db.SetDownloaded(i, "data/save/a.jpg", "thumbs/a.jpg")
	}
	posts, _ := tournamentOffline(db, []string{"spoiler"}, "", 5, nil)
	if len(posts) != 5 {
		t.Fatalf("получено %d участников, ожидалось 5", len(posts))
	}
	for _, p := range posts {
		if containsWord(p.Tags, "spoiler") {
			t.Errorf("участник %d содержит скрытый тег: %q", p.ID, p.Tags)
		}
	}
}

func containsWord(tags, word string) bool {
	for _, f := range splitTags(tags) {
		if f == word {
			return true
		}
	}
	return false
}

// Повторные турниры должны давать разные участники. Без перемешивания БД
// отдаёт «последние N постов», и второй заход в тот же день был бы тем же
// набором — игра превратилась бы в прокрутку вчерашнего.
func TestTournamentOfflineVariesBetweenRuns(t *testing.T) {
	db := tournamentTestDB(t, 60, true)
	seenSets := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		posts, _ := tournamentOffline(db, nil, "", 8, nil)
		if len(posts) != 8 {
			t.Fatalf("заход %d: участников %d, ожидалось 8", i, len(posts))
		}
		ids := make([]int, 0, len(posts))
		for _, p := range posts {
			ids = append(ids, p.ID)
		}
		sort.Ints(ids)
		key := fmt.Sprint(ids)
		for _, prev := range seenSets {
			if prev == key {
				t.Fatalf("заход %d дал тот же набор, что и один из предыдущих: %s", i, key)
			}
		}
		seenSets = append(seenSets, key)
	}
}

type stubTournamentProvider struct {
	Provider
	name      string
	postsFunc func(tags string, page, limit, minID int) ([]Rule34Post, error)
}

func (s *stubTournamentProvider) Name() string {
	if s.name != "" {
		return s.name
	}
	return "rule34"
}

func (s *stubTournamentProvider) SearchPosts(tags string, page, limit, minID int) ([]Rule34Post, error) {
	if s.postsFunc != nil {
		return s.postsFunc(tags, page, limit, minID)
	}
	return nil, nil
}

func TestTournamentOnlineSuccessAndFallback(t *testing.T) {
	var requestedPages []int
	mock := &stubTournamentProvider{
		name: "rule34",
		postsFunc: func(tags string, page, limit, minID int) ([]Rule34Post, error) {
			requestedPages = append(requestedPages, page)
			if page > 1 {
				// Симулируем случай, когда случайная глубокая страница пуста
				return []Rule34Post{}, nil
			}
			// Первая страница отдаёт достаточно постов
			res := make([]Rule34Post, limit)
			for i := 0; i < limit; i++ {
				res[i] = Rule34Post{
					ID:         1000 + i,
					Score:      10 + i,
					FileURL:    fmt.Sprintf("https://rule34.xxx/%d.jpg", 1000+i),
					PreviewURL: fmt.Sprintf("https://rule34.xxx/p%d.jpg", 1000+i),
				}
			}
			return res, nil
		},
	}

	h := &Handler{
		providers: map[string]Provider{
			"rule34": mock,
		},
	}

	posts, avail, err := h.tournamentOnline("", 16, nil, nil)
	if err != nil {
		t.Fatalf("ожидался успех, получено: %v", err)
	}
	if len(posts) != 16 {
		t.Fatalf("ожидалось 16 постов, получено %d", len(posts))
	}
	if avail != 16 {
		t.Errorf("avail=%d, want 16", avail)
	}
	// Проверяем, что если первая попытка была page > 1, сработал fallback к page=1
	if len(requestedPages) > 1 {
		if requestedPages[len(requestedPages)-1] != 1 {
			t.Errorf("последний запрос должен быть к page=1, было: %v", requestedPages)
		}
	}
}

func TestTournamentOnlineProviderError(t *testing.T) {
	mock := &stubTournamentProvider{
		name: "rule34",
		postsFunc: func(tags string, page, limit, minID int) ([]Rule34Post, error) {
			return nil, fmt.Errorf("network boom")
		},
	}
	h := &Handler{
		providers: map[string]Provider{
			"rule34": mock,
		},
	}

	_, _, err := h.tournamentOnline("", 16, nil, nil)
	if err == nil || err != ErrProviderUnavailable {
		t.Errorf("ожидалась ошибка ErrProviderUnavailable, получено: %v", err)
	}
}

// ── Отдельное API турнира: посты участников для галереи и «Открыть пост» ──

// Локальная копия важнее источника: скачанный участник отдаётся с downloaded=true
// (вьювер возьмёт /api/file/:id и в сеть не пойдёт), порядок — как запросили, а
// id без файла и без адреса уходит в missing.
func TestTournamentPostsFromLibrary(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { db.Close() })
	db.UpsertMeta(&Post{ID: 5, Tags: "a", FileURL: "https://x/5.jpg", PreviewURL: "https://x/p5.jpg", FileSize: 900})
	db.UpsertMeta(&Post{ID: 6, Tags: "b", FileURL: "https://x/6.jpg"})
	db.SetDownloaded(6, "data/save/6.jpg", "thumbs/6.jpg")
	db.UpsertMeta(&Post{ID: 7, Tags: "c"}) // ни файла, ни адреса — смотреть нечего

	posts, missing := tournamentPostsByIDs(context.Background(), db, nil,
		tournamentIDQueryPlan{}, "offline", []int{7, 6, 5})

	if !slices.Equal(missing, []int{7}) {
		t.Fatalf("missing=%v, ожидалось [7]", missing)
	}
	if len(posts) != 2 {
		t.Fatalf("постов %d, ожидалось 2", len(posts))
	}
	if posts[0]["id"] != 6 || posts[1]["id"] != 5 {
		t.Errorf("порядок постов %v/%v, ожидался 6, 5", posts[0]["id"], posts[1]["id"])
	}
	// Скачанный — из библиотеки: локальный файл, а не адрес источника.
	if posts[0]["downloaded"] != true || posts[0]["thumb"] != "/api/thumb/6" {
		t.Errorf("скачанный пост отдан неверно: %v", posts[0])
	}
	// Нетронутый — сырые адреса источника, без /api/proxy на клиенте.
	if posts[1]["downloaded"] != false || posts[1]["file_url"] != "https://x/5.jpg" {
		t.Errorf("пост из БД с адресом источника отдан неверно: %v", posts[1])
	}
}

// Пакетная выборка: один запрос id:20,21,22 на всю галерею, а ответ сайта,
// который проигнорировал список id и вернул свежую выдачу, отбрасывается —
// иначе в галерею турнира попали бы посты, которых в турнире нет.
func TestTournamentPostsOnlineBatch(t *testing.T) {
	var asked []string
	mock := &stubTournamentProvider{
		postsFunc: func(tags string, page, limit, minID int) ([]Rule34Post, error) {
			asked = append(asked, tags)
			return []Rule34Post{
				{ID: 20, Tags: "x", FileURL: "https://r/20.jpg", PreviewURL: "https://r/p20.jpg",
					SampleURL: "https://r/s20.jpg", FileSize: 5 << 20},
				{ID: 21, Tags: "y", FileURL: "https://r/21.jpg"},
				{ID: 999, Tags: "чужой", FileURL: "https://r/999.jpg"},
			}, nil
		},
	}
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { db.Close() })

	posts, missing := tournamentPostsByIDs(context.Background(), db, mock,
		tournamentIDQueryPlan{batch: true, prefix: "id:"}, "online", []int{20, 21, 22})

	if len(asked) != 1 || asked[0] != "id:20,21,22" {
		t.Errorf("запросы к источнику %v, ожидался один id:20,21,22", asked)
	}
	if len(posts) != 2 || !slices.Equal(missing, []int{22}) {
		t.Fatalf("постов %d, missing=%v (ожидалось 2 и [22])", len(posts), missing)
	}
	for _, p := range posts {
		if p["id"] == 999 {
			t.Errorf("пост не из списка id попал в галерею: %v", p)
		}
	}
	if posts[0]["sample_url"] != "https://r/s20.jpg" || posts[0]["file_size"] != 5<<20 {
		t.Errorf("сырые данные источника потеряны: %v", posts[0])
	}
	if posts[0]["downloaded"] != false || posts[0]["file_url"] != "https://r/20.jpg" {
		t.Errorf("онлайн-пост не должен выглядеть скачанным, адрес — как есть: %v", posts[0])
	}
}

// Источник без поддержки списков: по одному id:N, ненайденный id — в missing
// (вьювер такой пост не получит и не останется со спиннером).
func TestTournamentPostsOnlineSingle(t *testing.T) {
	// asked пишется из нескольких горутин tournamentFetchByIDs (id:31 и id:32
	// идут параллельно), поэтому без мьютекса это была гонка: -race падал
	// всегда, а без него append иногда терялся и тест мигал.
	var mu sync.Mutex
	var asked []string
	mock := &stubTournamentProvider{
		postsFunc: func(tags string, page, limit, minID int) ([]Rule34Post, error) {
			mu.Lock()
			asked = append(asked, tags)
			mu.Unlock()
			if tags == "id:31" {
				return []Rule34Post{{ID: 31, FileURL: "https://r/31.jpg"}}, nil
			}
			return []Rule34Post{}, nil
		},
	}
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { db.Close() })

	posts, missing := tournamentPostsByIDs(context.Background(), db, mock,
		tournamentIDQueryPlan{prefix: "id:"}, "online", []int{31, 32})

	if len(posts) != 1 || posts[0]["id"] != 31 || !slices.Equal(missing, []int{32}) {
		t.Fatalf("постов %d, missing=%v (ожидалось 1 и [32])", len(posts), missing)
	}
	mu.Lock()
	got := slices.Clone(asked)
	mu.Unlock()
	sort.Strings(got)
	if !slices.Equal(got, []string{"id:31", "id:32"}) {
		t.Errorf("запросы %v, ожидались id:31 и id:32", got)
	}
}

// Источник молчит или вовсе не умеет искать по id: галерея не ломается, все
// неразрешённые участники уходят в missing — «пост недоступен» вместо
// бесконечного спиннера в просмотрщике.
func TestTournamentPostsUnavailable(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	t.Cleanup(func() { db.Close() })

	broken := &stubTournamentProvider{
		postsFunc: func(string, int, int, int) ([]Rule34Post, error) { return nil, fmt.Errorf("network boom") },
	}
	posts, missing := tournamentPostsByIDs(context.Background(), db, broken,
		tournamentIDQueryPlan{batch: true, prefix: "id:"}, "online", []int{1, 2})
	if len(posts) != 0 || !slices.Equal(missing, []int{1, 2}) {
		t.Errorf("при сбое источника posts=%v missing=%v, ожидалось пусто и [1 2]", posts, missing)
	}

	// Оффлайн-турнир в сеть не ходит вообще: id остаются missing.
	posts, missing = tournamentPostsByIDs(context.Background(), db, broken, tournamentIDQueryPlan{}, "offline", []int{3})
	if len(posts) != 0 || !slices.Equal(missing, []int{3}) {
		t.Errorf("оффлайн: posts=%v missing=%v, ожидалось пусто и [3]", posts, missing)
	}
}

// План запроса по id выводится из источника: rule34 понимает списки id,
// safebooru их игнорирует (он отдаёт свежую выдачу — ему только по одному), а
// провайдер без dapi спросить не может вовсе.
func TestTournamentIDQueryPlan(t *testing.T) {
	if p := tournamentIDQueryPlanFor(NewRule34Client()); !p.batch || p.prefix != "id:" {
		t.Errorf("rule34: план %+v, ожидался пакетный с префиксом id:", p)
	}
	if p := tournamentIDQueryPlanFor(NewSafebooruClient()); p.batch || p.prefix != "id:" {
		t.Errorf("safebooru: план %+v, ожидался поштучный с префиксом id:", p)
	}
	if p := tournamentIDQueryPlanFor(&stubTournamentProvider{}); p.batch || p.prefix != "" {
		t.Errorf("провайдер без dapi: план %+v, ожидался пустой", p)
	}
}

// setupTournamentDB поднимает изолированную data/ и глобальную БД.
//
// Обработчик берёт БД через GetDB(), а не аргументом, поэтому HTTP-тестам нужен
// именно синглтон. Сбрасываем его ДО создания: иначе тест получил бы базу
// предыдущего теста (а её каталог давно удалён) и увидел бы пустую выборку.
func setupTournamentDB(t *testing.T) *PostDB {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)
	t.Setenv("BRIEFLY_DB_BACKUPS", "0")
	postDB = nil
	dbOnce = sync.Once{}
	db := GetDB()
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})
	gin.SetMode(gin.TestMode)
	return db
}

// tournamentTestServer поднимает роутер с одним хендлером турнира.
func tournamentTestServer(t *testing.T, path string, h gin.HandlerFunc) *httptest.Server {
	t.Helper()
	r := gin.New()
	r.GET(path, h)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// Фильтр рейтинга на уровне эндпоинта: sfw/18+ меняют состав участников, в
// ответе видно применённый фильтр, а мусорный rating — ошибка запроса, а не
// тихий возврат к «все».
func TestGetTournamentRatingHTTP(t *testing.T) {
	db := setupTournamentDB(t)
	// 8 general + 8 explicit: сетка на 4 помещается в любом режиме, иначе
	// отличать «отфильтровано» от «не хватает постов» было бы нечем.
	for i := 1; i <= 8; i++ {
		for j, rating := range []string{"general", "explicit"} {
			id := (j+1)*100 + i
			db.UpsertMeta(&Post{ID: id, Score: 10, Tags: "x", Rating: rating,
				FileURL: fmt.Sprintf("https://x/%d.jpg", id)})
			db.SetDownloaded(id, fmt.Sprintf("data/save/%d.jpg", id), fmt.Sprintf("thumbs/%d.jpg", id))
		}
	}
	ts := tournamentTestServer(t, "/api/tournament", NewHandler().GetTournament)

	type respBody struct {
		Error     string           `json:"error"`
		Rating    string           `json:"rating"`
		Available int              `json:"available"`
		Posts     []TournamentPost `json:"posts"`
	}
	get := func(q string) (respBody, int) {
		t.Helper()
		rsp, err := http.Get(ts.URL + "/api/tournament?" + q)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer rsp.Body.Close()
		var out respBody
		if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out, rsp.StatusCode
	}

	out, code := get("rounds=2&source=offline&rating=sfw")
	if code != http.StatusOK || out.Rating != "sfw" || len(out.Posts) != 4 {
		t.Fatalf("sfw: код %d, rating=%q, постов %d — ожидались 200/sfw/4", code, out.Rating, len(out.Posts))
	}
	if out.Available != 8 {
		t.Errorf("sfw: available=%d, ожидалось 8 (после фильтра general остаётся ровно столько)", out.Available)
	}
	for _, p := range out.Posts {
		if p.Rating != "general" {
			t.Errorf("sfw: в участниках пост %d с рейтингом %q", p.ID, p.Rating)
		}
	}

	out, code = get("rounds=2&source=offline&rating=nsfw")
	if code != http.StatusOK || out.Rating != "nsfw" || len(out.Posts) != 4 {
		t.Fatalf("18+: код %d, rating=%q, постов %d — ожидались 200/nsfw/4", code, out.Rating, len(out.Posts))
	}
	for _, p := range out.Posts {
		if p.Rating != "explicit" {
			t.Errorf("18+: в участниках пост %d с рейтингом %q", p.ID, p.Rating)
		}
	}

	out, code = get("rounds=2&source=offline")
	if code != http.StatusOK || out.Rating != "" || len(out.Posts) != 4 {
		t.Fatalf("без фильтра: код %d, rating=%q, постов %d — ожидались 200/\"\"/4", code, out.Rating, len(out.Posts))
	}

	if _, code = get("rounds=2&source=offline&rating=all"); code != http.StatusBadRequest {
		t.Errorf("rating=all: код %d, ожидался 400 (мусорный фильтр — ошибка, а не «все»)", code)
	}
}

// «Нет постов в библиотеке» — это три разные причины, и пользователю нужно
// назвать именно его: пустая библиотека, скачанное без оценки буры (эталона для
// финала нет) или фильтры, отсеявшие всё. Поэтому в ответе для оффлайна едут
// оба счётчика, посчитанные БЕЗ фильтров: по одному available «нет скачанного»
// не отличить от «всё скачанное отсеяли теги/рейтинг».
func TestGetTournamentNotEnoughExplainsWhy(t *testing.T) {
	db := setupTournamentDB(t)
	ts := tournamentTestServer(t, "/api/tournament", NewHandler().GetTournament)

	type respBody struct {
		Error      string           `json:"error"`
		Available  int              `json:"available"`
		Downloaded int              `json:"downloaded"`
		Scored     int              `json:"scored"`
		Posts      []TournamentPost `json:"posts"`
	}
	get := func() respBody {
		t.Helper()
		rsp, err := http.Get(ts.URL + "/api/tournament?rounds=2&source=offline")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer rsp.Body.Close()
		var out respBody
		if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	download := func(id, score int) {
		db.UpsertMeta(&Post{ID: id, Tags: "x", Score: score, FileURL: fmt.Sprintf("https://x/%d.jpg", id)})
		db.SetDownloaded(id, fmt.Sprintf("data/save/%d.jpg", id), fmt.Sprintf("thumbs/%d.jpg", id))
	}

	// 1. Библиотека пуста: турнир собрать не из чего.
	out := get()
	if out.Error != ErrTournamentNotEnough.Code {
		t.Fatalf("пустая библиотека: error=%q, ожидался %q", out.Error, ErrTournamentNotEnough.Code)
	}
	if out.Downloaded != 0 || out.Scored != 0 || out.Available != 0 {
		t.Errorf("пустая библиотека: %+v — ожидались нули во всех счётчиках", out)
	}

	// 2. Посты скачаны, но без оценки буры: участвовать не в чем (эталон финала
	// показывать нечем).
	for i := 1; i <= 6; i++ {
		download(i, 0)
	}
	out = get()
	if out.Downloaded != 6 || out.Scored != 0 || out.Available != 0 {
		t.Errorf("без оценки: %+v — ожидались downloaded=6 scored=0 available=0", out)
	}

	// 3. Оценка есть, но годных постов меньше, чем участников: available — это
	// «сколько подходит под фильтры», downloaded — «сколько лежит всего».
	for i := 7; i <= 8; i++ {
		download(i, 5)
	}
	out = get()
	if out.Downloaded != 8 || out.Scored != 2 || out.Available != 2 || len(out.Posts) != 0 {
		t.Errorf("мало постов: %+v — ожидались downloaded=8 scored=2 available=2 posts=0", out)
	}
}

// HTTP-уровень: маршрут /api/tournament/posts, порядок постов и missing.
// Порядок важен не для красоты — по нему клиент рисует галерею, и перестановка
// перемешала бы участников относительно сетки.
func TestGetTournamentPostsHTTP(t *testing.T) {
	db := setupTournamentDB(t)
	db.UpsertMeta(&Post{ID: 7, Tags: "a", FileURL: "https://x/7.jpg", PreviewURL: "https://x/p7.jpg", FileSize: 700})
	db.SetDownloaded(7, "data/save/7.jpg", "thumbs/7.jpg")
	db.UpsertMeta(&Post{ID: 8, Tags: "b", FileURL: "https://x/8.jpg", PreviewURL: "https://x/p8.jpg"})
	// id 9 в базе нет вообще — участник «исчез».

	ts := tournamentTestServer(t, "/api/tournament/posts", NewHandler().GetTournamentPosts)

	// Ответ разбираем в типизированную структуру: gin.H положил бы id в
	// float64, и сравнение 8 != 8.0 давало бы ложное падение.
	type respPost struct {
		ID         int    `json:"id"`
		FileURL    string `json:"file_url"`
		PreviewURL string `json:"preview_url"`
		SampleURL  string `json:"sample_url"`
		FileSize   int    `json:"file_size"`
		Downloaded bool   `json:"downloaded"`
		Thumb      string `json:"thumb"`
	}
	type respBody struct {
		Posts   []respPost `json:"posts"`
		Missing []int      `json:"missing"`
		Source  string     `json:"source"`
	}
	var out respBody
	code := 0
	raw := func(path string) {
		t.Helper()
		rsp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		defer rsp.Body.Close()
		code = rsp.StatusCode
		if code == http.StatusOK {
			if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
	}

	raw("/api/tournament/posts?ids=8,7,9&source=offline")
	if code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200", code)
	}
	if len(out.Posts) != 2 || out.Posts[0].ID != 8 || out.Posts[1].ID != 7 {
		t.Fatalf("порядок постов %+v, ожидался [8 7]", out.Posts)
	}
	if !slices.Equal(out.Missing, []int{9}) || out.Source != "offline" {
		t.Errorf("missing=%v source=%q, ожидалось [9] и offline", out.Missing, out.Source)
	}
	// Клиент отдаёт эти записи во вьювер как есть: сырой адрес источника,
	// никакой обёртки в /api/proxy на стороне сервера.
	if out.Posts[0].FileURL != "https://x/8.jpg" || out.Posts[0].Downloaded {
		t.Errorf("пост источника отдан неверно: %+v", out.Posts[0])
	}
	if out.Posts[0].PreviewURL != "https://x/p8.jpg" {
		t.Errorf("preview_url источника потерян: %+v", out.Posts[0])
	}
	if !out.Posts[1].Downloaded || out.Posts[1].Thumb != "/api/thumb/7" || out.Posts[1].FileSize != 700 {
		t.Errorf("скачанный пост отдан неверно: %+v", out.Posts[1])
	}

	raw("/api/tournament/posts?ids=%207%20,,%208%20&source=offline")
	if code != http.StatusOK || len(out.Posts) != 2 {
		t.Errorf("мусор в ids: код %d, постов %d (ожидался 200 и 2)", code, len(out.Posts))
	}

	for _, bad := range []string{
		"/api/tournament/posts?source=offline",         // без id
		"/api/tournament/posts?ids=abc&source=offline", // ни одного годного id
		"/api/tournament/posts?ids=7&source=bogus",     // неизвестный источник
		"/api/tournament/posts?ids=" + longIDList(tournamentMaxParticipants+1) + "&source=offline",
	} {
		raw(bad)
		if code != http.StatusBadRequest {
			t.Errorf("%s: код %d, ожидался 400", bad, code)
		}
	}
}

// longIDList склеивает count разных id через запятую.
func longIDList(count int) string {
	parts := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		parts = append(parts, strconv.Itoa(i))
	}
	return strings.Join(parts, ",")
}

// Фильтр рейтинга в оффлайн-турнире: sfw выкидывает explicit/questionable/
// sensitive, «18+» — наоборот. Проверяем и available: сообщение «в библиотеке
// только N подходящих» обязано считать посты, оставшиеся ПОСЛЕ фильтра, иначе
// игрок увидел бы «доступно 12» при шести годных постах.
func TestTournamentOfflineRating(t *testing.T) {
	build := func(t *testing.T) *PostDB {
		db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
		t.Cleanup(func() { db.Close() })
		// Регистр вперемешку: сайты пишут рейтинг как попало, и LOWER() в SQL
		// обязан это переварить, иначе «18+»-турнир протащил бы general.
		for i, rating := range []string{"general", "general", "safe", "sensitive", "questionable", "explicit", "Explicit"} {
			id := i + 1
			db.UpsertMeta(&Post{ID: id, Score: 10, Tags: "x", Rating: rating,
				FileURL: fmt.Sprintf("https://x/%d.jpg", id)})
			db.SetDownloaded(id, fmt.Sprintf("data/save/%d.jpg", id), fmt.Sprintf("thumbs/%d.jpg", id))
		}
		return db
	}
	ratings := func(posts []TournamentPost) []string {
		out := make([]string, 0, len(posts))
		for _, p := range posts {
			out = append(out, p.Rating)
		}
		sort.Strings(out)
		return out
	}

	_, sfwExcl := ratingFilter("sfw")
	posts, available := tournamentOffline(build(t), nil, "", 8, sfwExcl)
	if available != 3 || !slices.Equal(ratings(posts), []string{"general", "general", "safe"}) {
		t.Errorf("sfw: посты %v, available=%d — ожидались general/general/safe и 3", ratings(posts), available)
	}

	_, nsfwExcl := ratingFilter("nsfw")
	posts, available = tournamentOffline(build(t), nil, "", 8, nsfwExcl)
	if available != 4 || !slices.Equal(ratings(posts), []string{"Explicit", "explicit", "questionable", "sensitive"}) {
		t.Errorf("18+: посты %v, available=%d — ожидались 4 поста без general", ratings(posts), available)
	}

	// «Все»: фильтра нет, в наборку попадает вся библиотека.
	posts, available = tournamentOffline(build(t), nil, "", 8, nil)
	if available != 7 || len(posts) != 7 {
		t.Errorf("все: получено %d (available %d), ожидалось 7", len(posts), available)
	}
}

// Онлайн-турнир с фильтром: метатеги уходят в запрос (сайт фильтрует сам),
// а ответ досчищается локально — сайты обрезают хвост запроса по MaxQueryLen и
// могли бы прислать чужой рейтинг.
func TestTournamentOnlineRating(t *testing.T) {
	var asked []string
	mock := &stubTournamentProvider{
		postsFunc: func(tags string, page, limit, minID int) ([]Rule34Post, error) {
			asked = append(asked, tags)
			return []Rule34Post{
				{ID: 900001, Rating: "general", PreviewURL: "https://r/1.jpg"},
				{ID: 900002, Rating: "Explicit", PreviewURL: "https://r/2.jpg"},
				{ID: 900003, Rating: "questionable", PreviewURL: "https://r/3.jpg"},
			}, nil
		},
	}
	h := &Handler{providers: map[string]Provider{"rule34": mock}}

	terms, excl := ratingFilter("sfw")
	posts, _, err := h.tournamentOnline("cat", 4, terms, excl)
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "-rating:explicit") || !strings.Contains(asked[0], "cat") {
		t.Errorf("запрос %q — ожидались метатеги рейтинга вместе с тегом cat", asked)
	}
	if len(posts) != 1 || posts[0].ID != 900001 {
		var ids []int
		for _, p := range posts {
			ids = append(ids, p.ID)
		}
		t.Errorf("в sfw-турнир попали посты %v — ожидался только 900001 (general)", ids)
	}
}

// Разбор фильтра рейтинга. Пустое значение — «все»; мусор (в том числе
// «18+» буквами) — ошибка запроса, а не тихий возврат к «все»: иначе игрок
// выбрал бы SFW, а получил бы explicit-посты.
func TestParseTournamentRating(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true}, {"sfw", "sfw", true}, {"nsfw", "nsfw", true},
		{"all", "", false}, {"18+", "", false}, {"SFW", "", false}, {" safe", "", false},
	}
	for _, tc := range cases {
		got, ok := parseTournamentRating(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("rating=%q: получено (%q, %v), ожидалось (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Разбор списка id. Мусор отбрасывается молча, но запрос без единого
// пригодного id или длиннее максимальной сетки — ошибка, а не «пустая галерея»:
// иначе финал молча показал бы не тех участников.
func TestParseTournamentIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []int
		ok   bool
	}{
		{"", nil, false},
		{",", nil, false},
		{"abc", nil, false},
		{"0", nil, false},
		{"-5", nil, false},
		{"7", []int{7}, true},
		{"1,2,3", []int{1, 2, 3}, true},
		{" 3 , 3 ,4 ", []int{3, 4}, true},
		{"1,,2,", []int{1, 2}, true},
	}
	for _, tc := range cases {
		got, ok := parseTournamentIDs(tc.in)
		if ok != tc.ok || !slices.Equal(got, tc.want) {
			t.Errorf("ids=%q: получено (%v, %v), ожидалось (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	// Ровно размер максимальной сетки проходит, на один больше — ошибка.
	ids := make([]string, 0, tournamentMaxParticipants+1)
	for i := 1; i <= tournamentMaxParticipants+1; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	if _, ok := parseTournamentIDs(strings.Join(ids, ",")); ok {
		t.Errorf("список из %d id должен быть отвергнут (потолок %d)",
			tournamentMaxParticipants+1, tournamentMaxParticipants)
	}
}
