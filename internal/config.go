package internal

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type APICredential struct {
	Name   string `json:"name"`
	APIKey string `json:"api_key"`
	UserID string `json:"user_id"`
	// Provider ограничивает ключ одним источником ("rule34", "gelbooru").
	// Пусто — ключ используется всеми провайдерами (legacy-поведение).
	Provider string `json:"provider,omitempty"`
}

type Config struct {
	mu                  sync.RWMutex
	APIKeys             []APICredential `json:"api_keys"`
	ProxyURL            string          `json:"proxy_url"`
	DownloadPath        string          `json:"download_path"`
	ConcurrentDownloads int             `json:"concurrent_downloads"`
	ThumbSize           int             `json:"thumb_size"`
	AutoDownload        bool            `json:"auto_download"`
	SavePath            string          `json:"save_path"`
	MinID               int             `json:"min_id"`
	RenameTemplate      string          `json:"rename_template"`
	Provider            string          `json:"provider"` // активный источник постов

	loadedAt time.Time
}

var config *Config
var configOnce sync.Once
var configCheckAt atomic.Int64
var configGen atomic.Uint64

func GetConfig() *Config {
	configOnce.Do(func() {
		config = &Config{
			ProxyURL:            "",
			DownloadPath:        "data/posts",
			ConcurrentDownloads: 5,
			ThumbSize:           300,
			AutoDownload:        false,
			SavePath:            "data/posts",
		}
		config.load()
	})
	config.maybeReload()
	return config
}

// maybeReload перечитывает data/config.json, если файл изменился на диске
// (как минимум раз в 5 секунд) — новые API-ключи подхватываются без рестарта.
func (c *Config) maybeReload() {
	now := time.Now()
	if now.Sub(time.Unix(0, configCheckAt.Load())) < 5*time.Second {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	configCheckAt.Store(now.UnixNano())
	st, err := os.Stat("data/config.json")
	if err != nil {
		return
	}
	if len(c.APIKeys) > 0 && !st.ModTime().After(c.loadedAt) {
		return
	}
	c.load()
}

func (c *Config) load() {
	data, err := os.ReadFile("data/config.json")
	if err != nil {
		return
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	// Молча проглотить ошибку нельзя: часть настроек останется нулевой,
	// а администратор не поймёт, почему ключи/пути «слетели».
	if err := json.Unmarshal(data, c); err != nil {
		slog.Warn("config.json: не удалось разобрать, часть настроек пропущена", "error", err)
	}

	if len(c.APIKeys) == 0 {
		var legacy struct {
			APIKey  string `json:"api_key"`
			APIKey2 string `json:"api_key2"`
			UserID  string `json:"user_id"`
		}
		json.Unmarshal(data, &legacy)
		if legacy.APIKey != "" {
			c.APIKeys = append(c.APIKeys, APICredential{Name: "Основной", APIKey: legacy.APIKey, UserID: legacy.UserID})
		}
		if legacy.APIKey2 != "" {
			c.APIKeys = append(c.APIKeys, APICredential{Name: "Второй", APIKey: legacy.APIKey2})
		}
	}
	c.loadedAt = time.Now()
	configGen.Add(1)
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile("data/config.json", data, 0600)
}

func (c *Config) GetAPICredentials() []APICredential {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]APICredential, len(c.APIKeys))
	copy(out, c.APIKeys)
	return out
}

func (c *Config) SetAPIKeys(keys []APICredential) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.APIKeys = keys
}

func (c *Config) SetProxyURL(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ProxyURL = url
}

func (c *Config) SetConcurrentDownloads(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ConcurrentDownloads = n
}

func (c *Config) SetAutoDownload(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AutoDownload = v
}

func (c *Config) GetProxyURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ProxyURL
}

func (c *Config) GetDownloadPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DownloadPath
}

func (c *Config) SetDownloadPath(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DownloadPath = p
}

func (c *Config) GetSavePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SavePath
}

func (c *Config) SetSavePath(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SavePath = p
}

func (c *Config) GetMinID() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.MinID
}

func (c *Config) SetMinID(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.MinID = n
}

func (c *Config) GetThumbSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ThumbSize
}

func (c *Config) SetThumbSize(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ThumbSize = n
}

func (c *Config) GetRenameTemplate() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.RenameTemplate
}

func (c *Config) SetRenameTemplate(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.RenameTemplate = s
}

func (c *Config) GetConcurrentDownloads() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ConcurrentDownloads
}

func (c *Config) GetAutoDownload() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AutoDownload
}

// GetProvider возвращает нормализованное имя активного провайдера
// (пустое/неизвестное значение → дефолтный rule34).
func (c *Config) GetProvider() string {
	c.mu.RLock()
	name := strings.ToLower(strings.TrimSpace(c.Provider))
	c.mu.RUnlock()
	if !isKnownProvider(name) {
		return defaultProviderName
	}
	return name
}

func (c *Config) SetProvider(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	name = strings.ToLower(strings.TrimSpace(name))
	if !isKnownProvider(name) {
		return
	}
	c.Provider = name
}
