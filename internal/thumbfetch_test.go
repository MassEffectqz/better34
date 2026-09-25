package internal

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// thumbTestServer returns a test server with a JPEG body and counts hits.
func thumbTestServer(t *testing.T, hits *int64) *httptest.Server {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for x := 0; x < 64; x++ {
		for y := 0; y < 48; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), B: uint8(y * 5), A: 255})
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "image/jpeg")
		_ = jpeg.Encode(w, img, nil)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubProvider is a minimal Provider over the real rule34 client that
// allows exactly one host, so host filtering can be verified.
type stubProvider struct {
	Provider
	host string
}

func (s *stubProvider) Name() string             { return "rule34" }
func (s *stubProvider) AllowsHost(h string) bool { return strings.EqualFold(h, s.host) }
func (s *stubProvider) RefererURL() string       { return "https://rule34.xxx/" }

func newStubProvider(host string) Provider {
	return &stubProvider{Provider: NewRule34Client(), host: host}
}

func TestThumbSourceURLPrefersPreview(t *testing.T) {
	p := &Post{PreviewURL: "https://cdn.example/p.jpg", FileURL: "https://cdn.example/f.png"}
	got, err := thumbSourceURL(p)
	if err != nil || got != p.PreviewURL {
		t.Fatalf("thumbSourceURL = %q, %v", got, err)
	}

	// Without a preview we take the original, but only for images.
	got, err = thumbSourceURL(&Post{FileURL: "https://cdn.example/f.png"})
	if err != nil || got != "https://cdn.example/f.png" {
		t.Fatalf("image file_url = %q, %v", got, err)
	}
	if _, err = thumbSourceURL(&Post{FileURL: "https://cdn.example/f.mp4"}); err == nil {
		t.Fatal("video file_url must not be used as a thumbnail source")
	}
	if _, err = thumbSourceURL(&Post{}); err == nil {
		t.Fatal("post without any URL must be unsupported")
	}
	if _, err = thumbSourceURL(nil); err == nil {
		t.Fatal("nil post must be unsupported")
	}
}

func TestThumbSourceURLIgnoresQueryInExt(t *testing.T) {
	// A query string must not turn .mp4 into an "image".
	if _, err := thumbSourceURL(&Post{FileURL: "https://cdn.example/f.mp4?token=1"}); err == nil {
		t.Fatal("video with query string must be unsupported")
	}
}

func TestThumbFetchGroupDeduplicatesSameID(t *testing.T) {
	var g thumbFetchGroup
	var calls int32
	// The leader is held until every other goroutine has queued up: an
	// instant return would delete the entry before the waiters arrive.
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = g.Do(42, func() (string, error) {
				atomic.AddInt32(&calls, 1)
				<-release
				return "data/thumbs/42.jpg", nil
			})
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("singleflight leader ran %d times, want 1", got)
	}
	for i, p := range results {
		if p != "data/thumbs/42.jpg" {
			t.Fatalf("result[%d] = %q", i, p)
		}
	}
}

func TestThumbFetchGroupPropagatesError(t *testing.T) {
	var g thumbFetchGroup
	if _, err := g.Do(7, func() (string, error) { return "", errThumbFetchUnsupported }); err == nil {
		t.Fatal("error must be propagated to the caller")
	}
	// The key must be released, otherwise the next call would wait forever.
	if len(g.m) != 0 {
		t.Fatalf("flight group leaked %d entries", len(g.m))
	}
}

func TestThumbMissCacheTTL(t *testing.T) {
	thumbMarkMiss(555)
	if !thumbRecentMiss(555) {
		t.Fatal("fresh miss must be cached")
	}
	thumbClearMiss(555)
	if thumbRecentMiss(555) {
		t.Fatal("cleared miss must not be cached")
	}
	// An expired entry counts as absent and is evicted.
	thumbMisses.mu.Lock()
	thumbMisses.m[556] = time.Now().Add(-2 * thumbMissTTL)
	thumbMisses.mu.Unlock()
	if thumbRecentMiss(556) {
		t.Fatal("expired miss must be treated as absent")
	}
	thumbMisses.mu.Lock()
	_, leaked := thumbMisses.m[556]
	thumbMisses.mu.Unlock()
	if leaked {
		t.Fatal("expired miss must be evicted from the map")
	}
}

func TestDownloadThumbFetchesAndCaches(t *testing.T) {
	var hits int64
	srv := thumbTestServer(t, &hits)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{providers: map[string]Provider{"rule34": newStubProvider(u.Hostname())}}
	tg := NewThumbnailGenerator()
	tg.thumbDir = filepath.Join(t.TempDir(), "thumbs")

	path, err := h.downloadThumb(4242, &Post{PreviewURL: srv.URL + "/preview.jpg"}, tg)
	if err != nil {
		t.Fatalf("downloadThumb: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("thumbnail not written: %v", statErr)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1", got)
	}
}

func TestDownloadThumbRejectsForeignHost(t *testing.T) {
	var hits int64
	srv := thumbTestServer(t, &hits)
	// The provider allows a different host, so the test server is rejected.
	h := &Handler{providers: map[string]Provider{"rule34": newStubProvider("cdn.example")}}
	tg := NewThumbnailGenerator()
	tg.thumbDir = filepath.Join(t.TempDir(), "thumbs")

	post := &Post{PreviewURL: srv.URL + "/preview.jpg"}
	if _, err := h.downloadThumb(1, post, tg); !errors.Is(err, errThumbFetchUnsupported) {
		t.Fatalf("foreign host must be rejected, got %v", err)
	}
	if atomic.LoadInt64(&hits) != 0 {
		t.Fatal("no upstream request expected for a rejected host")
	}
}

