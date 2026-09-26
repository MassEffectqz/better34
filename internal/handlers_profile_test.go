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

// setupProfileTestRouter поднимает роутер с GET /api/profile в изолированной
// data/ (та же схема, что и setupBatchTestRouter).
func setupProfileTestRouter(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)

	gin.SetMode(gin.TestMode)
	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	{
		api.GET("/profile", h.GetProfileData)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// TestGetProfileDataOrdering — лайки отсортированы по дате лайка (новые
// первыми, без метки — по id), скрытые — по id, теги — по алфавиту; ответ
// содержит карту liked_at для клиентской сортировки.
func TestGetProfileDataOrdering(t *testing.T) {
	ts := setupProfileTestRouter(t)

	p := GetProfile()
	p.mu.Lock()
	p.LikedPosts = map[int]bool{10: true, 20: true, 30: true}
	p.LikedAt = map[int]int64{10: 100, 30: 300} // 20 — старый лайк без метки
	p.HiddenPosts = map[int]bool{5: true, 9: true, 7: true}
	p.FavTags = map[string]bool{"zebra": true, "alpha": true, "mango": true}
	p.HiddenTags = map[string]bool{"zzz": true, "aaa": true}
	p.mu.Unlock()

	resp, err := http.Get(ts.URL + "/api/profile")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		LikedPosts  []int            `json:"liked_posts"`
		LikedAt     map[string]int64 `json:"liked_at"`
		HiddenPosts []int            `json:"hidden_posts"`
		FavTags     []string         `json:"fav_tags"`
		HiddenTags  []string         `json:"hidden_tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 30 (LikedAt=300) → 10 (LikedAt=100) → 20 (без метки, id ниже).
	wantLikes := []int{30, 10, 20}
	if len(out.LikedPosts) != len(wantLikes) {
		t.Fatalf("likes: got %v", out.LikedPosts)
	}
	for i, id := range wantLikes {
		if out.LikedPosts[i] != id {
			t.Fatalf("порядок лайков: got %v, want %v", out.LikedPosts, wantLikes)
		}
	}
	if out.LikedAt == nil || out.LikedAt["30"] != 300 {
		t.Fatalf("liked_at отсутствует или неполон: %v", out.LikedAt)
	}

	wantHides := []int{9, 7, 5}
	if len(out.HiddenPosts) != len(wantHides) {
		t.Fatalf("hides: got %v", out.HiddenPosts)
	}
	for i, id := range wantHides {
		if out.HiddenPosts[i] != id {
			t.Fatalf("порядок скрытых: got %v, want %v", out.HiddenPosts, wantHides)
		}
	}

	wantFav := []string{"alpha", "mango", "zebra"}
	if len(out.FavTags) != 3 || out.FavTags[0] != wantFav[0] || out.FavTags[1] != wantFav[1] || out.FavTags[2] != wantFav[2] {
		t.Fatalf("порядок избранных тегов: got %v", out.FavTags)
	}
	if len(out.HiddenTags) != 2 || out.HiddenTags[0] != "aaa" || out.HiddenTags[1] != "zzz" {
		t.Fatalf("порядок скрытых тегов: got %v", out.HiddenTags)
	}
}

// setupPostsByIDsRouter — роутер с GET /api/posts-by-ids в изолированной data/
// (та же схема, что и setupProfileTestRouter).
func setupPostsByIDsRouter(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)

	// Глобальная БД держит posts.db открытым — на Windows TempDir не удалится.
	// Сбрасываем dbOnce, чтобы GetDB() в следующих тестах создал свежую БД в их
	// рабочем каталоге (как в TestIntegrationReadyMetricsSettingsPassword).
	t.Setenv("BRIEFLY_DB_BACKUPS", "0")
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})

	gin.SetMode(gin.TestMode)
	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	{
		api.GET("/posts-by-ids", h.GetPostsByIDs)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// TestGetPostsByIDsViewedFlag — /posts-by-ids отдаёт флаг viewed по данным
// view_history: 501 помечен просмотренным, 502 — нет. Нужен вкладкам
// «Лайки/Скрытые»: по нему клиент помечает пост «новым» и фильтрует непросмотренное.
func TestGetPostsByIDsViewedFlag(t *testing.T) {
	ts := setupPostsByIDsRouter(t)

	db := GetDB()
	db.UpsertMetaMany([]*Post{
		{ID: 501, PreviewURL: "https://example.invalid/501.jpg", FileURL: "https://example.invalid/501.jpg", FileType: "jpg", Tags: "a"},
		{ID: 502, PreviewURL: "https://example.invalid/502.jpg", FileURL: "https://example.invalid/502.jpg", FileType: "jpg", Tags: "b"},
	})
	db.RecordViews([]int{501})

	resp, err := http.Get(ts.URL + "/api/posts-by-ids?ids=501,502")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Posts []struct {
			ID     int  `json:"id"`
			Viewed bool `json:"viewed"`
		} `json:"posts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Posts) != 2 {
		t.Fatalf("posts: got %d, want 2", len(out.Posts))
	}
	viewed := map[int]bool{}
	for _, p := range out.Posts {
		viewed[p.ID] = p.Viewed
	}
	if _, ok := viewed[501]; !ok {
		t.Fatalf("501 отсутствует в ответе: %v", out.Posts)
	}
	if _, ok := viewed[502]; !ok {
		t.Fatalf("502 отсутствует в ответе: %v", out.Posts)
	}
	if !viewed[501] {
		t.Errorf("501 помечен просмотренным в view_history, но viewed=false")
	}
	if viewed[502] {
		t.Errorf("502 не просматривался, но viewed=true")
	}
}
