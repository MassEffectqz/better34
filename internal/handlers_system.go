package internal

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Readiness — GET /api/ready: для Docker/K8s, проверяет только БД
// (liveness уже есть в /api/healthz). Без авторизации: статус, не данные.
func (h *Handler) Readiness(c *gin.Context) {
	if err := GetDB().Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "uptime": int(time.Since(startedAt).Seconds())})
}

// Metrics — GET /api/metrics: лёгкая самодиагностика (очереди, SSE, кэши).
// Помощь при мониторинге; за авторизацией как остальные /api/*.
func (h *Handler) Metrics(c *gin.Context) {
	m := gin.H{
		"uptime":          int(time.Since(startedAt).Seconds()),
		"providers":       len(h.providers),
		"sse_subscribers": sseSubscriberCount(),
	}
	if h.downloader != nil {
		q, a, d := h.downloader.Status()
		m["download_queued"] = q
		m["download_active"] = a
		m["download_done"] = d
	}
	if entries, err := os.ReadDir(mediaCacheDir); err == nil {
		var size int64
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				continue
			}
			if fi, err := e.Info(); err == nil {
				size += fi.Size()
			}
		}
		m["media_cache_bytes"] = size
	}
	if entries, err := os.ReadDir(proxyCacheDir); err == nil {
		m["proxy_cache_files"] = len(entries)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, m)
}
