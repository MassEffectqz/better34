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

// booruTestServer создаёт изолированный роутер + сервер для тестов Booru-API
// с двумя тестовыми постами (id=7 с тегом «neko», id=9) и алиасом catgirl→neko.
func booruTestServer(t *testing.T) (*httptest.Server, func(string) (int, map[string]any)) {
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

	db := GetDB()
	db.AddOrUpdate(&Post{ID: 7, Tags: "touhou neko", FileType: "jpg", Width: 100, Height: 200,
		FileSize: 4096, Score: 42, Rating: "explicit", Source: "rule34", MD5: "abc123",
		Downloaded: true, FilePath: "data/posts/7/original.jpg"})
	db.AddOrUpdate(&Post{ID: 9, Tags: "solo", FileType: "png", Width: 5, Height: 5,
		FileSize: 100, Score: 1, Rating: "safe", Downloaded: true, FilePath: "data/posts/9/original.png"})
	db.AddTagAlias("catgirl", "neko")

	gin.SetMode(gin.TestMode)
	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	{
		api.GET("/booru/posts", h.BooruPosts)
		api.GET("/booru/posts.json", h.BooruPosts)
		api.GET("/booru/tags", h.BooruTags)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	getJSON := func(path string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	return ts, getJSON
}

// TestBooruPosts — экспорт библиотеки наружу: gelbooru-обёртка и голый
// массив для /posts.json, алиасы применяются к запросу, URL файлов
// абсолютные (чтобы Hydrus мог скачать).
func TestBooruPosts(t *testing.T) {
	ts, getJSON := booruTestServer(t)

	// /posts.json → голый массив (danbooru-style).
	getAny := func(path string) (int, any) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var out any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, arrV := getAny("/api/booru/posts.json")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	arr, ok := arrV.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("ожидался массив из 2 постов, got %T %v", arrV, arrV)
	}
	// Порядок выдачи не гарантирован — проверяем появление обоих постов.
	var files = map[string]bool{}
	for _, v := range arr {
		fu, _ := v.(map[string]any)["file_url"].(string)
		files[fu] = true
	}
	if !files[ts.URL+"/api/save/7"] || !files[ts.URL+"/api/save/9"] {
		t.Fatalf("ожидались file_url /api/save/{7,9}, got %v", files)
	}
	// Черновые проверки полей (source/md5 у поста 7).
	for _, v := range arr {
		m := v.(map[string]any)
		if m["id"] == float64(7) {
			if m["md5"] != "abc123" || m["source"] != "rule34" {
				t.Fatalf("поля поста 7: %v", m)
			}
		}
	}

	// Обычный /posts → gelbooru-0.2 обёртке.
	code, wrapped := getJSON("/api/booru/posts?limit=1")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if _, ok := wrapped["@attributes"]; !ok {
		t.Fatalf("нет обёртки @attributes: %v", wrapped)
	}
	posts, _ := wrapped["post"].([]any)
	if len(posts) != 1 {
		t.Fatalf("limit=1 должен вернуть один пост, got %d", len(posts))
	}

	// format=gelbooru (по умолчанию) и format=array — эквивалентны /posts и /posts.json.
	_, arrFmt := getAny("/api/booru/posts?format=array")
	if arr, ok := arrFmt.([]any); !ok || len(arr) != 2 {
		t.Fatalf("format=array должен отдать массив из 2 постов, got %T %v", arrFmt, arrFmt)
	}

	// Алиас в запросе: «catgirl» → «neko».
	_, viaAlias := getJSON("/api/booru/posts?tags=catgirl")
	posts2, _ := viaAlias["post"].([]any)
	if len(posts2) != 1 || posts2[0].(map[string]any)["id"] != float64(7) {
		t.Fatalf("поиск по алиасу: %v", viaAlias)
	}

	// Пагинация pid=2 при limit=1 должен быть пустым (offset за пределами).
	_, pg := getJSON("/api/booru/posts?pid=3&limit=1")
	posts3, _ := pg["post"].([]any)
	if len(posts3) != 0 {
		t.Fatalf("pid за пределами должен быть пустым, got %d", len(posts3))
	}

	// /booru/tags — хотя бы два тега с количеством.
	_, tags := getJSON("/api/booru/tags")
	tagsList, _ := tags["tags"].([]any)
	if len(tagsList) < 2 {
		t.Fatalf("тегов должно быть ≥2, got %v", tags)
	}
}
