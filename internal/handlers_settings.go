package internal

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// requireAdmin — доступ к настройкам сервера (API-ключи, пути, прокси)
// только у администратора (первый зарегистрированный пользователь) либо
// в legacy-режиме BRIEFLY_TOKEN (единственный владелец сервера).
func requireAdmin(c *gin.Context) bool {
	if TokenMatches(c) {
		return true
	}
	u := sessionUser(c)
	return u != "" && GetAccounts().IsAdmin(u)
}

// providerList — список источников для UI: встроенные + сторонние из
// data/providers.json, с человеческими именами. Один и тот же список отдают и
// админские настройки, и публичный /api/providers — иначе в шапке и в
// настройках показывалось бы разное.
func providerList() []gin.H {
	descriptors := allProviderDescriptors()
	providers := make([]gin.H, 0, len(descriptors))
	for _, p := range descriptors {
		providers = append(providers, gin.H{"value": p.Name, "name": p.DisplayName})
	}
	return providers
}

func (h *Handler) GetSettings(c *gin.Context) {
	if !requireAdmin(c) {
		AbortWithError(c, ErrAdminOnly)
		return
	}
	cfg := GetConfig()
	maxQueryLen := h.effectiveMaxQueryLen()
	c.JSON(http.StatusOK, gin.H{
		"api_keys":             cfg.GetAPICredentials(),
		"proxy_url":            cfg.GetProxyURL(),
		"concurrent_downloads": cfg.GetConcurrentDownloads(),
		"auto_download":        cfg.GetAutoDownload(),
		"save_path":            cfg.GetSavePath(),
		"thumb_size":           cfg.GetThumbSize(),
		"min_id":               cfg.GetMinID(),
		"rename_template":      cfg.GetRenameTemplate(),
		"provider":             cfg.GetProvider(),
		"providers":            providerList(),
		"max_query_len":        maxQueryLen,
	})
}

// ListProviders — GET /api/providers: источники постов, активный источник и
// лимит длины запроса.
//
// (Имя не «GetProviders»: на Handler уже есть GetProviders, отдающий мапу
// провайдеров для поиска.)
//
// Это НЕ настройки сервера, поэтому эндпоинт не админский. Бейдж «Источник
// постов» в шапке видят все, а список жил только в /api/settings под
// requireAdmin: у обычного пользователя loadSettings() выходил раньше запроса,
// шапка оставляла голое «rule34» из HTML, а меню источников не рисовалось
// вовсе. Ровно так выглядел инстанс, куда входили не администратором.
func (h *Handler) ListProviders(c *gin.Context) {
	cfg := GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"providers":     providerList(),
		"provider":      cfg.GetProvider(),
		"max_query_len": h.effectiveMaxQueryLen(),
	})
}

// canSwitchSource — кто вправе сменить активный источник.
//
// Шапочный бейдж источника видят все, поэтому менять его может любой вошедший
// пользователь. В legacy-режиме (аккаунтов нет) сессий не бывает вовсе — там,
// как и остальному API, доверяем локальной сети.
func canSwitchSource(c *gin.Context) bool {
	if requireAdmin(c) {
		return true
	}
	if GetAccounts().Count() == 0 {
		return true
	}
	return sessionUser(c) != ""
}

