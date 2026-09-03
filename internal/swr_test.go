package internal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// SWR: протухший по TTL поисковый кэш отдаётся мгновенно (без обращения
// к сети), а свежесть восстанавливается фоном — повторный запрос получает
// уже свежий ответ без ожидания RTT до CDN.
func TestSearchCacheStaleServedAndRefreshed(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// rule34: голый массив dapi-ответа, один пост.
		w.Write([]byte(`[{"id":99,"tags":"stale_tag","file_url":"https://rule34.xxx/x.jpg"}]`))
	}))
	defer srv.Close()

	c := NewRule34Client()
	c.spec.apiURL = srv.URL + "/index.php"
	c.httpClient.Store(&http.Client{})
	c.cache = newBooruCache(filepath.Join(os.TempDir(), "r34_swr_test.json"))
	c.breaker.failures = 0
	c.breaker.openUntil = time.Time{}
	seedTestKeys(c, []APICredential{{Name: "test", APIKey: "testkey", UserID: "1"}})

	// rule34 поддерживает sort:id:desc — SearchPosts добавит метатег сам.
	ckey := "stale_tag sort:id:desc|1|40|0"
	c.cache.mu.Lock()
	c.cache.m[ckey] = searchCacheEntry{
		posts: []Rule34Post{{ID: 42, Tags: "old", FileURL: "https://rule34.xxx/o.jpg"}},
		ts:    time.Now().Add(-30 * time.Minute),
	}
	c.cache.mu.Unlock()

	posts, err := c.SearchPosts("stale_tag", 1, 40, 0)
	if err != nil {
		t.Fatalf("SearchPosts: %v", err)
	}
	if len(posts) != 1 || posts[0].ID != 42 {
		t.Fatalf("stale serve: got %+v, want post 42", posts)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("stale serve пошёл в сеть сразу: %d запросов", n)
	}

	// Фоновое обновление в итоге кладёт в кэш свежий пост 99.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fresh, ok := c.cache.get(ckey); ok && len(fresh) == 1 && fresh[0].ID == 99 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("кэш не обновился фоном: hits=%d", hits.Load())
}
