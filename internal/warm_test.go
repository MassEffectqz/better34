package internal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// waitWarmIdle ждёт (с таймаутом) завершения всех фоновых горутин прогрева.
// Они читают proxyCacheDir/mediaCacheDir и их sync.Once, поэтому очистка
// тестовых каталогов может выполняться только после них. false — таймаут.
func waitWarmIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for warmPending.Load() != 0 || mediaWarmSpawned.Load() != 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// warmTestSetup — httptest-CDN + Handler со стаб-провайдером и временными
// каталогами кэшей (как в обычных тестах прокси).
func warmTestSetup(t *testing.T, handler http.HandlerFunc) (*Handler, *httptest.Server, *atomic.Int64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	oldProxyDir := proxyCacheDir
	proxyCacheDir = filepath.Join(t.TempDir(), "proxy-cache")
	proxyDiskOnce = sync.Once{}
	t.Cleanup(func() {
		if !waitWarmIdle(30 * time.Second) {
			t.Logf("waitWarmIdle: фоновые прогревы не завершились за отведённое время")
		}
		proxyCacheDir = oldProxyDir
		proxyDiskOnce = sync.Once{}
	})

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}
	return h, srv, &hits
}

// Прогрев кладёт превью в proxy-cache с правильным Content-Type и
// заголовками запроса (Referer обязателен: CDN проверяет).
func TestWarmOneStoresPreview(t *testing.T) {
	var sawReferer atomic.Bool
	h, srv, hits := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "" {
			t.Error("warm request without Referer")
		}
		sawReferer.Store(true)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("warmbytes"))
	})

	u, err := url.Parse(srv.URL + "/p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if !warmOne(h, srv.URL+"/p.jpg", u) {
		t.Fatal("warmOne reported failure")
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits.Load())
	}
	if !sawReferer.Load() {
		t.Error("Referer header was not checked (no request reached the server?)")
	}
	it, ok := proxyCache.get(srv.URL + "/p.jpg")
	if !ok || it.ct != "image/jpeg" || string(it.data) != "warmbytes" {
		t.Fatalf("preview not stored: ok=%v ct=%q data=%q", ok, it.ct, string(it.data))
	}

	// Повторный прогрев уже закэшированного URL не ходит на CDN.
	if warmOne(h, srv.URL+"/p.jpg", u) {
		t.Error("warm of cached URL should be skipped")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("cached URL re-fetched: hits = %d, want 1", got)
	}
}

// Ошибки апстрима (не-200) не должны оставлять мусор в кэше.
func TestWarmOneFailsClean(t *testing.T) {
	h, srv, _ := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	u, _ := url.Parse(srv.URL + "/p.jpg")
	if warmOne(h, srv.URL+"/p.jpg", u) {
		t.Fatal("warmOne must report failure on non-200")
	}
	if _, ok := proxyCache.get(srv.URL + "/p.jpg"); ok {
		t.Fatal("failed warm stored data in proxy cache")
	}
}

// Скачанные картинки не прогреваются (сетка берёт /api/thumb),
// скачанные видео — прогреваются (их сетка показывает CDN-превью).
func TestWarmPreviewCacheSkipsDownloadedImages(t *testing.T) {
	h, srv, hits := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	})

	entries := []gin.H{
		{"preview_url": srv.URL + "/a.jpg", "downloaded": true, "file_type": "image"},
		{"preview_url": srv.URL + "/v.jpg", "downloaded": true, "file_type": "video"},
		{"preview_url": srv.URL + "/b.jpg", "downloaded": false, "file_type": "image"},
	}
	warmPreviewCache(h, entries)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("upstream hits = %d, want 2 (downloaded image must be skipped)", got)
	}
}

// kind=preview через прокси получает immutable Cache-Control.
func TestProxyPreviewImmutableCacheControl(t *testing.T) {
	h, srv, _ := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("hello"))
	})

	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/p.jpg") + "&kind=preview"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.ProxyRemote(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", cc)
	}

	// Без kind=preview — прежний режим (не immutable).
	target2 := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/p2.jpg")
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, target2, nil)
	h.ProxyRemote(c2)
	if cc := w2.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Fatalf("non-preview Cache-Control = %q, must not be immutable", cc)
	}
}

// ── warmMediaCacheFile: прогрев полного медиа в дисковый кэш ────────────
// Первый просмотр <video> (через `Range: bytes=0-`) докачивает весь файл
// фоном; повторный просмотр/перемотка обслуживаются из media-cache без CDN.

func TestWarmMediaCacheFileStoresFullFile(t *testing.T) {
	payload := make([]byte, 300<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	h, srv, hits := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	})

	u, err := url.Parse(srv.URL + "/v.mp4")
	if err != nil {
		t.Fatal(err)
	}
	warmMediaCacheFile(h, u)

	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
	p := mediaCacheGet(srv.URL + "/v.mp4")
	if p == "" {
		t.Fatal("full media not committed to media cache")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, payload) {
		t.Fatalf("cached body corrupted: got %d bytes, want %d", len(b), len(payload))
	}

	// Файл уже в кэше—повторный прогрев не ходит на CDN.

	warmMediaCacheFile(h, u)
	if got := hits.Load(); got != 1 {
		t.Fatalf("cached URL re-fetched: hits = %d, want 1", got)
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("leftover temp files: %d", n)
	}
}

// Параллельные вызовы одного URL дедуплицируются через warmInflight: CDN
// получает ровно один запрос, файл — один раз в кэше.

func TestWarmMediaCacheFileDedupParallel(t *testing.T) {
	payload := make([]byte, 64<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	h, srv, hits := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond) // держим лидера: параллельные успевают сгруппироваться
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(payload)
	})

	u, err := url.Parse(srv.URL + "/v.mp4")
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			warmMediaCacheFile(h, u)
		}()
	}
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (dedup via warmInflight)", got)
	}
	if p := mediaCacheGet(srv.URL + "/v.mp4"); p == "" {
		t.Fatal("warmed file missing from media cache")
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("leftover temp files: %d", n)
	}
}

// Ошибка апстрима(не-200) не оставляет ни файла, ни мусора в кэше.

func TestWarmMediaCacheFileFailsClean(t *testing.T) {
	h, srv, _ := warmTestSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	u, err := url.Parse(srv.URL + "/v.mp4")
	if err != nil {
		t.Fatal(err)
	}
	warmMediaCacheFile(h, u)

	if p := mediaCacheGet(srv.URL + "/v.mp4"); p != "" {
		t.Fatal("failed warm left file in media cache")
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("leftover temp files: %d", n)
	}
}
