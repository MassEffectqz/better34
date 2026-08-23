package main

import (
	"briefly/internal"
	"compress/gzip"
	"context"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
}

func newRotatingWriter(path string, maxBytes int64) *rotatingWriter {
	w := &rotatingWriter{path: path, maxBytes: maxBytes}
	w.rotate()
	return w
}

func (w *rotatingWriter) rotate() {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	if info, err := os.Stat(w.path); err == nil && info.Size() >= w.maxBytes {
		os.Rename(w.path, w.path+".old")
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	w.file = f
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		w.rotate()
		if w.file == nil {
			return len(p), nil
		}
	}
	n, err := w.file.Write(p)
	if err == nil {
		if info, statErr := w.file.Stat(); statErr == nil && info.Size() >= w.maxBytes {
			w.rotate()
		}
	}
	return n, err
}

func debugMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		if strings.HasPrefix(c.Request.URL.Path, "/api") {
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

func gzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if c.Request.Method != "GET" || !strings.Contains(c.Request.Header.Get("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}
		lower := strings.ToLower(p)
		for _, prefix := range []string{"/api/proxy", "/api/file/", "/api/thumb", "/api/events"} {
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
		gz := gzip.NewWriter(c.Writer)
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Writer = &gzipWriter{ResponseWriter: c.Writer, gz: gz}
		c.Next()
		_ = gz.Close()
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
		if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".css") {
			c.Header("Cache-Control", "no-cache")
		} else {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	}
}

func debugEnabled() bool {
	v := strings.ToLower(os.Getenv("BRIEFLY_DEBUG"))
	return v == "1" || v == "true"
}

func main() {
	internal.SetBriefToken(authToken)
	log.SetOutput(io.MultiWriter(os.Stdout, newRotatingWriter("data/briefly.log", 10<<20)))
	cfg := internal.GetConfig()
	db := internal.GetDB()
	log.Printf("DB loaded: %d posts", db.Stats()["total"])
	downloader := internal.NewDownloader(cfg.GetConcurrentDownloads(), func(result internal.DownloadResult) {
		if result.Error != nil {
			log.Printf("Download failed [%d]: %v", result.PostID, result.Error)
		} else {
			log.Printf("Downloaded [%d] -> %s", result.PostID, result.FilePath)
		}
	})
	handler := internal.NewHandler()
	handler.SetDownloader(downloader)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// За обратным прокси не рассчитан: ClientIP берём из соединения,
	// иначе спуфингом X-Forwarded-For можно обойти rate-limit авторизации.
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())
	r.Use(webSecurityMiddleware())
	r.Use(staticCacheMiddleware())
	r.Use(gzipMiddleware())
	// Построчный лог каждого /api запроса — только при явном BRIEFLY_DEBUG=1.
	if v := strings.ToLower(os.Getenv("BRIEFLY_DEBUG")); v == "1" || v == "true" {
		r.Use(debugMiddleware())
	}
	api := r.Group("/api")
	{
		api.GET("/healthz", handler.Healthz)
		api.POST("/auth/register", handler.AuthRegister)
		api.POST("/auth/login", handler.AuthLogin)
		api.POST("/auth/logout", handler.AuthLogout)
		api.GET("/auth/me", handler.AuthMe)
		api.POST("/profile/meta", handler.UpdateProfileMeta)
		api.GET("/posts", handler.SearchPosts)
		api.GET("/recommend", handler.Recommend)
		api.POST("/recommend/dislike", handler.RecommendDislike)
		api.GET("/posts-by-ids", handler.GetPostsByIDs)
		api.GET("/posts/:id", handler.GetPost)
		api.POST("/download", handler.DownloadMultiple)
		api.POST("/download/:id", handler.DownloadPost)
		api.GET("/file/:id", handler.GetFile)
		api.GET("/save/:id", handler.SaveFile)
		api.GET("/thumb/:id", handler.GetThumb)
		api.GET("/proxy", handler.ProxyRemote)
		api.GET("/local", handler.GetLocalPosts)
		api.GET("/results", handler.DownloadResults)
		api.GET("/download-status", handler.DownloadStatus)
		api.GET("/events", handler.StreamEvents)
		api.POST("/download/pause", handler.DownloadPause)
		api.POST("/download/resume", handler.DownloadResume)
		api.GET("/download/queue", handler.DownloadQueue)
		api.POST("/download/cancel/:id", handler.DownloadCancel)
		api.POST("/download/move-up/:id", handler.DownloadMoveUp)
		api.POST("/download/move-down/:id", handler.DownloadMoveDown)
		api.POST("/rename", handler.BatchRename)
		api.GET("/settings", handler.GetSettings)
		api.POST("/settings", handler.UpdateSettings)
		api.GET("/stats", handler.GetStats)
		api.DELETE("/posts/:id", handler.DeletePost)
		api.GET("/suggest", handler.SuggestTags)
		api.GET("/suggest-local", handler.SuggestLocal)
		api.GET("/tag-counts", handler.GetTagCounts)
		api.GET("/tag-stats", handler.GetLocalTagStats)
		api.GET("/tags/popular", handler.GetPopularTags)
		api.POST("/like/:id", handler.ToggleLike)
		api.POST("/hide/:id", handler.ToggleHide)
		api.GET("/profile", handler.GetProfileData)
		api.POST("/preset", handler.AddPreset)
		api.DELETE("/preset/:id", handler.DeletePreset)
		api.PATCH("/preset/:id", handler.UpdatePreset)
		api.POST("/preset/:id/apply", handler.ApplyPreset)
		api.POST("/preset/:id/move", handler.MovePreset)
		api.POST("/fav-tag", handler.ToggleFavTag)
		api.GET("/presets/export", handler.ExportPresets)
		api.POST("/presets/import", handler.ImportPresets)
		api.POST("/hidden-tag", handler.ToggleHiddenTag)
		api.POST("/fav-tags/clear", handler.ClearFavTags)
		api.POST("/hidden-tags/clear", handler.ClearHiddenTags)
		api.POST("/db/clean", handler.CleanDB)
		api.GET("/random", handler.SearchRandom)
		api.GET("/related", handler.GetRelated)
		api.POST("/download-liked", handler.DownloadLiked)
		api.GET("/files", handler.FindFiles)
		api.GET("/dups", handler.FindDuplicates)
		api.POST("/dups/clean", handler.CleanDuplicates)
	}

	r.Static("/static", "static")
	// Service worker обязан отдаваться с корня (scope /), иначе PWA не работает.
	r.StaticFile("/sw.js", "static/sw.js")
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		serveIndex(c)
	})

	// По умолчанию слушаем все интерфейсы: приложение рассчитано на
	// просмотр с телефона/других устройств локальной сети, доступ
	// защищён экраном входа. Loopback-only — BRIEFLY_HOST=127.0.0.1.
	host := os.Getenv("BRIEFLY_HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port := os.Getenv("BRIEFLY_PORT")
	if port == "" {
		port = "3000"
	}
	addr := net.JoinHostPort(host, port)

	if host != "127.0.0.1" && host != "localhost" {
		log.Printf("Сервер доступен в локальной сети (вход по логину/паролю). " +
			"Ограничить: BRIEFLY_HOST=127.0.0.1, список хостов BRIEFLY_ALLOWED_HOSTS или файрвол.")
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
		// Медленные заголовки — классический Slowloris. ReadTimeout/WriteTimeout
		// намеренно не ставим: SSE (/api/events) и прокси-стриминг долгоживущие.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Server starting on http://%s", addr)
		for _, a := range allowedHosts {
			if a != "localhost" && a != "127.0.0.1" && a != "::1" && !strings.Contains(a, ":") {
				log.Printf("  в локальной сети: http://%s:%s", a, port)
			}
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()
	<-quit
	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	downloader.Close()
	db.Close()
	log.Println("Done.")
}

var allowedHosts = func() []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	extra := os.Getenv("BRIEFLY_ALLOWED_HOSTS")
	if extra == "" {
		// Список не задан — разрешаем адреса всех сетевых интерфейсов
		// машины: доступ с телефона/другого ПК по IP хоста работает
		// без ручных настроек. Явный BRIEFLY_ALLOWED_HOSTS сужает круг.
		ifaces, err := net.Interfaces()
		if err != nil {
			return hosts
		}
		for _, ifc := range ifaces {
			if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := ifc.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
					continue
				}
				s := ip.String()
				if !slices.Contains(hosts, s) {
					hosts = append(hosts, s)
				}
			}
		}
		return hosts
	}
	for _, h := range strings.Split(extra, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}()

