package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// viewedFilterTestHandler поднимает Handler на источнике-моке: отдаёт count
// постов и запоминает pid/limit, реально ушедшие в апстрим. Страницы
// нумеруются с нуля (pid), как у danbooru-стиля.
func viewedFilterTestHandler(t *testing.T, count int) (*Handler, *atomic.Value) {
	t.Helper()
	var lastReq atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pid, _ := strconv.Atoi(q.Get("pid"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		lastReq.Store(struct{ pid, limit int }{pid, limit})
		out := make([]string, 0, limit)
		for i := pid * limit; i < (pid+1)*limit && i < count; i++ {
			out = append(out, `{"id":`+strconv.Itoa(i+1)+`,"tags":"a","file_url":"a.jpg","preview_url":"t.jpg"}`)
		}
		w.Write([]byte("[" + strings.Join(out, ",") + "]"))
	}))
	t.Cleanup(srv.Close)

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "k"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "viewed_sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}
	return &Handler{providers: map[string]Provider{"rule34": cl}}, &lastReq
}

// viewedDB поднимает изолированную БД и отмечает просмотренные посты.
func viewedDB(t *testing.T, viewed ...int) {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})
	GetDB().RecordViews(viewed)
}

// searchPostsPage — вызов SearchPosts с листом и фильтром просмотров.
func searchPostsPage(t *testing.T, h *Handler, viewed string, page int) (map[string]any, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/api/posts?tags=cat_girl&limit=10&page=" + strconv.Itoa(page)
	if viewed != "" {
		url += "&viewed=" + viewed
	}
	c.Request = httptest.NewRequest("GET", url, nil)
	h.SearchPosts(c)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad response: %v (%s)", err, w.Body.String())
	}
	return out, w.Code
}

func postIDs(resp map[string]any) []int {
	raw, _ := resp["posts"].([]any)
	out := make([]int, 0, len(raw))
	for _, p := range raw {
		m, _ := p.(map[string]any)
		id, _ := m["id"].(float64)
		out = append(out, int(id))
	}
	return out
}

// Регрессия: в онлайн-ленте фильтр «Новое/Виденное» должен работать так же,
// как в локальной. Раньше viewed обрабатывался только в /local, поэтому клики
// по «Новое/Виденное» в ленте источника (в т.ч. gelbooru) не меняли ничего.
func TestSearchPostsViewedFilter(t *testing.T) {
	viewedDB(t, 3, 7)
	h, _ := viewedFilterTestHandler(t, 400)

	resp, code := searchPostsPage(t, h, "1", 1)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, resp)
	}
	seen := postIDs(resp)
	if len(seen) != 2 || seen[0] != 3 || seen[1] != 7 {
		t.Fatalf("viewed=1 must return only viewed posts, got %v", seen)
	}

	resp, _ = searchPostsPage(t, h, "0", 1)
	fresh := postIDs(resp)
	if len(fresh) == 0 {
		t.Fatal("viewed=0 must return unviewed posts")
	}
	for _, id := range fresh {
		if id == 3 || id == 7 {
			t.Fatalf("viewed=0 leaked viewed post %d: %v", id, fresh)
		}
	}
	if len(fresh) > 10 {
		t.Errorf("лист должен быть обрезан обратно до limit, got %d", len(fresh))
	}
	// Окна соседних листов не пересекаются: иначе пагинация дублирует посты.
	page2, _ := searchPostsPage(t, h, "0", 2)
	second := postIDs(page2)
	if len(second) == 0 {
		t.Fatal("второй лист не должен быть пустым")
	}
	seen2 := make(map[int]bool, len(second))
	for _, id := range second {
		seen2[id] = true
	}
	for _, id := range fresh {
		if seen2[id] {
			t.Fatalf("лист 2 повторяет пост %d с листа 1: %v", id, second)
		}
	}
}

// Пустое окно при активном фильтре ≠ конец выдачи: сервер сообщает флагом
// more, что за окном есть ещё посты (клиент догрузит следующий лист).
// Окно листа 1 — 30 постов (limit 10 × viewedWindow 3), помечаем их все.
func TestSearchPostsViewedMoreFlag(t *testing.T) {
	ids := make([]int, 0, 30)
	for i := 1; i <= 30; i++ {
		ids = append(ids, i)
	}
	viewedDB(t, ids...)
	h, _ := viewedFilterTestHandler(t, 400)

	resp, _ := searchPostsPage(t, h, "0", 1)
	if got := postIDs(resp); len(got) != 0 {
		t.Fatalf("весь лист просмотрен, ожидался пустой ответ, got %v", got)
	}
	if more, _ := resp["more"].(bool); !more {
		t.Error("more=true: окно апстрима полное, совпадения могут быть дальше")
	}
}

