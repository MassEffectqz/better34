package internal

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Тесты бьют по локальным httptest-серверам: глобальный троттлинг
// там не нужен, иначе тесты тянутся на секунды. Персист tag-count
// уводим во временную папку, чтобы тесты не трогали реальный data/cache.
func TestMain(m *testing.M) {
	apiRatePerSecond = 1000
	apiRateBurst = 1000
	dir, err := os.MkdirTemp("", "briefly-test-cache")
	if err == nil {
		tagCountsFile = filepath.Join(dir, "tag_counts.json")
	}
	code := m.Run()
	if err == nil {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// Лимитер должен сглаживать всплеск: первые burst запросов мгновенно,
// дальше — не быстрее perSec.
func TestRateLimiterPacesBurst(t *testing.T) {
	r := &rateLimiter{tokens: 2, last: time.Now(), perSec: 50, burst: 2}
	start := time.Now()
	for i := 0; i < 6; i++ {
		r.Wait()
	}
	elapsed := time.Since(start)
	// 4 «лишних» токена при 50/с ≈ 80мс.
	if elapsed < 40*time.Millisecond {
		t.Errorf("limiter did not pace: 6 waits took %v, expect >= ~80ms", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("limiter too slow: %v", elapsed)
	}
}

func TestRateLimiterNilSafe(t *testing.T) {
	var r *rateLimiter
	done := make(chan struct{})
	go func() {
		r.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil limiter blocked")
	}
}

// При быстрой печати одинаковые префиксы должны схлопываться в один
// запрос к autocomplete.php.
func TestSuggestTagsSingleflightDedup(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`[{"label":"touhou","value":"touhou","count":100}]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sugg, err := cl.SuggestTags("Touhou ")
			if err != nil {
				t.Errorf("SuggestTags: %v", err)
				return
			}
			if len(sugg) != 1 || sugg[0].Value != "touhou" {
				t.Errorf("unexpected suggestions: %+v", sugg)
			}
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 upstream call for %d concurrent identical queries, got %d", n, got)
	}
}

// 429 от автодополнения не должен открывать брейкер (раньше каждая
// 429 считалась аварией и после 5 подряд блокировался весь API).
func TestSuggest429DoesNotOpenBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	for i := 0; i < 7; i++ {
		tag := fmt.Sprintf("tag%d", i)
		if _, err := cl.SuggestTags(tag); err == nil {
			t.Fatalf("expected error for %q", tag)
		}
	}
	cl.breaker.mu.Lock()
	allow := time.Now().After(cl.breaker.openUntil)
	fails := cl.breaker.failures
	cl.breaker.mu.Unlock()
	if !allow || fails != 0 {
		t.Errorf("breaker must stay closed on 429: allow=%v failures=%d", allow, fails)
	}
}

// Аналогично для поиска: исчерпанные ретраи 429 дают errAPITransient
// и не наказывают брейкер.
func TestSearchPosts429TransientNoBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	_, err := cl.SearchPosts("cat_girl", 1, 10, 0)
	if !errors.Is(err, errAPITransient) {
		t.Fatalf("err = %v, want errAPITransient", err)
	}
	cl.breaker.mu.Lock()
	allow := time.Now().After(cl.breaker.openUntil)
	fails := cl.breaker.failures
	cl.breaker.mu.Unlock()
	if !allow || fails != 0 {
		t.Errorf("breaker must stay closed on 429: allow=%v failures=%d", allow, fails)
	}
}

// 5xx по-прежнему считаются аварией апстрима и копят failures брейкера.
func TestSearchPosts500StillFailsBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	_, _ = cl.SearchPosts("cat_girl", 1, 10, 0)
	cl.breaker.mu.Lock()
	fails := cl.breaker.failures
	cl.breaker.mu.Unlock()
	if fails == 0 {
		t.Error("5xx must count as breaker failure")
	}
}

// Сбои автодополнения (5xx) открывают ТОЛЬКО suggest-брейкер:
// поиск по тому же сайту продолжает работать.
func TestSuggestBreakerIsolatedFromSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "autocomplete") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// dapi-ответ поиска с одним постом.
		w.Write([]byte(`[{"id":1,"tags":"cat_girl","file_url":"a.jpg","preview_url":"t.jpg"}]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	for i := 0; i < 7; i++ {
		_, _ = cl.SuggestTags(fmt.Sprintf("query%d", i))
	}

	cl.suggBreaker.mu.Lock()
	suggAllow := time.Now().After(cl.suggBreaker.openUntil)
	cl.suggBreaker.mu.Unlock()
	if suggAllow {
		t.Error("suggest breaker must be open after repeated 5xx")
	}

	cl.breaker.mu.Lock()
	mainAllow := time.Now().After(cl.breaker.openUntil)
	fails := cl.breaker.failures
	cl.breaker.mu.Unlock()
	if !mainAllow || fails != 0 {
		t.Errorf("main breaker must stay closed: allow=%v failures=%d", mainAllow, fails)
	}

	posts, err := cl.SearchPosts("cat_girl", 1, 10, 0)
	if err != nil || len(posts) != 1 {
		t.Errorf("search broken by suggest failures: posts=%d err=%v", len(posts), err)
	}
}

func wireTestClient(t *testing.T, cl *booruClient, srv *httptest.Server) {
	t.Helper()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "k"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.suggMu.Lock()
	cl.suggM = make(map[string]suggestionCacheEntry)
	cl.suggMu.Unlock()
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}
}
