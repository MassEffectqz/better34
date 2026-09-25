package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"briefly/internal"
)

func TestHostAllowed(t *testing.T) {
	orig := allowedHosts
	allowedHosts = []string{"localhost", "127.0.0.1", "::1"}
	defer func() { allowedHosts = orig }()

	cases := []struct {
		host string
		want bool
	}{
		{"localhost:3000", true},
		{"localhost", true},
		{"127.0.0.1:3000", true},
		{"[::1]:3000", true},
		{"localhost.", true},
		{"example.com:3000", false},
		{"localhost.evil.com:3000", false},
		{"192.168.1.10:3000", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tc.host
		if got := hostAllowed(req); got != tc.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestHostAllowedWildcard(t *testing.T) {
	orig := allowedHosts
	allowedHosts = []string{".trycloudflare.com"}
	defer func() { allowedHosts = orig }()

	cases := []struct {
		host string
		want bool
	}{
		{"match-track-eau-evaluating.trycloudflare.com", true},
		{"sub.trycloudflare.com:443", true},
		{"trycloudflare.com", true},
		{"trycloudflare.com:3000", true},
		{"eviltunnel.trycloudflare.com.evil.com", false},
		{"nottrycloudflare.com", false},
		{"localhost.evil.com:3000", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tc.host
		if got := hostAllowed(req); got != tc.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestTokenMatches(t *testing.T) {
	orig := authToken
	authToken = "secret"
	internal.SetBriefToken("secret") // main() вызывает SetBriefToken — в тестах делаем то же
	defer func() {
		authToken = orig
		internal.SetBriefToken("")
	}()

	ok := func(tag string, headers func(r *http.Request)) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		if headers != nil {
			headers(c.Request)
		}
		if !tokenMatches(c) {
			t.Errorf("%s: expected token to match", tag)
		}
	}
	fail := func(tag string, headers func(r *http.Request)) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		if headers != nil {
			headers(c.Request)
		}
		if tokenMatches(c) {
			t.Errorf("%s: expected token to mismatch", tag)
		}
	}

	ok("X-Briefly-Token", func(r *http.Request) { r.Header.Set("X-Briefly-Token", "secret") })
	ok("Bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") })
	fail("wrong header", func(r *http.Request) { r.Header.Set("X-Briefly-Token", "nope") })
	fail("wrong bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") })
	fail("no token", nil)
}

func TestIsMediaPathSeparatesMediaFromLogic(t *testing.T) {
	for _, p := range []string{
		"/api/thumb/123", "/api/file/9", "/api/proxy", "/api/proxy?url=x&kind=preview",
	} {
		if !isMediaPath(p) {
			t.Errorf("isMediaPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"/api/posts", "/api/profile", "/api/like/1", "/api/download", "/api/thumbs",
		"/api/healthz", "/",
	} {
		if isMediaPath(p) {
			t.Errorf("isMediaPath(%q) = true, want false", p)
		}
	}
}

func TestMediaLimiterToleratesGridBurst(t *testing.T) {
	// Сетка лайков дергает сотни миниатюр разом. Общий лимит 120/60 req/s
	// превращал это в каскад 429 — отсюда битые превью в профиле.
	mediaLimiter.buckets = make(map[string]*tokenBucket)
	mediaLimiter.lastPrune = time.Now()
	apiLimiter.buckets = make(map[string]*tokenBucket)
	apiLimiter.lastPrune = time.Now()

	const ip = "203.0.113.7"
	for i := 0; i < 400; i++ {
		if !mediaLimiter.allow(ip) {
			t.Fatalf("media request %d was rate limited; grid bursts must pass", i)
		}
	}
	// Логика приложения при этом троттлится как раньше.
	limited := 0
	for i := 0; i < 400; i++ {
		if !apiLimiter.allow(ip) {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("api limiter must still throttle non-media requests")
	}
}

func TestWebSecurityMiddleware(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	origToken := authToken
	authToken = "secret"
	internal.SetBriefToken("secret") // как в main(): BRIEFLY_TOKEN задаёт legacy-токен
	defer func() {
		authToken = origToken
		internal.SetBriefToken("")
	}()

	withToken := func(r *http.Request) { r.Header.Set("X-Briefly-Token", "secret") }

	r := gin.New()
	r.Use(webSecurityMiddleware())
	r.GET("/api/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "index") })

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.Host = "localhost:3000"
		withToken(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("loopback request: got %d, want 200", w.Code)
		}
	}

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "attacker.com:3000"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("foreign host: got %d, want 403", w.Code)
		}
	}

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.Host = "localhost:3000"
		req.Header.Set("Origin", "http://evil.example")
		withToken(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("cross-origin: got %d, want 403", w.Code)
		}
	}

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.Host = "localhost:3000"
		req.Header.Set("Origin", "http://localhost:3000")
		withToken(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("same-origin: got %d, want 200", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
			t.Errorf("ACAO = %q, want origin echo", got)
		}
	}

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.Host = "localhost:3000"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("missing token: got %d, want 401", w.Code)
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/ping", nil)
		req.Host = "localhost:3000"
		req.Header.Set("X-Briefly-Token", "secret")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("with token: got %d, want 200", w.Code)
		}
	}

	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "localhost:3000"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("static without token: got %d, want 200", w.Code)
		}
	}
}

// resolveLogDir: приоритет BRIEFLY_LOG_DIR, иначе data/logs.
func TestResolveLogDir(t *testing.T) {
	t.Setenv("BRIEFLY_LOG_DIR", "")

	logDirOnce = sync.Once{}
	logDir = ""
	got := resolveLogDir()
	if got != "data/logs" {
		t.Errorf("resolveLogDir() = %q, want data/logs", got)
	}

	logDirOnce = sync.Once{}
	logDir = ""
	t.Setenv("BRIEFLY_LOG_DIR", "X:/custom-log-dir")
	if got := resolveLogDir(); got != "X:/custom-log-dir" {
		t.Errorf("resolveLogDir() = %q, want X:/custom-log-dir", got)
	}

	logDirOnce = sync.Once{}
	logDir = ""
	t.Setenv("BRIEFLY_LOG_DIR", "  ") // пустой после TrimSpace → дефолт
	if got := resolveLogDir(); got != "data/logs" {
		t.Errorf("resolveLogDir(whitespace) = %q, want data/logs", got)
	}
}

// Папка лога создаётся заранее, даже если вся иерархия отсутствует;
// старый лог в data/ (до разделения) не читается заново.
func TestRotatingWriterCreatesLogDir(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "lvl1", "lvl2", "briefly.log")

	w := newRotatingWriter(logPath, 10<<20)
	defer func() {
		if w.file != nil {
			w.file.Close()
		}
	}()

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("лог-файл не создан: %v", err)
	}
	if st.Size() == 0 {
		t.Error("лог-файл пуст после записи")
	}
}
