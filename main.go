package main

import (
	"briefly/internal"
	"context"
	"crypto/tls"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	loadDotEnv()
	slog.SetDefault(newSlogLogger())
	internal.SetBriefToken(authToken)
	log.SetOutput(logOutput)
	cfg := internal.GetConfig()
	db := internal.GetDB()
	slog.Info("db loaded", "posts", db.Stats()["total"])
	downloader := internal.NewDownloader(cfg.GetConcurrentDownloads(), func(result internal.DownloadResult) {
		if result.Error != nil {
			slog.Error("download failed", "post_id", result.PostID, "error", result.Error.Error())
		} else {
			slog.Info("downloaded", "post_id", result.PostID, "file", result.FilePath)
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
		api.GET("/ready", handler.Readiness)
		api.GET("/metrics", handler.Metrics)
		api.POST("/auth/register", handler.AuthRegister)
		api.POST("/auth/password", handler.AuthChangePassword)
		api.POST("/auth/logout-others", handler.AuthLogoutOthers)
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
		api.GET("/tag-aliases", handler.ListTagAliases)
		api.POST("/tag-alias", handler.AddTagAlias)
		api.DELETE("/tag-alias/:alias", handler.DeleteTagAlias)
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
		api.POST("/view/:id", handler.RecordView)
		api.POST("/view/:id/forget", handler.ForgetView)
		api.POST("/views", handler.RecordViews)
		api.POST("/posts/:id/parent", handler.SetPostParent)
		api.GET("/posts/:id/relations", handler.PostRelations)
		api.POST("/remote/push", handler.RemotePush)
		api.GET("/comments/:id", handler.GetComments)
		api.POST("/comments/:id", handler.AddComment)
		api.DELETE("/comments/:cid", handler.DeleteComment)
		api.POST("/download-liked", handler.DownloadLiked)
		api.GET("/files", handler.FindFiles)
		api.GET("/download-zip", handler.DownloadZip)
		api.GET("/dups", handler.FindDuplicates)
		api.POST("/dups/clean", handler.CleanDuplicates)
		api.POST("/dups/merge", handler.MergeDuplicates)
		api.GET("/collections", handler.ListCollections)
		api.POST("/collection", handler.CreateCollection)
		api.PATCH("/collection/:id", handler.RenameCollection)
		api.DELETE("/collection/:id", handler.DeleteCollection)
		api.POST("/collection/:id/post", handler.CollectionTogglePost)
		api.POST("/collection/:id/posts", handler.CollectionAddMany)
		api.GET("/collection/:id/posts", handler.CollectionPosts)
		api.POST("/batch/like", handler.BatchLike)
		api.POST("/batch/hide", handler.BatchHide)
		api.GET("/profile/export", handler.ExportProfile)
		api.POST("/profile/import", handler.ImportProfile)
		api.GET("/user-stats", handler.GetUserStats)
		api.GET("/booru/posts", handler.BooruPosts)
		api.GET("/booru/posts.json", handler.BooruPosts)
		api.GET("/booru/tags", handler.BooruTags)
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
		slog.Info("сервер доступен в локальной сети (вход по логину/паролю). " +
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
	scheme, rawLn, plainSrv, h3Server := setupTLS(addr)
	if scheme == "https" && rawLn != nil {
		cert, _ := loadOrGenerateCert()
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		}
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		slog.Info("server starting", "scheme", scheme, "addr", addr)
		if scheme == "https" {
			slog.Info("самоподписанный сертификат: при первом открытии браузер предупредит — примите исключение (на телефоне: «Дополнительно» → «Перейти на сайт»)")
			slog.Info("http:// на тот же порт автоматически перенаправляется на https (старые ссылки работают)")
		}
		for _, a := range allowedHosts {
			if a != "localhost" && a != "127.0.0.1" && a != "::1" && !strings.Contains(a, ":") {
				slog.Info("в локальной сети", "url", scheme+"://"+a+":"+port)
			}
		}
		var err error
		switch {
		case scheme == "https":
			// Пустые пути — сертификат берётся из srv.TLSConfig.
			err = srv.ServeTLS(rawLn, "", "")
		default:
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed && !errors.Is(err, net.ErrClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	}()
	<-quit
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("server forced to shutdown", "error", err)
	}
	if plainSrv != nil {
		_ = plainSrv.Shutdown(ctx)
	}
	if h3Server != nil {
		_ = h3Server.Close()
	}
	downloader.Close()
	db.Close()
	slog.Info("done")
}
