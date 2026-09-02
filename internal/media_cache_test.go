package internal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

// stubProxyProvider подменяет проверку хостов, чтобы ProxyRemote пускал
// запросы на httptest-сервер (127.0.0.1 не входит в CDN-хосты провайдеров).
type stubProxyProvider struct{ Provider }

func (stubProxyProvider) AllowsHost(string) bool { return true }
func (stubProxyProvider) RefererURL() string     { return "http://stub/" }

func withTempMediaCache(t *testing.T) {
	t.Helper()
	oldDir := mediaCacheDir
	mediaCacheDir = filepath.Join(t.TempDir(), "media-cache")
	mediaDiskOnce = sync.Once{}
	oldInterval := mediaCacheEvictInterval
	mediaCacheEvictInterval = 0 // в тестах эвиция без троттлинга
	lastMediaCacheEvict.Store(0)
	t.Cleanup(func() {
		mediaCacheDir = oldDir
		mediaDiskOnce = sync.Once{}
		mediaCacheEvictInterval = oldInterval
	})
}

func countTmpFiles() int {
	matches, _ := filepath.Glob(filepath.Join(mediaCacheDir, "*.tmp"))
	return len(matches)
}

// Большое видео (>4MB RAM-лимита) при первом полном просмотре стримится
// клиенту и оседает в дисковом кэше; повторный Range-запрос отдаётся из
// кэша без похода на CDN — иначе перемотка каждый раз перекачивала файл.
func TestProxyRemoteBigVideoCachedAndRangeServedFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	payload := make([]byte, 6<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}

	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/big.mp4")

	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.ProxyRemote(c1)

	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w1.Code, http.StatusOK)
	}
	if !bytes.Equal(w1.Body.Bytes(), payload) {
		t.Fatalf("тело ответа повреждено: got %d bytes", w1.Body.Len())
	}
	key := srv.URL + "/big.mp4"
	if p := mediaCacheGet(key); p == "" {
		t.Fatal("большой файл не попал в дисковый кэш")
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("в кэше осталось %d временных файлов", n)
	}

	hitsAfterFirst := atomic.LoadInt32(&hits)

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	req2 := httptest.NewRequest(http.MethodGet, target, nil)
	req2.Header.Set("Range", "bytes=100-199")
	c2.Request = req2
	h.ProxyRemote(c2)

	if w2.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", w2.Code, http.StatusPartialContent)
	}
	if w2.Body.Len() != 100 || !bytes.Equal(w2.Body.Bytes(), payload[100:200]) {
		t.Fatalf("range body = %d bytes, want payload[100:200]", w2.Body.Len())
	}
	if got := atomic.LoadInt32(&hits); got != hitsAfterFirst {
		t.Fatalf("Range-запрос ушёл на CDN (+%d), должен отдаваться из кэша", got-hitsAfterFirst)
	}
}

// Обрыв апстрима посередине не должен оставлять недокачанный файл в кэше.
func TestProxyRemoteTruncatedUpstreamNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	payload := make([]byte, 6<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload[:1024])
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}

	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/cut.mp4")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.ProxyRemote(c)

	if p := mediaCacheGet(srv.URL + "/cut.mp4"); p != "" {
		t.Fatalf("обрезанный ответ попал в кэш: %s", p)
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("после обрыва осталось %d временных файлов", n)
	}
}

// Бюджет диска: при переполнении выкидываются самые старые файлы.
func TestMediaCacheEviction(t *testing.T) {
	withTempMediaCache(t)

	mediaCacheBudgetOverride = 300 << 10
	defer func() { mediaCacheBudgetOverride = 0 }()

	sink := newMediaCacheSink("a")
	sink.Write(make([]byte, 200<<10))
	sink.commit()
	if mediaCacheGet("a") == "" {
		t.Fatal("файл a не записан")
	}

	sink = newMediaCacheSink("b")
	sink.Write(make([]byte, 200<<10))
	sink.commit()

	if p := mediaCacheGet("a"); p != "" {
		t.Fatal("старый файл a должен быть вытеснен при превышении бюджета")
	}
	if p := mediaCacheGet("b"); p == "" {
		t.Fatal("новый файл b не должен быть вытеснен")
	}
}
