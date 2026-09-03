package internal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// GetTagCount должен возвращать счётчик именно запрошенного тега:
// fuzzy-автодополнение возвращает и похожие теги, и раньше при отсутствии
// точного совпадения приписывался счётчик первого «похожего».
func TestGetTagCountExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "autocomplete.php" {
			switch r.URL.Query().Get("q") {
			case "mytag":
				w.Write([]byte(`[{"label":"mytag","value":"mytag","count":42},{"label":"mytag2","value":"mytag2","count":999}]`))
			case "other":
				w.Write([]byte(`[{"label":"other_thing","value":"other_thing","count":7}]`))
			default:
				w.Write([]byte(`[]`))
			}
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewRule34Client()
	c.spec.apiURL = srv.URL + "/index.php"
	c.httpClient.Store(&http.Client{})
	c.cache = newBooruCache(filepath.Join(os.TempDir(), "r34_sc_tagcount_test.json"))
	c.suggMu.Lock()
	c.suggM = make(map[string]suggestionCacheEntry)
	c.suggMu.Unlock()
	c.suggBreaker.failures = 0
	c.suggBreaker.openUntil = time.Time{}
	seedTestKeys(c, []APICredential{{Name: "test", APIKey: "testkey", UserID: "1"}})

	n, err := c.GetTagCount("mytag")
	if err != nil {
		t.Fatalf("GetTagCount: %v", err)
	}
	if n != 42 {
		t.Fatalf("GetTagCount(mytag) = %d, want 42", n)
	}

	// Точного совпадения нет — раньше вернулся бы 7 от «other_thing».
	n, err = c.GetTagCount("other")
	if err != nil {
		t.Fatalf("GetTagCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("GetTagCount(other) = %d, want 0 (no exact match)", n)
	}
}
