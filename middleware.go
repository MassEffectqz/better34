package main

import (
	"briefly/internal"
	"compress/gzip"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
)

func debugMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			// Path без query: в query может приехать секрет (?token=).
			log.Printf("REQUEST: %s %s -> %s", c.Request.Method, c.Request.URL.Path, c.FullPath())
		}
		c.Next()
	}
}

type gzipWriter struct {
	gin.ResponseWriter
	gz *gzip.Writer
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	g.Header().Del("Content-Length")
	return g.gz.Write(p)
}

func (g *gzipWriter) WriteString(s string) (int, error) {
	return g.Write([]byte(s))
}

func (g *gzipWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Flush() {
	_ = g.gz.Flush()
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type brotliWriter struct {
	gin.ResponseWriter
	bw *brotli.Writer
}

func (b *brotliWriter) Write(p []byte) (int, error) {
	b.Header().Del("Content-Length")
	return b.bw.Write(p)
}

func (b *brotliWriter) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}

func (b *brotliWriter) WriteHeader(code int) {
	b.Header().Del("Content-Length")
	b.ResponseWriter.WriteHeader(code)
}

func (b *brotliWriter) Flush() {
	_ = b.bw.Flush()
	if f, ok := b.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// gzipMiddleware сжимает текстовые ответы: для статики и HTML — brotli
// (клиенты, не умеющие br, получают gzip), для /api/* — gzip на уровне
// BestSpeed: JSON-ответы идут через прокси на каждый запрос, там важнее
// минимальная CPU-задержка, чем максимальная степень сжатия.
func gzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if c.Request.Method != "GET" {
			c.Next()
			return
		}
		lower := strings.ToLower(p)
		for _, prefix := range []string{"/api/proxy", "/api/file/", "/api/thumb", "/api/events", "/api/download-zip"} {
			if strings.HasPrefix(lower, prefix) {
				c.Next()
				return
			}
		}
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif", ".svg", ".mp4", ".webm", ".mov", ".avi", ".mkv", ".mp3", ".zip"} {
			if strings.HasSuffix(lower, ext) {
				c.Next()
				return
			}
		}
		ace := c.Request.Header.Get("Accept-Encoding")
		isAPI := strings.HasPrefix(lower, "/api/")
		switch {
		case !isAPI && strings.Contains(ace, "br"):
			bw := brotli.NewWriterLevel(c.Writer, 6)
			c.Header("Content-Encoding", "br")
			c.Header("Vary", "Accept-Encoding")
			c.Writer = &brotliWriter{ResponseWriter: c.Writer, bw: bw}
			c.Next()
			_ = bw.Close()
		case strings.Contains(ace, "gzip"):
			level := gzip.DefaultCompression
			if isAPI {
				level = gzip.BestSpeed
			}
			gz, err := gzip.NewWriterLevel(c.Writer, level)
			if err != nil {
				c.Next()
				return
			}
			c.Header("Content-Encoding", "gzip")
			c.Header("Vary", "Accept-Encoding")
			c.Writer = &gzipWriter{ResponseWriter: c.Writer, gz: gz}
			c.Next()
			_ = gz.Close()
		default:
			c.Next()
		}
	}
}

func staticCacheMiddleware() func(c *gin.Context) {
	return func(c *gin.Context) {
		if c.Request.Method != "GET" || !strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Next()
			return
		}
		lower := strings.ToLower(c.Request.URL.Path)
		// JS-модули импортируются по фиксированным URL (?v= есть только у
		// точки входа) — им ревалидация по Last-Modified (304), остальной
		// статике с версионированными ссылками — длинный immutable-кэш.
		// Сборка esbuild (static/js/dist/) всегда версионируется ?v= у
		// точки входа — ей тоже immutable.
		if strings.Contains(lower, "/dist/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".css") {
			c.Header("Cache-Control", "no-cache")
		} else {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	}
}

// maxBodyBytes — верхняя граница тела JSON-запросов к /api: без лимита
// неавторизованный клиент читает в память произвольные объёмы (DoS).
// Запас взят под самый крупный payload — импорт профиля и base64-аватар.
const maxBodyBytes = 16 << 20

func bodyLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			c.Next()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
		}
		c.Next()
	}
}

func debugEnabled() bool {
	v := strings.ToLower(os.Getenv("BRIEFLY_DEBUG"))
	return v == "1" || v == "true"
}

func webSecurityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hostAllowed(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "host not allowed"})
			return
		}
		// Стандартные security-заголовки на всех ответах: медиа проксируется,
		// поэтому nosniff/реферал/фреймы/браузерные API ограничиваем глобально.
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if origin := c.GetHeader("Origin"); origin != "" {
			expected := sameOriginHost(c.Request)
			c.Header("Vary", "Origin")
			if origin != expected {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin request blocked"})
				return
			}
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Briefly-Token")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		legacyMode := authToken != "" && tokenMatches(c)
		if strings.HasPrefix(c.Request.URL.Path, "/api") && !legacyMode {
			if strings.HasPrefix(c.Request.URL.Path, "/api/auth/") ||
				strings.HasPrefix(c.Request.URL.Path, "/api/healthz") ||
				c.Request.URL.Path == "/api/tags/popular" ||
				c.Request.URL.Path == "/api/ready" {
				c.Next()
				return
			}
			accs := internal.GetAccounts()
			if accs.Count() == 0 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no_accounts", "message": "no accounts registered yet"})
				return
			}
			if cookie, err := c.Cookie(internal.SessionCookieName); err == nil {
				if u, ok := accs.UserBySession(cookie); ok {
					c.Set("briefly_user", u)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "auth_required", "message": "login required"})
			return
		}
		c.Next()
	}
}

func sameOriginHost(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if v := r.Header.Get("X-Forwarded-Proto"); v == "https" {
		scheme = v
	}
	return scheme + "://" + r.Host
}