// Когда совпадений нет, но выдача кончилась (окно неполное), флага more быть
// не должно — иначе клиент догружал бы пустые листы вечно.
func TestSearchPostsViewedNoMoreAtEnd(t *testing.T) {
	viewedDB(t)
	h, _ := viewedFilterTestHandler(t, 12)

	resp, _ := searchPostsPage(t, h, "0", 1)
	if len(postIDs(resp)) == 0 {
		t.Fatal("ничего не просмотрено — должны прийти посты")
	}
	if more, _ := resp["more"].(bool); more {
		t.Error("more must be false: апстрим отдал неполное окно (конец выдачи)")
	}
}

// Без фильтра поведение прежнее: запрос к апстриму 1:1 и в ответе нет more —
// иначе изменились бы ETag и кэш дефолтной выдачи.
func TestSearchPostsWithoutViewedUnchanged(t *testing.T) {
	viewedDB(t, 1)
	h, req := viewedFilterTestHandler(t, 40)

	resp, _ := searchPostsPage(t, h, "", 1)
	if _, ok := resp["more"]; ok {
		t.Error("more must be absent when viewed filter is off")
	}
	if got := len(postIDs(resp)); got != 10 {
		t.Errorf("want full page of 10, got %d", got)
	}
	got, _ := req.Load().(struct{ pid, limit int })
	if got.limit != 10 || got.pid != 0 {
		t.Errorf("upstream request must stay 1:1 without filter, got pid=%d limit=%d", got.pid, got.limit)
	}
}

// Контракт, на который теперь опирается фронтенд: батч POST /api/views
// (RecordViews) и снятие отметки POST /api/view/:id/forget (ForgetView)
// должны менять то, что показывает фильтр «Новое/Виденное». Отдельные
// одиночные отметки из UI больше не летят — весь поток идёт пачкой.
func TestViewMarksDriveLocalFilter(t *testing.T) {
	viewedDB(t)
	db := GetDB()
	for _, id := range []int{1, 2, 3, 4} {
		db.AddOrUpdate(&Post{ID: id, Tags: "a", Downloaded: true})
	}
	h := &Handler{providers: map[string]Provider{}}

	do := func(method, url, body string, params gin.Params) map[string]any {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, url, strings.NewReader(body))
		if body != "" {
			c.Request.Header.Set("Content-Type", "application/json")
		}
		if params != nil {
			c.Params = params
		}
		switch {
		case method == "POST" && strings.HasSuffix(url, "/forget"):
			h.ForgetView(c)
		case method == "POST":
			h.RecordViews(c)
		default:
			h.GetLocalPosts(c)
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad response: %v (%s)", err, w.Body.String())
		}
		return out
	}

	local := func(viewed string) []int {
		url := "/api/local?page=1&limit=10"
		if viewed != "" {
			url += "&viewed=" + viewed
		}
		return postIDs(do("GET", url, "", nil))
	}

	if got := local("0"); len(got) != 4 {
		t.Fatalf("без отметок «Новое» должно отдавать всю библиотеку, got %v", got)
	}

	out := do("POST", "/api/views", `{"ids":[1,2]}`, nil)
	if out["recorded"] != float64(2) {
		t.Fatalf("recorded = %v, ожидалось 2", out["recorded"])
	}
	// GetDownloaded сортирует по id DESC.
	if got := local("1"); len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("после батча «Виденное» = %v, ожидалось [2 1]", got)
	}
	if got := local("0"); len(got) != 2 || got[0] != 4 || got[1] != 3 {
		t.Fatalf("«Новое» после батча = %v, ожидалось [4 3]", got)
	}

	// Снятие отметки: пост снова «новый».
	do("POST", "/api/view/2/forget", "", gin.Params{{Key: "id", Value: "2"}})
	if got := local("1"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("после forget «Виденное» = %v, ожидалось [1]", got)
	}
	if got := local("0"); len(got) != 3 || got[0] != 4 || got[2] != 2 {
		t.Fatalf("после forget «Новое» = %v, ожидалось [4 3 2]", got)
	}

	// Пустой батч и мусорный id не должны ронять эндпоинт.
	if out := do("POST", "/api/views", `{"ids":[]}`, nil); out["error"] == nil {
		t.Errorf("пустой ids должен дать 400, got %v", out)
	}
}

func TestViewedIDsBatch(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "viewed_ids.db"))
	t.Cleanup(func() { db.Close() })

	db.RecordViews([]int{2, 5, 9})
	got := db.ViewedIDs([]int{1, 2, 3, 5, 9, 11})
	if len(got) != 3 || !got[2] || !got[5] || !got[9] {
		t.Fatalf("ViewedIDs = %v, want {2,5,9}", got)
	}
	if got[1] || got[3] {
		t.Errorf("непросмотренные id не должны попадать в выборку: %v", got)
	}
	if len(db.ViewedIDs(nil)) != 0 {
		t.Error("пустой вход — пустой результат")
	}
	many := make([]int, 0, 1200)
	for i := 1; i <= 1200; i++ {
		many = append(many, i)
	}
	db.RecordViews([]int{7, 600, 1200})
	big := db.ViewedIDs(many)
	for _, id := range []int{7, 600, 1200} {
		if !big[id] {
			t.Errorf("id %d потерян на границе чанка", id)
		}
	}
}
