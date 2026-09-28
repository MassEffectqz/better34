package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newIDBudgetProvider — gelbooru-клиент на поддельном dapi, отвечающий на
// одиночный запрос tags=id:N именно этим постом. Счётчик нужен тестам, чтобы
// видеть, сколько раз сервер дёрнул источник на один HTTP-ответ клиента.
func newIDBudgetProvider(t *testing.T, calls *atomic.Int64) *booruClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw := strings.TrimSpace(r.URL.Query().Get("id"))
		if raw == "" {
			raw = strings.TrimSpace(strings.TrimPrefix(r.URL.Query().Get("tags"), "id:"))
		}
		if raw == "" {
			w.Write([]byte("[]"))
			return
		}
		w.Write([]byte(`[{"id":` + strconv.Itoa(atoiOr(raw, 0)) +
			`,"file_url":"","preview_url":"","tags":"a b","width":10,"height":10}]`))
	}))
	t.Cleanup(srv.Close)

	cl := NewGelbooruClient()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "fake-key", UserID: "1"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	// provider() смотрит активного провайдера в конфиге: без этого тест ушёл
	// бы в nil и объявил все id unresolved.
	cfg := GetConfig()
	prev := cfg.GetProvider()
	cfg.SetProvider("gelbooru")
	t.Cleanup(func() { cfg.SetProvider(prev) })
	return cl
}

type byIDsResponse struct {
	Posts []struct {
		ID int `json:"id"`
	} `json:"posts"`
	Unresolved []int `json:"unresolved"`
	Deferred   []int `json:"deferred"`
}

func callByIDs(t *testing.T, h *Handler, req *http.Request) byIDsResponse {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	h.GetPostsByIDs(c)
	var resp byIDsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

// idListURL — /posts-by-ids?ids=… для последовательного диапазона id.
func idListURL(from, n int) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, strconv.Itoa(from+i))
	}
	return "/posts-by-ids?ids=" + strings.Join(parts, ",")
}

// G5: CleanDuplicates игнорирует заголовок подтверждения X-Confirm-Dupes
// (handlers_extra.go: `_ = c.GetHeader(...)`) — POST /api/dups/clean удаляет
// файлы даже без подтверждения.
func TestAdversarialCleanDuplicatesWithoutConfirm(t *testing.T) {
	root := t.TempDir()
	cfg := GetConfig()
	oldSave := cfg.GetSavePath()
	cfg.SetSavePath(root)
	defer cfg.SetSavePath(oldSave)

	os.MkdirAll(filepath.Join(root, "a"), 0o755)
	os.MkdirAll(filepath.Join(root, "b"), 0o755)
	content := []byte("same-content-bytes")
	os.WriteFile(filepath.Join(root, "a", "file.bin"), content, 0o644)
	os.WriteFile(filepath.Join(root, "b", "file.bin"), content, 0o644)

	h := &Handler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/dups/clean", nil)
	// без заголовка X-Confirm-Dupes
	h.CleanDuplicates(c)

	if _, err := os.Stat(filepath.Join(root, "a", "file.bin")); err != nil {
		t.Errorf("BUG G5: дубликат удалён БЕЗ подтверждения X-Confirm-Dupes (a/file.bin: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b", "file.bin")); err != nil {
		t.Errorf("BUG G5: дубликат удалён БЕЗ подтверждения X-Confirm-Dupes (b/file.bin: %v)", err)
	}
}

