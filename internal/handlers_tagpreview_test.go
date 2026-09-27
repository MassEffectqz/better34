package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// setupTagPreviewRouter поднимает роутер с изолированной data/ и наполняет
// БД постами с тегами, чтобы проверить счётчик, обложки и смежные теги.
func setupTagPreviewRouter(t *testing.T) (*httptest.Server, *PostDB) {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)
	gin.SetMode(gin.TestMode)
	db := NewPostDB("data/posts.db")
	t.Cleanup(func() {
		db.Close()
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})

	// Библиотека из 20 постов: 1girl есть везде (значит, как сосед он и
	// отсеется фильтром «везде», это правильно), blue_hair — в 6 постах с
	// 1girl и ещё в одном БЕЗ него (частота 7, связь слабее), smile — в 5,
	// solo — в 4, long_hair — всего в одном (шум, должен отпасть).
	const n = 20
	tagsFor := func(i int) string {
		base := "1girl"
		switch {
		case i <= 6:
			base += " blue_hair"
		case i == 7:
			base = "blue_hair" // без 1girl — вот и делает его частоту выше связи
		case i <= 12:
			base += " smile"
		case i <= 16:
			base += " solo"
		}
		if i == 3 {
			base += " long_hair"
		}
		return base
	}
	for i := 1; i <= n; i++ {
		db.UpsertMeta(&Post{ID: i, Tags: tagsFor(i), FileURL: "https://x/" + strconv.Itoa(i) + ".jpg"})
	}
	// Часть постов скачана — только у них будут обложки.
	for _, id := range []int{1, 2, 3, 5, 17, 20} {
		db.SetDownloaded(id, "data/save/"+strconv.Itoa(id)+".jpg", "thumbs/"+strconv.Itoa(id)+".jpg")
	}

	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	api.GET("/tags/:tag/preview", h.TagPreview)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, db
}

func fetchTagPreview(t *testing.T, ts *httptest.Server, path string) (TagPreview, int) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out TagPreview
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out, resp.StatusCode
}

func TestTagPreviewCountAndCovers(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, code := fetchTagPreview(t, ts, "/api/tags/1girl/preview")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	// 1girl есть у 19 постов из 20 (пост 7 у нас только с blue_hair).
	if got.Count != 19 {
		t.Errorf("count=%d, want 19", got.Count)
	}
	// Обложки только у скачанных: 17, 20, 5, 3, 2, 1 — шесть, по id по убыванию.
	if len(got.Posts) != 6 {
		t.Fatalf("covers=%d, want 6", len(got.Posts))
	}
	if got.Posts[0].ID != 20 {
		t.Errorf("newest first expected, got %d", got.Posts[0].ID)
	}
	if got.Posts[0].Thumb != "/api/thumb/20" {
		t.Errorf("thumb url=%q", got.Posts[0].Thumb)
	}
}

func TestTagPreviewUnknownTagIsEmptyNot404(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, code := fetchTagPreview(t, ts, "/api/tags/nosuchtag/preview")
	if code != http.StatusOK {
		t.Fatalf("unknown tag must be 200, got %d", code)
	}
	if got.Count != 0 || len(got.Posts) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func TestTagPreviewNormalizesTagName(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	// С пробелами и в верхнем регистре, как это бывает в интерфейсе.
	got, code := fetchTagPreview(t, ts, "/api/tags/Blue+Hair/preview")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	// blue_hair есть в постах 1–6 плюс пост 7 (где он без 1girl).
	if got.Count != 7 {
		t.Errorf("normalized count=%d, want 7", got.Count)
	}
	if got.Tag != "blue_hair" {
		t.Errorf("normalized tag=%q", got.Tag)
	}
}

// Смежные теги: важно отсечь «популярное, но не связанное». long_hair
// встречается в одном посте — попасть не должен, blue_hair в трёх — должен.
// Смежные теги: отсекаем «везде» (1girl — во всех 20 постах, как сосед он
// бесполезен) и шум из одного поста (long_hair). Остальное — по числу
// совместных постов: blue_hair(6), smile(5), solo(4).
func TestTagPreviewRelatedTagsRanking(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, _ := fetchTagPreview(t, ts, "/api/tags/1girl/preview?related=10")
	names := make([]string, 0, len(got.Related))
	for _, r := range got.Related {
		names = append(names, r.Tag)
	}
	if len(names) == 0 {
		t.Fatal("expected related tags for 1girl")
	}
	for _, n := range names {
		if n == "long_hair" {
			t.Errorf("single-post match must be dropped: %v", names)
		}
		if n == "1girl" {
			t.Errorf("the tag itself must not be a neighbour: %v", names)
		}
	}
	want := []string{"blue_hair", "smile", "solo"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("related=%v, want %v", names, want)
	}
}

func TestTagPreviewRelatedDisabled(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, _ := fetchTagPreview(t, ts, "/api/tags/1girl/preview?related=0")
	if len(got.Related) != 0 {
		t.Errorf("related=0 must return none, got %+v", got.Related)
	}
	if got.Count == 0 {
		t.Error("count must still be returned")
	}
}
