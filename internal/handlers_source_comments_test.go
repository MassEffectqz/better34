package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubCommentsProvider — провайдер с заранее заданным ответом SourceComments
// и счётчиком вызовов: проверяем, что кэш не даёт ходить в источник повторно.
type stubCommentsProvider struct {
	Provider
	name  string
	reply []*SourceComment
	err   error
	calls int
}

func (s *stubCommentsProvider) SourceComments(postID int) ([]SourceComment, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	out := make([]SourceComment, 0, len(s.reply))
	for _, c := range s.reply {
		cc := *c
		cc.PostID = postID
		out = append(out, cc)
	}
	return out, nil
}

func setupSourceCommentsRouter(t *testing.T, prov *stubCommentsProvider) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})
	gin.SetMode(gin.TestMode)
	h := NewHandler()
	h.providers[prov.name] = prov
	r := gin.New()
	api := r.Group("/api")
	api.GET("/posts/:id/source-comments", h.GetSourceComments)
	api.GET("/posts/:id/source-comments/meta", h.GetSourceCommentsMeta)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

type srcCommentsResp struct {
	Site        string          `json:"site"`
	Count       int             `json:"count"`
	Cached      bool            `json:"cached"`
	Unsupported bool            `json:"unsupported"`
	Comments    []SourceComment `json:"comments"`
}

func fetchSourceComments(t *testing.T, ts *httptest.Server, path string) (srcCommentsResp, int) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out srcCommentsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out, resp.StatusCode
}

func TestGetSourceCommentsCachesAfterFirstFetch(t *testing.T) {
	prov := &stubCommentsProvider{name: "hypnohub", reply: []*SourceComment{
		{ID: 1, Author: "a", Body: "first"},
		{ID: 2, Author: "b", Body: "second"},
	}}
	ts := setupSourceCommentsRouter(t, prov)
	GetDB().UpsertMeta(&Post{ID: 100, FileURL: "https://hypnohub.net/img/x/100.jpg"})

	// Первый запрос: кэша нет — идём в источник.
	first, code := fetchSourceComments(t, ts, "/api/posts/100/source-comments?site=hypnohub")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if first.Unsupported || first.Count != 2 || len(first.Comments) != 2 {
		t.Fatalf("first: %+v", first)
	}
	if first.Cached {
		t.Error("first response must not be marked cached")
	}
	if prov.calls != 1 {
		t.Fatalf("source calls=%d, want 1", prov.calls)
	}

	// Второй запрос — из кэша, источник не трогаем.
	second, _ := fetchSourceComments(t, ts, "/api/posts/100/source-comments?site=hypnohub")
	if !second.Cached || second.Count != 2 {
		t.Fatalf("second: %+v", second)
	}
	if prov.calls != 1 {
		t.Fatalf("cache did not prevent refetch: calls=%d", prov.calls)
	}

	// refresh=1 — явный перезапрос.
	third, _ := fetchSourceComments(t, ts, "/api/posts/100/source-comments?site=hypnohub&refresh=1")
	if third.Cached {
		t.Error("refresh must hit the source")
	}
	if prov.calls != 2 {
		t.Fatalf("refresh calls=%d, want 2", prov.calls)
	}

	// Счётчик комментариев попал в posts — оттуда его берёт лента.
	p := GetDB().Get(100)
	if p == nil || p.CommentCount != 2 || !p.HasComments {
		t.Fatalf("post counters: %+v", p)
	}
}

func TestGetSourceCommentsUnsupportedIsNotAnError(t *testing.T) {
	prov := &stubCommentsProvider{name: "gelbooru", err: ErrSourceCommentsUnsupported}
	ts := setupSourceCommentsRouter(t, prov)
	GetDB().UpsertMeta(&Post{ID: 7, FileURL: "https://gelbooru.com/img/x/7.jpg"})

	resp, code := fetchSourceComments(t, ts, "/api/posts/7/source-comments?site=gelbooru")
	if code != http.StatusOK {
		t.Fatalf("unsupported must be 200, got %d", code)
	}
	if !resp.Unsupported || len(resp.Comments) != 0 {
		t.Fatalf("want unsupported+empty, got %+v", resp)
	}
}

func TestGetSourceCommentsUnknownSite(t *testing.T) {
	prov := &stubCommentsProvider{name: "hypnohub"}
	ts := setupSourceCommentsRouter(t, prov)
	GetDB().UpsertMeta(&Post{ID: 8, FileURL: "https://example.org/8.jpg"})

	// Сайт неизвестен — приложение не знает, куда идти.
	resp, code := fetchSourceComments(t, ts, "/api/posts/8/source-comments?site=unknownsite")
	if code != http.StatusOK || !resp.Unsupported {
		t.Fatalf("code=%d resp=%+v", code, resp)
	}
	if prov.calls != 0 {
		t.Fatal("unknown site must not hit the provider")
	}
	// Пустой site (пост без источника) — тоже unsupported, без похода в сеть.
	resp, code = fetchSourceComments(t, ts, "/api/posts/8/source-comments")
	if code != http.StatusOK || !resp.Unsupported {
		t.Fatalf("no site: code=%d resp=%+v", code, resp)
	}
}

func TestGetSourceCommentsTransientErrorIsRateLimited(t *testing.T) {
	prov := &stubCommentsProvider{name: "hypnohub", err: errAPITransient}
	ts := setupSourceCommentsRouter(t, prov)
	GetDB().UpsertMeta(&Post{ID: 9, FileURL: "https://hypnohub.net/img/x/9.jpg"})

	_, code := fetchSourceComments(t, ts, "/api/posts/9/source-comments?site=hypnohub")
	if code != http.StatusTooManyRequests {
		t.Fatalf("rate limit must surface as 429, got %d", code)
	}
}

func TestGetSourceCommentsMeta(t *testing.T) {
	prov := &stubCommentsProvider{name: "hypnohub", reply: []*SourceComment{{ID: 1, Author: "a", Body: "x"}}}
	ts := setupSourceCommentsRouter(t, prov)
	GetDB().UpsertMeta(&Post{ID: 11, FileURL: "https://hypnohub.net/img/x/11.jpg"})
	fetchSourceComments(t, ts, "/api/posts/11/source-comments?site=hypnohub")

	resp, err := http.Get(ts.URL + "/api/posts/11/source-comments/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var meta struct {
		HasComments bool `json:"has_comments"`
		Count       int  `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if !meta.HasComments || meta.Count != 1 {
		t.Fatalf("meta: %+v", meta)
	}
}