// G8: «пост недоступен» и «источник не ответил» — разные вещи, а клиент
// вкладок «Лайки»/«Скрытые» трактует отсутствие в ответе как удалённый пост.
// Сбой источника обязан попадать в отдельный unresolved, иначе живой пост
// навечно помечается недоступным (одна сетевая ошибка — и статус закреплён).
func TestAdversarialPostsByIDsReportsUnresolved(t *testing.T) {
	// id из диапазона, не занятого другими тестами пакета: БД общая на весь
	// прогон, и запись от соседнего теста (UpsertMetaMany) сделала бы эти id
	// «найденными в локальной БД» — до источника дело не дойдёт, и проверка
	// молча потеряла бы смысл.
	const idA, idB = 7001201, 7001202
	h := &Handler{providers: map[string]Provider{"rule34": &stubTagProvider{name: "rule34"}}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", fmt.Sprintf("/posts-by-ids?ids=%d,%d", idA, idB), nil)
	h.GetPostsByIDs(c)

	var resp struct {
		Posts []struct {
			ID int `json:"id"`
		} `json:"posts"`
		Unresolved []int `json:"unresolved"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Posts) != 0 {
		t.Errorf("постов нет, но ответ их содержит: %+v", resp.Posts)
	}
	got := append([]int(nil), resp.Unresolved...)
	sort.Ints(got)
	if len(got) != 2 || got[0] != idA || got[1] != idB {
		t.Errorf("unresolved=%v, want [%d %d]", got, idA, idB)
	}
}

// G8b: то же, но источник ОТВЕЧАЕТ успешно и при этом игнорирует список id —
// возвращает посты, которых не просили (поведение safebooru и часть CDN-ов).
// Раньше такой ответ проходил через `if !want[p.ID] { continue }`, и запрошенные
// id исчезали молча: их не было ни в posts, ни в unresolved. Клиент вкладок
// «Лайки»/«Скрытые» трактует отсутствие как удалённый пост и навечно рисует
// «#id · недоступен» — без кнопки повтора, хотя пост жив и его можно переспросить.
func TestAdversarialPostsByIDsIgnoredIDListIsUnresolved(t *testing.T) {
	// Уникальный диапазон id: БД общая на весь прогон пакета, и если такой id
	// уже лежит в ней, до источника дело не дойдёт — тест молча потеряет смысл.
	const idA, idB = 7001301, 7001302
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Просили idA,idB — отдали посторонний пост.
		w.Write([]byte(`[{"id":9001,"file_url":"https://x/1.jpg","preview_url":"https://x/1p.jpg","tags":"a"}]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "adversarial-fake-key"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	h := &Handler{providers: map[string]Provider{"rule34": cl}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", fmt.Sprintf("/posts-by-ids?ids=%d,%d", idA, idB), nil)
	h.GetPostsByIDs(c)

	var resp struct {
		Posts []struct {
			ID int `json:"id"`
		} `json:"posts"`
		Unresolved []int `json:"unresolved"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Чужие посты в ответ попадать не должны — это не те id, что просили.
	for _, p := range resp.Posts {
		if p.ID != idA && p.ID != idB {
			t.Errorf("в ответе чужой пост id=%d", p.ID)
		}
	}
	got := append([]int(nil), resp.Unresolved...)
	sort.Ints(got)
	if len(got) != 2 || got[0] != idA || got[1] != idB {
		t.Errorf("BUG G8b: источник проигнорировал список id, но id пропали молча: unresolved=%v, want [%d %d]", got, idA, idB)
	}
}

// G7: GetPostsByIDs делает по одному HTTP-запросу на каждый id (N+1).
// 60 id → 60 запросов к API вместо одного пакетного.
func TestAdversarialGetPostsByIDsFanOut(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "adversarial-fake-key"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	var ids []string
	for i := 0; i < 60; i++ {
		ids = append(ids, fmt.Sprintf("%d", 8100000+i))
	}
	h := &Handler{providers: map[string]Provider{"rule34": cl}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/posts-by-ids?ids="+strings.Join(ids, ","), nil)
	h.GetPostsByIDs(c)

	if got := calls.Load(); got > 10 {
		t.Errorf("BUG G7: %d HTTP-запросов к API на %d id (N+1; ожидалось пакетно ≤10)", got, len(ids))
	}
}

// G41: GetTagCounts без лимита на число тегов — 40 тегов → 40 запросов.
func TestAdversarialGetTagCountsFanOut(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "adversarial-fake-key"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.suggMu.Lock()
	cl.suggM = make(map[string]suggestionCacheEntry)
	cl.suggMu.Unlock()
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	var tags []string
	for i := 0; i < 40; i++ {
		tags = append(tags, fmt.Sprintf("ab%d", i))
	}
	h := &Handler{providers: map[string]Provider{"rule34": cl}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/tag-counts?tags="+strings.Join(tags, ","), nil)
	h.GetTagCounts(c)

	if got := calls.Load(); got > 20 {
		t.Errorf("BUG G41: %d HTTP-запросов на %d тегов (ожидалось ≤20: suggest + dapi exact-lookup на тег)", got, len(tags))
	}
}

// G10: publishSSE шлёт в канал подписчика в неблокирующем режиме;
// при медленном подписчике сообщения молча теряются (буфер 32).
func TestAdversarialSSEDropsSlowSubscriber(t *testing.T) {
	ch, unsubscribe := subscribeSSE()
	defer unsubscribe()

	const published = 40
	for i := 0; i < published; i++ {
		publishSSE(map[string]any{"n": i})
	}

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got == published {
				return // все сообщения доставлены — бага нет
			}
			t.Errorf("BUG G10: подписчик получил %d из %d событий (буфер 32 — остальные молча потеряны)", got, published)
			return
		}
	}
}

// G8: downloader не ограничивает размер скачиваемого файла (в отличие от
// ProxyRemote с лимитом 4MB) — 6MB файл тихо сохраняется на диск.
func TestAdversarialDownloaderNoSizeLimit(t *testing.T) {
	payload := make([]byte, 6<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := GetConfig()
	oldPath := cfg.GetDownloadPath()
	cfg.SetDownloadPath(dir)
	defer cfg.SetDownloadPath(oldPath)

	d := NewDownloader(1, nil)
	d.setQueueFile(filepath.Join(dir, "q.json"))
	defer d.Close()

	done := make(chan struct{})
	go func() {
		for {
			_, a, don := d.Status()
			if a == 0 && don > 0 {
				close(done)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	d.Submit(DownloadJob{PostID: 9940001, FileURL: srv.URL + "/big.bin", FileType: "bin"})
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("download did not finish")
	}

	filePath := filepath.Join(dir, "9940001", "original.bin")
	st, err := os.Stat(filePath)
	if err == nil && st.Size() > 4<<20 {
		t.Errorf("BUG G8: downloader сохранил %d байт (лимита размера нет — ProxyRemote режет на 4MB)", st.Size())
	}
}

// G17: Downloader.Close() не идемпотентен — повторный вызов паникует
// на close(d.stopCh).
func TestAdversarialDownloaderDoubleClose(t *testing.T) {
	d := NewDownloader(1, nil)
	d.setQueueFile(filepath.Join(t.TempDir(), "q.json"))
	d.Close()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG G17: повторный Close() паникует: %v", r)
		}
	}()
	d.Close()
}

// РЕГРЕССИЯ (2026-09): клиентский дедлайн на телефоне — 20с, троттлинг сайта —
// единицы запросов в секунду. Сайты, не понимающие списки id (gelbooru,
// safebooru), отвечают на КАЖДЫЙ id отдельным запросом, поэтому «спросить у
// источника все id разом» — это сотни запросов на один ответ: клиент
// отваливался по таймауту, сервер продолжал жечь лимит источника и забирал
// токены у обычного поиска ленты (тот тоже отдавал «превышено время
// ожидания» — симптом был один и тот же).
//
// Ожидание: за один HTTP-ответ у источника спрашивается не больше
// maxIDsPerFetch id, остальные возвращаются в deferred. deferred — НЕ
// unresolved: про них источник ничего не сказал, клиент придёт следом.
// Смешали бы — живые посты вкладки «Лайки» навечно стали бы «удалёнными».
func TestPostsByIDsBudgetsSourceFetchAndDefersRest(t *testing.T) {
	var calls atomic.Int64
	cl := newIDBudgetProvider(t, &calls)
	h := &Handler{providers: map[string]Provider{"gelbooru": cl}}

	// Диапазон id уникален для пакета: БД общая на весь прогон, и запись
	// соседнего теста сделала бы эти id «найденными в локальной БД».
	const from = 7010000
	const n = maxIDsPerFetch + 25
	resp := callByIDs(t, h, httptest.NewRequest("GET", idListURL(from, n), nil))

	if got := int(calls.Load()); got > maxIDsPerFetch {
		t.Errorf("источник опрошен %d раз за один ответ, бюджет %d — клиент не дождётся", got, maxIDsPerFetch)
	}
	if len(resp.Deferred) != n-maxIDsPerFetch {
		t.Errorf("deferred=%d, ждали %d", len(resp.Deferred), n-maxIDsPerFetch)
	}
	deferred := map[int]bool{}
	for _, id := range resp.Deferred {
		deferred[id] = true
	}
	for _, id := range resp.Unresolved {
		if deferred[id] {
			t.Errorf("id %d одновременно в unresolved и deferred — клиент не сможет его переспросить", id)
		}
	}
	// Спрошенные id обязаны вернуться постами: бюджет режет только остаток.
	got := map[int]bool{}
	for _, p := range resp.Posts {
		got[p.ID] = true
	}
	for i := 0; i < n; i++ {
		id := from + i
		if deferred[id] {
			continue
		}
		if !got[id] {
			t.Errorf("пост %d спросили, но не отдали (deferred=%v)", id, resp.Deferred)
		}
	}
}

// Бюджет должен резать ХВОСТ списка, а не случайное подмножество: клиент идёт
// по id от начала и дорисовывает по мере поступления. Если бы сервер отдавал
// разбросанный набор, начало списка (свежие лайки) отсутствовало бы в гриде,
// хотя id формально «запрошены».
func TestPostsByIDsDefersTailNotRandomSubset(t *testing.T) {
	var calls atomic.Int64
	cl := newIDBudgetProvider(t, &calls)
	h := &Handler{providers: map[string]Provider{"gelbooru": cl}}

	const from = 7020000
	const n = maxIDsPerFetch + 10
	resp := callByIDs(t, h, httptest.NewRequest("GET", idListURL(from, n), nil))

	if len(resp.Deferred) == 0 {
		t.Fatal("deferred пуст — бюджет не сработал, тест бессмысленен")
	}
	if last := from + n - 1; resp.Deferred[len(resp.Deferred)-1] != last {
		t.Errorf("deferred должен кончаться хвостом списка (%d): %v", last, resp.Deferred)
	}
	got := map[int]bool{}
	for _, p := range resp.Posts {
		got[p.ID] = true
	}
	if !got[from] {
		t.Errorf("первый id %d должен был вернуться — клиент рисует список с начала", from)
	}
	if got[from+n-1] {
		t.Errorf("хвост списка (%d) не должен попадать в посты — он в deferred", from+n-1)
	}
}

// Клиент отключился (закрыл вкладку / ушёл по своему таймауту). Сервер не
// должен продолжать опрашивать источник: он жёг бы лимит сайта впустую и
// отбирал его у живых клиентов. Непрошенные id помечаются deferred, а не
// unresolved — источник не сказал, что поста нет, его просто не спросили.
func TestPostsByIDsStopsOnClientCancel(t *testing.T) {
	var calls atomic.Int64
	cl := newIDBudgetProvider(t, &calls)
	h := &Handler{providers: map[string]Provider{"gelbooru": cl}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // клиент ушёл до начала выборки

	const n = 10
	req := httptest.NewRequest("GET", idListURL(7030000, n), nil).WithContext(ctx)
	resp := callByIDs(t, h, req)

	if got := int(calls.Load()); got > maxIDsPerFetch {
		t.Errorf("после отмены клиента источник опрошен %d раз — работа не остановлена", got)
	}
	if len(resp.Deferred)+len(resp.Unresolved) != n {
		t.Errorf("часть id потеряна: deferred=%v unresolved=%v", resp.Deferred, resp.Unresolved)
	}
	deferred := map[int]bool{}
	for _, id := range resp.Deferred {
		deferred[id] = true
	}
	for _, id := range resp.Unresolved {
		if deferred[id] {
			t.Errorf("id %d попал и в deferred, и в unresolved", id)
		}
	}
}

// Отмена по контексту — не поломка источника. Иначе ушедший клиент открывал
// брейкер, и на следующие полминуты поиск ленты получал «API временно
// недоступен» в дополнение к таймауту.
func TestSearchPostsCtxCancelDoesNotOpenBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "k"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := cl.SearchPostsCtx(ctx, "cancel_probe_tag", 1, 10, 0); err == nil {
		t.Fatal("ожидалась ошибка отменённого запроса")
	}
	cl.breaker.mu.Lock()
	allow := time.Now().After(cl.breaker.openUntil)
	fails := cl.breaker.failures
	cl.breaker.mu.Unlock()
	if !allow || fails != 0 {
		t.Errorf("отмена по контексту не должна штрафовать источник: allow=%v failures=%d", allow, fails)
	}
}

// isIDLookup: только чистый «id:N» (включая пакетную форму) относится к
// выборке по id. Обычный поиск, где «id:» — часть тега, должен идти по
// общему ведру, иначе выборка по id тихо ускорила бы весь трафик к сайту.
func TestIsIDLookup(t *testing.T) {
	for _, tc := range []struct {
		tags string
		want bool
	}{
		{"id:123", true},
		{"id:1,2,3", true},
		{" id:42 ", true},
		{"", false},
		{"id:", false},
		{"cat", false},
		{"cat id:5", false},
		{"id:something", false},
		{"sorted:id:asc", false},
	} {
		if got := isIDLookup(tc.tags); got != tc.want {
			t.Errorf("isIDLookup(%q) = %v, want %v", tc.tags, got, tc.want)
		}
	}
}
