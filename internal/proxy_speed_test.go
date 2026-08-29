package internal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Параллельные /api/proxy одного URL должны обслуживаться одним походом
// на CDN: лидер качает, остальные ждут его байты (или берут из кэша).
func TestProxyRemoteSingleflightDedupSmallFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	var hits atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release // держим лидера, пока все запросы не соберутся в группу
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", "5")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}
	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/pic.jpg")

	const n = 8
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, target, nil)
			h.ProxyRemote(c)
			recs[i] = w
		}(i)
	}
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
	for i, w := range recs {
		if w == nil || w.Code != http.StatusOK || w.Body.String() != "hello" {
			t.Errorf("request %d: code=%v body=%v", i, w, w)
		}
	}
}

// Большой файл (>4MB): лидер уходит в стриминг, ожидавшие качают своим
// запросом — все клиенты получают полный ответ без порчи данных.
func TestProxyRemoteBigFileStreamParallel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withTempMediaCache(t)

	payload := make([]byte, 6<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	h := NewHandler()
	h.providers["__stub"] = stubProxyProvider{}
	target := "/api/proxy?url=" + url.QueryEscape(srv.URL+"/big.mp4")

	const n = 3
	bodies := make([][]byte, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, target, nil)
			h.ProxyRemote(c)
			if w.Code == http.StatusOK {
				bodies[i] = w.Body.Bytes()
			} else {
				t.Errorf("request %d: status %d", i, w.Code)
			}
		}(i)
	}
	wg.Wait()

	for i, b := range bodies {
		if !bytes.Equal(b, payload) {
			t.Errorf("request %d: body corrupted (%d bytes)", i, len(b))
		}
	}

	// После стрима файл должен осесть в дисковом кэше, а повторный
	// запрос — обслужиться из него без похода на CDN.
	if p := mediaCacheGet(srv.URL + "/big.mp4"); p == "" {
		t.Fatal("big file not persisted to media cache")
	}
	before := func() int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Range", "bytes=0-99")
		c.Request = req
		h.ProxyRemote(c)
		return w.Code
	}()
	if before != http.StatusPartialContent {
		t.Fatalf("range from cache: status %d", before)
	}
}

// Транспорт должен ходить по HTTP/2 с переиспользованием соединений:
// свой DialContext без ForceAttemptHTTP2 молча отключает h2.
func TestResolveTransportHTTP2AndKeepAlive(t *testing.T) {
	tr := NewResolveTransport("https://cloudflare-dns.com/dns-query")
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 must be enabled: custom DialContext silently disables HTTP/2")
	}
	if tr.MaxIdleConnsPerHost < 16 {
		t.Errorf("MaxIdleConnsPerHost = %d, want >= 16 (default 2 starves thumbnail grids)",
			tr.MaxIdleConnsPerHost)
	}
	if tr.MaxIdleConns < tr.MaxIdleConnsPerHost*2 {
		t.Errorf("MaxIdleConns = %d too small vs per-host %d", tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
}
