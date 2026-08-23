package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

func serveLocalFile(c *gin.Context, path string) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	c.File(path)
}

func (h *Handler) GetFile(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	db := GetDB()
	post := db.Get(id)
	if post == nil || !post.Downloaded || post.FilePath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	serveLocalFile(c, post.FilePath)
}

func (h *Handler) SaveFile(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	db := GetDB()
	post := db.Get(id)
	if post == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}

	name := fmt.Sprintf("%d.%s", id, post.FileType)
	if post.Downloaded && post.FilePath != "" {
		c.FileAttachment(post.FilePath, name)
		return
	}

	if post.FileURL == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", post.FileURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file url"})
		return
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	if fu, err := url.Parse(post.FileURL); err == nil {
		req.Header.Set("Referer", h.refererForHost(fu.Hostname()))
	}

	resp, err := h.provider().HTTPClient().Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream status %d", resp.StatusCode)})
		return
	}

	wh := c.Writer.Header()
	wh.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	for k, vs := range resp.Header {
		switch strings.ToLower(k) {
		case "content-type", "content-length", "accept-ranges", "cache-control", "etag", "last-modified":
			for _, v := range vs {
				wh.Add(k, v)
			}
		}
	}
	c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
}

// proxyHostAllowed разрешает CDN-хосты всех известных провайдеров:
// после переключения источника старые превью из локальной БД
// (ссылки на прошлый сайт) должны продолжать открываться.
func (h *Handler) proxyHostAllowed(host string) bool {
	host = strings.ToLower(host)
	if h.providers == nil {
		return false
	}
	for _, p := range h.providers {
		if p.AllowsHost(host) {
			return true
		}
	}
	return false
}

// refererForHost подбирает Referer под хост апстрима: CDN Gelbooru
// (hotlink.php) и rule34 проверяют его, причём пост в БД может быть
// с сайта, который сейчас не активен.
func (h *Handler) refererForHost(host string) string {
	host = strings.ToLower(host)
	if h.providers != nil {
		for _, p := range h.providers {
			if p.AllowsHost(host) {
				return p.RefererURL()
			}
		}
	}
	return "https://rule34.xxx/"
}

func (h *Handler) GetThumb(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	path := filepath.Join("data", "thumbs", fmt.Sprintf("%d.jpg", id))

	db := GetDB()
	if post := db.Get(id); post != nil && post.Downloaded && post.ThumbPath != "" {
		path = post.ThumbPath
	}

	serveLocalFile(c, path)
}

func (h *Handler) ProxyRemote(c *gin.Context) {
	raw := c.Query("url")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url required"})
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid url"})
		return
	}
	if !h.proxyHostAllowed(u.Hostname()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "host not allowed"})
		return
	}

	if c.GetHeader("Range") == "" {
		proxyCache.Lock()
		it, ok := proxyCache.m[u.String()]
		proxyCache.Unlock()
		if !ok {

			if data, err := os.ReadFile(proxyCache.diskPath(u.String())); err == nil {
				proxyCache.store(u.String(), data, "")
				it = proxyCacheEntry{data: data, ct: http.DetectContentType(data)}
				ok = true
			}
		}
		if ok {
			wh := c.Writer.Header()
			wh.Set("Content-Type", it.ct)
			wh.Set("Cache-Control", "private, max-age=86400")
			c.Data(http.StatusOK, it.ct, it.data)
			return
		}
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", u.String(), nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid url"})
		return
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", h.refererForHost(u.Hostname()))
	if r := c.GetHeader("Range"); r != "" {
		req.Header.Set("Range", r)
	}

	resp, err := h.provider().HTTPClient().Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && c.GetHeader("Range") == "" && resp.ContentLength > 0 && resp.ContentLength <= proxyCache.maxItemSize {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			proxyCache.store(u.String(), body, resp.Header.Get("Content-Type"))
			wh := c.Writer.Header()
			wh.Set("Content-Type", resp.Header.Get("Content-Type"))
			wh.Set("Cache-Control", "private, max-age=86400")
			c.Data(http.StatusOK, resp.Header.Get("Content-Type"), body)
			return
		}
	}

	wh := c.Writer.Header()
	for k, vs := range resp.Header {
		switch strings.ToLower(k) {
		case "content-type", "content-length", "content-range", "accept-ranges", "cache-control", "etag", "last-modified":
			for _, v := range vs {
				wh.Add(k, v)
			}
		}
	}
	if wh.Get("Cache-Control") == "" {
		wh.Set("Cache-Control", "private, max-age=86400")
	}
	c.DataFromReader(resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
}

type proxyCacheEntry struct {
	data []byte
	ct   string
}

type proxyCacheStore struct {
	sync.Mutex
	m           map[string]proxyCacheEntry
	order       []string
	totalBytes  int64
	maxBytes    int64
	maxItems    int
	maxItemSize int64
}

var proxyCache = proxyCacheStore{
	m:           make(map[string]proxyCacheEntry),
	maxBytes:    128 << 20,
	maxItems:    512,
	maxItemSize: 4 << 20,
}

const proxyCacheDir = "data/proxy-cache"
const proxyDiskCacheMax = 256 << 10

var (
	proxyDiskOnce sync.Once
)

func proxyCacheDiskInit() {
	proxyDiskOnce.Do(func() {
		os.MkdirAll(proxyCacheDir, 0755)

		entries, err := os.ReadDir(proxyCacheDir)
		if err != nil || len(entries) <= proxyCache.maxItems {
			return
		}
		type fe struct {
			name string
			t    time.Time
		}
		list := make([]fe, 0, len(entries))
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				os.Remove(filepath.Join(proxyCacheDir, e.Name()))
				continue
			}
			list = append(list, fe{e.Name(), fi.ModTime()})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].t.Before(list[j].t) })
		for len(list) > proxyCache.maxItems {
			os.Remove(filepath.Join(proxyCacheDir, list[0].name))
			list = list[1:]
		}
	})
}

func (pc *proxyCacheStore) diskPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(proxyCacheDir, hex.EncodeToString(sum[:]))
}

func (pc *proxyCacheStore) persistDisk(key string, data []byte) {
	if int64(len(data)) > proxyDiskCacheMax {
		return
	}
	proxyCacheDiskInit()
	p := pc.diskPath(key)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	os.Rename(tmp, p)
}

func (pc *proxyCacheStore) store(key string, data []byte, ct string) {
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	if int64(len(data)) > pc.maxItemSize {
		return
	}
	var evicted []string
	pc.Lock()
	if _, ok := pc.m[key]; ok {
		pc.totalBytes -= int64(len(pc.m[key].data))
		pc.m[key] = proxyCacheEntry{data: data, ct: ct}
		pc.totalBytes += int64(len(data))
	} else {
		for pc.totalBytes+int64(len(data)) > pc.maxBytes || len(pc.m) >= pc.maxItems {
			if len(pc.order) == 0 {
				pc.Unlock()
				return
			}
			oldest := pc.order[0]
			pc.order = pc.order[1:]
			if e, ok := pc.m[oldest]; ok {
				pc.totalBytes -= int64(len(e.data))
				delete(pc.m, oldest)
				evicted = append(evicted, oldest)
			}
		}
		pc.m[key] = proxyCacheEntry{data: data, ct: ct}
		pc.order = append(pc.order, key)
		pc.totalBytes += int64(len(data))
	}
	pc.Unlock()
	for _, k := range evicted {
		os.Remove(pc.diskPath(k))
	}
	pc.persistDisk(key, data)
}