// SetProvider — POST /api/providers {"provider":"rule34"}.
//
// Отдельный эндпоинт вместо POST /settings: тот админский, а источник в шапке
// должен переключаться всеми, кому он там показан.
func (h *Handler) SetProvider(c *gin.Context) {
	if !canSwitchSource(c) {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	// Мусор не молчим: тихо оставшийся прежним источник выглядел бы как
	// «нажал — не переключилось».
	if !isKnownProvider(req.Provider) {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	cfg := GetConfig()
	cfg.SetProvider(req.Provider)
	cfg.Save()
	c.JSON(http.StatusOK, gin.H{"ok": true, "provider": req.Provider})
}

func (h *Handler) UpdateSettings(c *gin.Context) {
	if !requireAdmin(c) {
		AbortWithError(c, ErrAdminOnly)
		return
	}
	var req struct {
		APIKeys []APICredential `json:"api_keys"`
		APIKey  string          `json:"api_key"`
		APIKey2 string          `json:"api_key2"`
		UserID  string          `json:"user_id"`
		// Указатели на строки: частичные сохранения (например, смена
		// провайдера из хедера) не должны трогать другие поля, а пустая
		// строка здесь — явный сброс (например, очистка proxy_url).
		ProxyURL            *string `json:"proxy_url"`
		ConcurrentDownloads int     `json:"concurrent_downloads"`
		// AutoDownload — указатель: частичные сохранения не должны
		// сбрасывать флаг в false.
		AutoDownload   *bool   `json:"auto_download"`
		SavePath       *string `json:"save_path"`
		ThumbSize      int     `json:"thumb_size"`
		MinID          int     `json:"min_id"`
		RenameTemplate *string `json:"rename_template"`
		Provider       string  `json:"provider"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := GetConfig()
	if len(req.APIKeys) > 0 {
		// Ключи приходят из UI без поля provider — восстанавливаем теги
		// уже известных ключей, чтобы привязка к сайту не терялась.
		prevByAPIKey := make(map[string]string)
		for _, k := range cfg.GetAPICredentials() {
			if k.Provider != "" {
				prevByAPIKey[k.APIKey] = k.Provider
			}
		}
		clean := make([]APICredential, 0, len(req.APIKeys))
		for _, k := range req.APIKeys {
			if k.APIKey == "" {
				continue
			}
			if k.Provider == "" {
				k.Provider = prevByAPIKey[k.APIKey]
			}
			clean = append(clean, k)
		}
		cfg.SetAPIKeys(clean)
	} else {

		cur := cfg.GetAPICredentials()
		changed := false
		if req.APIKey != "" {
			if len(cur) > 0 {
				cur[0].APIKey = req.APIKey
				if req.UserID != "" {
					cur[0].UserID = req.UserID
				}
			} else {
				cur = append(cur, APICredential{Name: "Primary", APIKey: req.APIKey, UserID: req.UserID})
			}
			changed = true
		}
		if req.APIKey2 != "" {
			if len(cur) > 1 {
				cur[1].APIKey = req.APIKey2
			} else {
				cur = append(cur, APICredential{Name: "Secondary", APIKey: req.APIKey2})
			}
			changed = true
		}
		if changed {
			cfg.SetAPIKeys(cur)
		}
	}
	if req.ProxyURL != nil {
		cfg.SetProxyURL(*req.ProxyURL)
		for _, p := range h.providers {
			p.RebuildClient()
		}
		if h.downloader != nil {
			h.downloader.RebuildClient()
		}
		RebuildMediaClient()
	}
	if req.Provider != "" {
		cfg.SetProvider(req.Provider)
	}
	if req.ConcurrentDownloads > 0 {
		cfg.SetConcurrentDownloads(req.ConcurrentDownloads)
	}
	if req.AutoDownload != nil {
		cfg.SetAutoDownload(*req.AutoDownload)
	}
	if req.SavePath != nil {
		cfg.SetSavePath(*req.SavePath)
		cfg.SetDownloadPath(*req.SavePath)
	}
	if req.ThumbSize > 0 {
		cfg.SetThumbSize(req.ThumbSize)
	}
	if req.MinID >= 0 {
		cfg.SetMinID(req.MinID)
	}
	if req.RenameTemplate != nil {
		cfg.SetRenameTemplate(*req.RenameTemplate)
	}

	cfg.Save()

	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}

var statsCache = struct {
	sync.Mutex
	at         time.Time
	thumbCount int
	diskUsage  int64
}{}

const statsCacheTTL = 5 * time.Second

func (h *Handler) GetStats(c *gin.Context) {
	db := GetDB()
	stats := db.Stats()

	cfg := GetConfig()

	statsCache.Lock()
	now := time.Now()
	if now.Sub(statsCache.at) > statsCacheTTL {
		thumbCount := 0
		if entries, err := os.ReadDir("data/thumbs"); err == nil {
			thumbCount = len(entries)
		}

		postDir := "data/posts"
		var diskUsage int64
		if entries, err := os.ReadDir(postDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					if subEntries, err := os.ReadDir(filepath.Join(postDir, entry.Name())); err == nil {
						for _, sub := range subEntries {
							if info, err := sub.Info(); err == nil {
								diskUsage += info.Size()
							}
						}
					}
				}
			}
		}
		statsCache.thumbCount = thumbCount
		statsCache.diskUsage = diskUsage
		statsCache.at = now
	}
	thumbCount := statsCache.thumbCount
	diskUsage := statsCache.diskUsage
	statsCache.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"total_searched":       stats["total"],
		"total_downloaded":     stats["downloaded"],
		"thumbnails":           thumbCount,
		"disk_usage_mb":        fmt.Sprintf("%.1f", float64(diskUsage)/1024/1024),
		"concurrent_downloads": cfg.GetConcurrentDownloads(),
	})
}
