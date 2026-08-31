package main

import (
	"briefly/internal"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/quic-go/quic-go/http3"
	"io"
	"log"
	"math/big"
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

// tlsEnabled включает HTTPS + HTTP/2: браузеры открывают HTTP/2 только
// поверх TLS, а без него лимит 6 соединений на хост сериализует ленту
// из десятков миниатюр. Сертификат self-signed (BRIEFLY_TLS=1).
func tlsEnabled() bool {
	v := strings.ToLower(os.Getenv("BRIEFLY_TLS"))
	return v == "1" || v == "true" || v == "yes"
}

const (
	// Слэши намеренно: пакет os в Go принимает "/" и на Windows.
	tlsCertPath = "data/tls/cert.pem"
	tlsKeyPath  = "data/tls/key.pem"
)

// loadOrGenerateCert возвращает self-signed сертификат, покрывающий все
// разрешённые хосты (allowedHosts). Сертификат сохраняется в data/tls и
// переиспользуется: постоянный отпечаток означает, что браузер (особенно
// на телефоне) принимает предупреждение о самоподписанном сертификате
// один раз, а не после каждого перезапуска сервера.
func loadOrGenerateCert() (tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath); err == nil {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "briefly-local"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range allowedHosts {
		if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	var certPEM, keyPEM bytes.Buffer
	if err := pem.Encode(&certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := pem.Encode(&keyPEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(tlsCertPath), 0700); err == nil {
		// Ошибки записи не критичны: сертификат просто перегенерируется
		// при следующем запуске.
		_ = os.WriteFile(tlsCertPath, certPEM.Bytes(), 0644)
		_ = os.WriteFile(tlsKeyPath, keyPEM.Bytes(), 0600)
	}
	return tls.X509KeyPair(certPEM.Bytes(), keyPEM.Bytes())
}

// ── Один порт: TLS и plain HTTP одновременно ─────────────────────────────
// Порт один, а клиенты бывают разные: телефон со старой закладкой/PWA
// стучится по http:// и получает загадочную ошибку TLS handshake. Поэтому
// первый байт соединения подглядывается: 0x16 (TLS ClientHello) — в
// HTTPS-сервер (с HTTP/2), любой другой — в крошечный редиректор на https.
// Старые http-ссылки продолжают работать сами собой.

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(addr net.Addr, buf int) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn, buf), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// demuxAccept раскладывает соединения основного листенера по двум
// каналам-листенерам. Дедлайн на Peek обязателен: молчащее соединение
// (сканер, зависший клиент) иначе навсегда блокировало бы accept-цикл.
func demuxAccept(ln net.Listener, tlsLn, plainLn *chanListener) {
	defer ln.Close()
	defer tlsLn.Close()
	defer plainLn.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		br := bufio.NewReader(conn)
		first, perr := br.Peek(1)
		_ = conn.SetReadDeadline(time.Time{})
		pc := &peekConn{Conn: conn, r: br}
		if perr == nil && first[0] == 0x16 {
			tlsLn.ch <- pc
		} else {
			// Не TLS (включая обрывы): редиректор ответит или закроет сам.
			plainLn.ch <- pc
		}
	}
}

// altSvcMiddleware рекламирует HTTP/3 на том же порту: современные браузеры
// по Alt-Svc сами переключаются на QUIC, когда сеть его поддерживает.
func altSvcMiddleware() gin.HandlerFunc {
	h3Active := strings.ToLower(strings.TrimSpace(os.Getenv("BRIEFLY_H3"))) != "0"
	if !tlsEnabled() || !h3Active {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if c.Request.TLS != nil {
			c.Header("Alt-Svc", `h3=":`+os.Getenv("BRIEFLY_PORT")+`"; ma=86400`)
		}
		c.Next()
	}
}

// httpsRedirectHandler отправляет plain HTTP на тот же хост/порт по https.
// 307 (временный) — чтобы браузер не «залипал» на редиректе, если TLS
// потом отключат.
func httpsRedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
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
	r.Use(bodyLimitMiddleware())
	r.Use(staticCacheMiddleware())
	r.Use(gzipMiddleware())
	// Задача 10: рекламируем HTTP/3 (если сервер реально его слушает).
	r.Use(altSvcMiddleware())
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
		api.GET("/auth/qr/create", handler.QRCreate)
		api.GET("/auth/qr/svg", handler.QRSVG)
		api.POST("/auth/qr/claim", handler.QRClaim)
		api.POST("/auth/qr/session", handler.QRCreateSession)
		api.GET("/auth/qr/poll", handler.QRPollSession)
		api.POST("/auth/qr/exchange", handler.QRExchangeSession)
		api.GET("/auth/qr/image", handler.QRImagePublic)
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
		api.GET("/nl-search", handler.NlSearch)
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
		api.GET("/similar/:id", handler.GetSimilar)
		api.POST("/remote/push", handler.RemotePush)
		api.GET("/comments/:id", handler.GetComments)
		api.POST("/comments/:id", handler.AddComment)
		api.DELETE("/comments/:cid", handler.DeleteComment)
		api.POST("/download-liked", handler.DownloadLiked)
		api.GET("/files", handler.FindFiles)
		api.GET("/download-zip", handler.DownloadZip)
		api.GET("/dups", handler.FindDuplicates)
		api.POST("/dups/clean", handler.CleanDuplicates)
		api.GET("/collections", handler.ListCollections)
		api.POST("/collection", handler.CreateCollection)
		api.PATCH("/collection/:id", handler.RenameCollection)
		api.DELETE("/collection/:id", handler.DeleteCollection)
		api.POST("/collection/:id/post", handler.CollectionTogglePost)
		api.GET("/collection/:id/posts", handler.CollectionPosts)
		api.GET("/profile/export", handler.ExportProfile)
		api.POST("/profile/import", handler.ImportProfile)
		api.GET("/user-stats", handler.GetUserStats)
	}

	r.Static("/static", "static")
	// Service worker обязан отдаваться с корня (scope /), иначе PWA не работает.
	r.StaticFile("/sw.js", "static/sw.js")
	r.GET("/qr", handler.QRPage)
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
	scheme := "http"
	var rawLn net.Listener
	var tlsLn, plainLn *chanListener
	var plainSrv *http.Server
	var h3Server *http3.Server
	if tlsEnabled() {
		cert, err := loadOrGenerateCert()
		if err != nil {
			log.Printf("BRIEFLY_TLS: не удалось подготовить сертификат (%v) — работаю по HTTP", err)
		} else {
			srv.TLSConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
				NextProtos:   []string{"h2", "http/1.1"},
			}
			scheme = "https"
			rawLn, err = net.Listen("tcp", addr)
			if err != nil {
				log.Fatalf("Server failed: %v", err)
			}
			tlsLn = newChanListener(rawLn.Addr(), 64)
			plainLn = newChanListener(rawLn.Addr(), 64)
			go demuxAccept(rawLn, tlsLn, plainLn)
			plainSrv = &http.Server{
				Handler:           httpsRedirectHandler(),
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       120 * time.Second,
			}
			go func() {
				// Ошибки редиректора (в т.ч. остановка) не критичны.
				_ = plainSrv.Serve(plainLn)
			}()
			// Задача 10: HTTP/3 (QUIC) — отдельный UDP-сокет на том же порту.
			// Браузер видит Alt-Svc и сам уходит на h3, если сеть позволяет.
			if strings.ToLower(strings.TrimSpace(os.Getenv("BRIEFLY_H3"))) != "0" {
				h3Server = &http3.Server{
					Addr:    addr,
					Handler: r,
					TLSConfig: &tls.Config{
						MinVersion: tls.VersionTLS12,
						NextProtos: []string{"h3"},
					},
				}
				go func() {
					if err := h3Server.ListenAndServeTLS(tlsCertPath, tlsKeyPath); err != nil &&
						!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
						log.Printf("HTTP/3: %v (QUIC недоступен — продолжаем по TCP)", err)
					}
				}()
			}
		}
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		log.Printf("Server starting on %s://%s", scheme, addr)
		if scheme == "https" {
			log.Printf("  самоподписанный сертификат: при первом открытии браузер предупредит — примите исключение (на телефоне: «Дополнительно» → «Перейти на сайт»)")
			log.Printf("  http:// на тот же порт автоматически перенаправляется на https (старые ссылки работают)")
		}
		for _, a := range allowedHosts {
			if a != "localhost" && a != "127.0.0.1" && a != "::1" && !strings.Contains(a, ":") {
				log.Printf("  в локальной сети: %s://%s:%s", scheme, a, port)
			}
		}
		var err error
		switch {
		case scheme == "https":
			// Пустые пути — сертификат берётся из srv.TLSConfig.
			err = srv.ServeTLS(tlsLn, "", "")
		default:
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
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
	if plainSrv != nil {
		_ = plainSrv.Shutdown(ctx)
	}
	if h3Server != nil {
		_ = h3Server.Close()
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
	if r.TLS != nil {
		scheme = "https"
	} else if v := r.Header.Get("X-Forwarded-Proto"); v == "https" {
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
	// distBundleOK — собран ли esbuild-бандл (static/js/dist/app.js).
	// Обновляется вместе с перечитыванием index.html: пересборка бандла
	// меняет mtime дерева static/ и, значит, версию.
	distBundleOK bool
	// distAppJSPath — точка входа фронтенда в собранном виде.
	distAppJSPath = filepath.Join("static", "js", "dist", "app.js")
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
		_, distErr := os.Stat(distAppJSPath)
		distBundleOK = distErr == nil
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
	out := string(html)
	// Без BRIEFLY_DEBUG отдаём собранный esbuild-бандл вместо графа из
	// ~16 ES-модулей: один запрос вместо каскада 304-ревалидаций (на
	// мобильном интернете/слабом Wi-Fi это секунды до старта приложения).
	if distBundleOK && !debugEnabled() {
		out = strings.Replace(out,
			`<script type="module" src="/static/js/app.js?v=__VERSION__"></script>`,
			`<script type="module" src="/static/js/dist/app.js?v=__VERSION__"></script>`, 1)
	}
	out = strings.ReplaceAll(out, "__VERSION__", v)
	// Legacy-токен (BRIEFLY_TOKEN) сознательно НЕ вшивается в HTML:
	// страница отдаётся без проверки сессии, и любой посетитель LAN
	// видел бы секрет, полностью обходящий аккаунты. Токен-режим
	// работает только для клиентов, передающих заголовок сами.
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(out))
}
