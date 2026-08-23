package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
