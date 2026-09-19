package main

import (
	"briefly/internal"
	"compress/gzip"
	"fmt"
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

// compressedWriter — общая обёртка сжатого ответа для gzip и brotli:
// дублирующиеся gzipWriter/brotliWriter (Write/WriteString/WriteHeader/Flush)
// сведены к одному типу с делегированием через функции (P2-13).
type compressedWriter struct {
	gin.ResponseWriter
	write func([]byte) (int, error)
	flush func() error
}

func (w *compressedWriter) Write(p []byte) (int, error) {
	w.Header().Del("Content-Length")
	return w.write(p)
}

func (w *compressedWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *compressedWriter) WriteHeader(code int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(code)
}

func (w *compressedWriter) Flush() {
	_ = w.flush()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
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
			c.Writer = &compressedWriter{
				ResponseWriter: c.Writer,
				write: func(p []byte) (int, error) { return bw.Write(p) },
				flush: func() error { return bw.Flush() },
			}
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
			c.Writer = &compressedWriter{
				ResponseWriter: c.Writer,
				write: func(p []byte) (int, error) { return gz.Write(p) },
				flush: func() error { return gz.Flush() },
			}
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
			// no-cache -> must-revalidate: тот же RTT валидации, но при 304
			// тело не ретранслируется (ETag ниже).

			c.Header("Cache-Control", "max-age=0, must-revalidate")
		} else {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		// Предсжатый бандл (.br/.gz от сборки) отдаётся файлом: gzip-middleware
		// пропускается через Abort; HTML и несобранные модули сжимает как раньше.

		if strings.HasPrefix(lower, "/static/js/dist/") {
			ace := c.Request.Header.Get("Accept-Encoding")
			if strings.Contains(ace, "br") && staticPrecompressedReady(c, ".br") {
				servePrecompressed(c, lower, ".br", "br")
				return
			}
			if strings.Contains(ace, "gzip") && staticPrecompressedReady(c, ".gz") {
				servePrecompressed(c, lower, ".gz", "gzip")
				return
			}
		}
		// Несобранные модули: слабый ETag от mtime+size, при If-None-Match —
		// 304 без ретрансляции тела.

		if !strings.Contains(lower, "/dist/") && (strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".css")) &&
			c.Request.Method == http.MethodGet && c.Request.Header.Get("Range") == "" {
			if info, err := os.Stat("static" + c.Request.URL.Path); err == nil && !info.IsDir() {
				etag := fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size())
				c.Header("ETag", etag)
				if inm := c.Request.Header.Get("If-None-Match"); inm != "" && weakETagMatch(inm, etag) {
					c.AbortWithStatus(http.StatusNotModified)
					return
				}
			}
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

// staticPrecompressedReady — существует ли предсжатая версия файла.

func staticPrecompressedReady(c *gin.Context, suffix string) bool {
	_, err := os.Stat("static" + c.Request.URL.Path + suffix)
	return err == nil
}

// servePrecompressed отдаёт предсжатый файл с нужными заголовками.

func servePrecompressed(c *gin.Context, lower, suffix, enc string) {
	c.Header("Content-Encoding", enc)
	c.Header("Vary", "Accept-Encoding")
	if strings.HasSuffix(lower, ".css") {
		c.Header("Content-Type", "text/css; charset=utf-8")
	} else {
		c.Header("Content-Type", "application/javascript; charset=utf-8")
	}
	c.File("static" + c.Request.URL.Path + suffix)
	c.Abort()
}

// weakETagMatch сравнивает If-None-Match (список через запятую) с нашим
// слабым ETag, игнорируя префикс W/.

func weakETagMatch(header, etag string) bool {
	norm := func(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "W/") }
	etag = norm(etag)
	for _, part := range strings.Split(header, ",") {
		if norm(part) == etag || strings.TrimSpace(part) == "*" {
			return true
		}
	}
	return false
}

func debugEnabled() bool {
	v := strings.ToLower(os.Getenv("BRIEFLY_DEBUG"))
	return v == "1" || v == "true"
}

// forceHTTPSEnabled — опциональный режим S-4: при BRIEFLY_FORCE_HTTPS=1
// сервер рекламирует HSTS (только по TLS), давая понять клиентам, что
// HTTPS обязателен. Редирект http→https на этом же порту и так включён
// (tls.go), отдельно — для reverse-proxy сценариев сервер не трогает.
func forceHTTPSEnabled() bool {
	v := strings.ToLower(os.Getenv("BRIEFLY_FORCE_HTTPS"))
	return v == "1" || v == "true" || v == "yes"
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
		// S-3: CSP внедряем поэтапно, начиная с Report-Only в debug-режиме:
		// политику не блокирует ничего, но нарушения видны в консоли браузера
		// и в сетевом трафике — так собираем whitelist перед ужесточением.
		if debugEnabled() {
			c.Header("Content-Security-Policy-Report-Only",
				"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
				"img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; " +
				"font-src 'self'; worker-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
		}
		// S-4: при BRIEFLY_FORCE_HTTPS=1 — HSTS для отвеченных по TLS запросов.
		if forceHTTPSEnabled() && c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
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