var authToken = strings.TrimSpace(os.Getenv("BRIEFLY_TOKEN"))

func hostAllowed(r *http.Request) bool {
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
	h = strings.TrimSuffix(h, ".")
	for _, a := range allowedHosts {
		if h == a {
			return true
		}
	}
	return false
}

func tokenMatches(c *gin.Context) bool {
	if internal.TokenMatches(c) {
		return true
	}
	return authToken == ""
}

func webSecurityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hostAllowed(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "host not allowed"})
			return
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
				c.Request.URL.Path == "/api/tags/popular" {
				c.Next()
				return
			}
			accs := internal.GetAccounts()
			if accs.Count() == 0 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "не создано ни одного аккаунта — зарегистрируйте первый через /auth/register"})
				return
			}
			if cookie, err := c.Cookie(internal.SessionCookieName); err == nil {
				if u, ok := accs.UserBySession(cookie); ok {
					c.Set("briefly_user", u)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "login required"})
			return
		}
		c.Next()
	}
}

func sameOriginHost(r *http.Request) string {
	scheme := "http"
	if v := r.Header.Get("X-Forwarded-Proto"); v == "https" {
		scheme = v
	}
	return scheme + "://" + r.Host
}

var (
	indexHTML  []byte
	indexOnce  sync.Once
	indexError error
	versionMu  sync.Mutex
	versionVal string
	lastWalk   time.Time
)

func staticVersion() string {
	versionMu.Lock()
	defer versionMu.Unlock()
	// В проде версию достаточно пересчитывать раз в 30с: walk по дереву
	// static/ на каждый запрос главной не нужен. BRIEFLY_DEBUG=1 —
	// пересчёт на каждый вызов, чтобы правки фронтенда подхватывались сразу.
	ttl := 30 * time.Second
	if debugEnabled() {
		ttl = 0
	}
	if versionVal != "" && time.Since(lastWalk) < ttl {
		return versionVal
	}
	lastWalk = time.Now()
	var latest int64
	filepath.Walk("static", func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().UnixNano() > latest {
			latest = info.ModTime().UnixNano()
		}
		return nil
	})
	v := strconv.FormatInt(latest, 16)
	if v != versionVal {
		versionVal = v
		indexOnce = sync.Once{}
		indexHTML, indexError = os.ReadFile("static/index.html")
	}
	return v
}

func loadIndex() ([]byte, error) {
	staticVersion()
	versionMu.Lock()
	defer versionMu.Unlock()
	return indexHTML, indexError
}

func serveIndex(c *gin.Context) {
	html, err := loadIndex()
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html missing")
		return
	}
	v := staticVersion()
	out := strings.ReplaceAll(string(html), "__VERSION__", v)
	out = strings.ReplaceAll(out, "__BRIEFLY_TOKEN__", authToken)
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(out))
}
