package internal

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── Прогрев кэша превью (cache warming) ─────────────────────────────────
// Превью страницы поиска скачиваются фоново сразу после выдачи /api/posts:
// пока пользователь смотрит первую страницу, сервер уже положил превью в
// RAM/дисковый proxy-cache, и сетка грузится с локального диска по LAN,
// а не с медленного CDN. Прокси для этого не нужен — байты идут по прямому
// каналу, просто заранее. Выключается BRIEFLY_WARM=0.

var (
	// warmInflight дедуплицирует параллельные прогревы одного URL
	// (два клиента открыли один и тот же поиск).
	warmInflight sync.Map // url -> struct{}
	// warmSlots — общий semaphore на все прогревы: не душим CDN
	// параллельной закачкой всей страницы.
	warmSlots = make(chan struct{}, 4)
)

func warmEnabled() bool {
	return os.Getenv("BRIEFLY_WARM") != "0"
}

// warmPreviewCache планирует фоновое скачивание превью для выдачи поиска.
// entries — gin.H-записи из SearchPosts/GetPostsByIDs (нужны preview_url,
// downloaded и file_type). Скачанные картинки пропускаются: их сетка берёт
// из /api/thumb. Неблокирующая: на каждый URL максимум одна горутина.
func warmPreviewCache(h *Handler, entries []gin.H) {
	if !warmEnabled() || h == nil {
		return
	}
	for _, e := range entries {
		purl, _ := e["preview_url"].(string)
		if purl == "" {
			continue
		}
		downloaded, _ := e["downloaded"].(bool)
		ft, _ := e["file_type"].(string)
		// Скачанное видео в сетке всё равно показывает превью с CDN,
		// картинки — локальный /api/thumb.
		if downloaded && ft != "video" {
			continue
		}
		u, err := url.Parse(purl)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if !h.proxyHostAllowed(u.Hostname()) {
			continue
		}
		key := u.String()
		if _, busy := warmInflight.LoadOrStore(key, struct{}{}); busy {
			continue
		}
		go func(key string, u *url.URL) {
			defer warmInflight.Delete(key)
			warmSlots <- struct{}{}
			defer func() { <-warmSlots }()
			warmOne(h, key, u)
		}(key, u)
	}
}

// warmOne синхронно качает одно превью в proxy-cache. true — файл реально
// скачан и сохранён; false — уже в кэше, запрещён или не скачался.
func warmOne(h *Handler, key string, u *url.URL) bool {
	// Уже в RAM или на диске — CDN не трогаем.
	if _, ok := proxyCache.get(key); ok {
		return false
	}
	if _, err := os.Stat(proxyCache.diskPath(key)); err == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", h.refererForHost(u.Hostname()))

	resp, err := MediaHTTPClient().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if resp.ContentLength > proxyCache.maxItemSize {
		return false // нетипично большое «превью» — кэширует обычный прокси
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, proxyCache.maxItemSize+1))
	if err != nil || len(data) == 0 || int64(len(data)) > proxyCache.maxItemSize {
		return false
	}
	proxyCache.store(key, data, resp.Header.Get("Content-Type"))
	return true
}
