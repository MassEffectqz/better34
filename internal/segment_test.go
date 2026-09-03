package internal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

var zeroTime = time.Time{}

// Сегментированная закачка: файл >= segMinSize при включённом Range
// качается N параллельными byte-range и целиком попадает в media-cache,
// тело ответа клиента — побайтово как у однопоточной скачки.
func TestStreamSegmentedParallelRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	oldMin, oldPar := segMinSize, segParallel
	segMinSize, segParallel = 64<<10, 2
	t.Cleanup(func() { segMinSize, segParallel = oldMin, oldPar })

	payload := make([]byte, 300<<10)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// http.ServeContent сам обслуживает Range/206.
		http.ServeContent(w, r, "v.mp4", zeroTime, bytes.NewReader(payload))
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}
	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/v.mp4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.ProxyRemote(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("segmented body corrupted: got %d bytes", w.Body.Len())
	}
	// Проба + 2 сегмента = 3 запроса к CDN; параллельность проверяем
	// фактом 206-ответов на каждый сегмент (сервер отдал бы 200 при
	// отсутствии Range — тогда сработал бы fallback и hits был бы 1).
	if got := hits.Load(); got != 3 {
		t.Fatalf("upstream hits = %d, want 3 (probe + 2 segments)", got)
	}
	if p := mediaCacheGet(srv.URL + "/v.mp4"); p == "" {
		t.Fatal("segmented file not committed to media cache")
	}
	if n := countTmpFiles(); n != 0 {
		t.Fatalf("leftover temp files: %d", n)
	}

	// Повторный запрос обслуживается из кэша без похода на CDN.
	before := hits.Load()
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	req2 := httptest.NewRequest(http.MethodGet, target, nil)
	req2.Header.Set("Range", "bytes=100-199")
	c2.Request = req2
	h.ProxyRemote(c2)
	if w2.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", w2.Code)
	}
	if got := hits.Load(); got != before {
		t.Fatalf("cache hit went upstream: hits %d -> %d", before, got)
	}
}

// CDN без поддержки Range: проба получает 200 — откат на обычный стрим,
// тело клиента не страдает, файл оседает в кэше через mediaCacheSink.
func TestStreamSegmentedFallbackNoRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	oldMin := segMinSize
	segMinSize = 64 << 10
	t.Cleanup(func() { segMinSize = oldMin })

	payload := make([]byte, 5<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload) // Range игнорируется
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}
	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/v.mp4")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.ProxyRemote(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatalf("fallback body corrupted: got %d bytes", w.Body.Len())
	}
	if p := mediaCacheGet(srv.URL + "/v.mp4"); p == "" {
		t.Fatal("fallback stream not cached")
	}
}
