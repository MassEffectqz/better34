package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestTagAliasesDB — базовые операции с таблицей алиасов.
func TestTagAliasesDB(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "aliases.db"))
	t.Cleanup(func() { db.Close() })

	if db.AddTagAlias("", "neko") || db.AddTagAlias("catgirl", "") {
		t.Fatal("пустые алиас/цель не должны приниматься")
	}
	if db.AddTagAlias("neko", "neko") {
		t.Fatal("alias==target не должен приниматься")
	}
	if !db.AddTagAlias("CatGirl", "NEKO") {
		t.Fatal("валидный алиас не добавлен")
	}
	if got := db.ResolveTag("catgirl"); got != "neko" {
		t.Fatalf("ResolveTag(catgirl)=%q, ожидалось neko", got)
	}
	if got := db.ResolveTag("CATGIRL"); got != "neko" {
		t.Fatalf("ResolveTag не регистронезависим: %q", got)
	}
	if got := db.ResolveTag("solo"); got != "solo" {
		t.Fatalf("не-алиас вернулся изменённым: %q", got)
	}
	// Перезапись.
	if !db.AddTagAlias("catgirl", "cat_ears") {
		t.Fatal("перезапись алиаса не удалась")
	}
	if got := db.ResolveTag("catgirl"); got != "cat_ears" {
		t.Fatalf("перезапись не применилась: %q", got)
	}
	aliases := db.ListTagAliases()
	if len(aliases) != 1 || aliases[0].Alias != "catgirl" || aliases[0].Target != "cat_ears" {
		t.Fatalf("ListTagAliases: %+v", aliases)
	}
	if !db.DeleteTagAlias("CATGIRL") {
		t.Fatal("удаление алиаса не удалось")
	}
	if got := db.ResolveTag("catgirl"); got != "catgirl" {
		t.Fatalf("после удаления алиас остался: %q", got)
	}
}

// TestResolveAliasesInQuery — строка поиска переводит синонимы в канон,
// сохраняя префиксы +/- и пропуская метаформы (чистая логика без БД).
func TestResolveAliasesInQuery(t *testing.T) {
	m := map[string]string{"catgirl": "neko", "futanari": "dickgirl"}
	resolver := func(s string) string {
		if v, ok := m[s]; ok {
			return v
		}
		return s
	}

	cases := []struct{ in, want string }{
		{"catgirl", "neko"},
		{"+catgirl", "+neko"},
		{"-catgirl", "-neko"},
		{"catgirl solo", "neko solo"},
		{"catgirl futanari pink_hair", "neko dickgirl pink_hair"},
		{"id:12345", "id:12345"},
		{"rating:sfw", "rating:sfw"},
		{"touhou | catgirl", "touhou | neko"},
		{"solo", "solo"},
		{"", ""},
	}
	for _, c := range cases {
		if got := resolveAliases(c.in, resolver); got != c.want {
			t.Errorf("resolveAliases(%q)=%q, ожидалось %q", c.in, got, c.want)
		}
	}
}

// TestTagAliasHandlers — CRUD алиасов через HTTP (GET/POST/DELETE).
func TestTagAliasHandlers(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	// GetDB() создаёт data/posts.db в этом каталоге и держит файл открытым —
	// закрываем и сбрасываем глобальную БД, чтобы TempDir удалился.
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
		api.GET("/tag-aliases", h.ListTagAliases)
		api.POST("/tag-alias", h.AddTagAlias)
		api.DELETE("/tag-alias/:alias", h.DeleteTagAlias)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	do := func(method, path, body string) (int, map[string]any) {
		var rd *bytes.Reader
		if body != "" {
			rd = bytes.NewReader([]byte(body))
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	raw, _ := json.Marshal(map[string]string{"alias": "catgirl", "target": "neko"})
	code, out := do("POST", "/api/tag-alias", string(raw))
	if code != http.StatusOK || out["target"] != "neko" {
		t.Fatalf("POST: %d %v", code, out)
	}
	code, out = do("POST", "/api/tag-alias", `{"alias":"a","target":"a"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("alias==target должен отклоняться: %d", code)
	}
	code, out = do("GET", "/api/tag-aliases", "")
	if code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	aliases, _ := out["aliases"].([]any)
	if len(aliases) != 1 {
		t.Fatalf("GET: ожидался 1 алиас, got %d", len(aliases))
	}
	code, _ = do("DELETE", "/api/tag-alias/catgirl", "")
	if code != http.StatusOK {
		t.Fatalf("DELETE: %d", code)
	}
	code, _ = do("DELETE", "/api/tag-alias/catgirl", "")
	if code != http.StatusNotFound {
		t.Fatalf("повторный DELETE должен быть 404, got %d", code)
	}
}