// thumbStatusServer returns a test server answering with the given status.
func thumbStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadThumbTreatsOnly404AsPermanent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		perm    bool
		wantErr string
	}{
		{"upstream 404 is permanent", http.StatusNotFound, true, "gone"},
		{"upstream 410 is permanent", http.StatusGone, true, "gone"},
		{"upstream 500 is temporary", http.StatusInternalServerError, false, "status 500"},
		{"upstream 429 is temporary", http.StatusTooManyRequests, false, "status 429"},
		{"upstream 403 is temporary", http.StatusForbidden, false, "status 403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := thumbStatusServer(t, tc.status)
			u, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			h := &Handler{providers: map[string]Provider{"rule34": newStubProvider(u.Hostname())}}
			tg := NewThumbnailGenerator()
			tg.thumbDir = filepath.Join(t.TempDir(), "thumbs")

			// FileURL совпадает с PreviewURL, чтобы fallback не срабатывал.
			post := &Post{PreviewURL: srv.URL + "/p.jpg", FileURL: srv.URL + "/p.jpg"}
			_, err = h.downloadThumb(7, post, tg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
			if got := thumbIsPermanentGone(err); got != tc.perm {
				t.Fatalf("thumbIsPermanentGone = %v, want %v (err=%v)", got, tc.perm, err)
			}
		})
	}
}

func TestThumbFallbackURLPicksOriginalImage(t *testing.T) {
	post := &Post{PreviewURL: "https://cdn.example/p.jpg", FileURL: "https://cdn.example/f.png"}
	if got := thumbFallbackURL(post, post.PreviewURL); got != post.FileURL {
		t.Fatalf("fallback = %q, want %q", got, post.FileURL)
	}
	// Тот же URL повторно не пробуем.
	if got := thumbFallbackURL(post, post.FileURL); got != "" {
		t.Fatalf("same URL must not be retried, got %q", got)
	}
	// Видео оригиналом не подменить — нужен кадр ffmpeg.
	vid := &Post{PreviewURL: "https://cdn.example/p.jpg", FileURL: "https://cdn.example/f.mp4"}
	if got := thumbFallbackURL(vid, vid.PreviewURL); got != "" {
		t.Fatalf("video fallback = %q, want empty", got)
	}
	if got := thumbFallbackURL(nil, "x"); got != "" {
		t.Fatalf("nil post fallback = %q, want empty", got)
	}
}

func TestDownloadThumbFallsBackToOriginalWhenPreviewGone(t *testing.T) {
	var previewHits, originalHits int64
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := 0; x < 32; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, color.RGBA{B: uint8(x * 8), A: 255})
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/thumb") {
			atomic.AddInt64(&previewHits, 1)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt64(&originalHits, 1)
		w.Header().Set("Content-Type", "image/jpeg")
		_ = jpeg.Encode(w, img, nil)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{providers: map[string]Provider{"rule34": newStubProvider(u.Hostname())}}
	tg := NewThumbnailGenerator()
	tg.thumbDir = filepath.Join(t.TempDir(), "thumbs")

	post := &Post{PreviewURL: srv.URL + "/thumb/p.jpg", FileURL: srv.URL + "/images/f.jpg"}
	path, err := h.downloadThumb(11, post, tg)
	if err != nil {
		t.Fatalf("expected the original to be used: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("thumbnail not written: %v", statErr)
	}
	if atomic.LoadInt64(&previewHits) != 1 || atomic.LoadInt64(&originalHits) != 1 {
		t.Fatalf("preview hits=%d original hits=%d, want 1 and 1", previewHits, originalHits)
	}
}

func TestGetThumbReturns503ForTemporaryFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Подставляем singleflight-ошибку: апстрим ответил 5xx.
	thumbFetches.mu.Lock()
	thumbFetches.m = map[int]*thumbCall{}
	cl := &thumbCall{done: make(chan struct{}), err: errors.New("thumb: upstream status 500")}
	thumbFetches.m[424242] = cl
	thumbFetches.mu.Unlock()
	close(cl.done)
	t.Cleanup(func() {
		thumbFetches.mu.Lock()
		thumbFetches.m = map[int]*thumbCall{}
		thumbFetches.mu.Unlock()
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/thumb/424242", nil)
	thumbTestHandler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for a temporary failure", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store so the browser may retry", cc)
	}
}

// thumbTestHandler wires the real GetThumb into a gin router.
func thumbTestHandler() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/thumb/:id", (&Handler{providers: map[string]Provider{}}).GetThumb)
	return r
}

func TestGetThumbReturns404ForUnknownPost(t *testing.T) {
	// No post in the DB and no URL: nothing to fetch, and it must not panic.
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/thumb/987654321", nil)
	thumbTestHandler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestGetThumbServesExistingFile(t *testing.T) {
	dir := t.TempDir()
	thumbs := filepath.Join(dir, "data", "thumbs")
	if err := os.MkdirAll(thumbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thumbs, "123.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/thumb/123", nil)
	thumbTestHandler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "jpeg-bytes" {
		t.Fatalf("body = %q", got)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control = %q, want immutable", cc)
	}
}

func TestGetThumbRejectsInvalidID(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/thumb/not-a-number", nil)
	thumbTestHandler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
