package internal

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
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
	// warmPending — число живых горутин прогрева: при быстрых переходах по
	// страницам без лимита за слоты копились бы сотни висящих горутин (P2-17).
	warmPending atomic.Int32
	// mediaWarmInFlight — отдельный лимит на долгие видео-прогревы (оба
	// ограничителя вместе не дают одному видео занять все слоты навсегда).
	mediaWarmInFlight atomic.Int32
	// mediaWarmSpawned — живые горутины `go warmMediaCacheFile`. Увеличивается
	// ДО запуска (в месте спавна), чтобы тесты могли дождаться их полного
	// завершения и только потом подменять каталоги кэшей (иначе — data race
	// с чтением mediaCacheDir/mediaDiskOnce в фоновой горутине).
	mediaWarmSpawned atomic.Int32
)

const (
	maxWarmPending = 8
	maxMediaWarm   = 2
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
		// Лимит живых горутин: при быстрых переходах по страницам не копим
		// очередь ожидающих слот (P2-17).
		if int(warmPending.Load()) >= maxWarmPending {
			warmInflight.Delete(key)
			continue
		}
		warmPending.Add(1)
		go func(key string, u *url.URL) {
			defer func() {
				warmPending.Add(-1)
				warmInflight.Delete(key)
			}()
			select {
			case warmSlots <- struct{}{}:
				defer func() { <-warmSlots }()
				warmOne(h, key, u)
			default:
				// Слоты заняты (например, долгим видео-прогревом): не копим
				// очередь — превью догреется через обычный proxy-путь.
			}
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

// ── Прогрев полного медиа (видео) в дисковый media-cache ──────────────
// Превью (warmPreviewCache) достаточно для сетки, но первый просмотр
// <video> идёт через /api/proxy с `Range: bytes=0-`: браузер часто рвёт
// соединение сразу после чтения moov-заголовка, и media-cache не успевает
// заполниться из обычного стрима. warmMediaCacheFile качает файл целиком
// фоном (не завися от клиента), и перемотка/повторный просмотр к этому
// моменту уже обслуживаются локально.

func warmMediaCacheFile(h *Handler, u *url.URL) {
	if !warmEnabled() || h == nil || u == nil {
		return
	}
	if mediaCacheGet(u.String()) != "" {
		return
	}
	if !h.proxyHostAllowed(u.Hostname()) {
		return
	}
	key := u.String()
	if _, busy := warmInflight.LoadOrStore(key, struct{}{}); busy {
		return
	}
	// Долгие видео-прогревы ограничиваем отдельно: иначе пара роликов может
	// занять все слоты warmSlots на полчаса и задушить превью-прогрев (P2-9).
	if int(mediaWarmInFlight.Load()) >= maxMediaWarm {
		warmInflight.Delete(key)
		return
	}
	mediaWarmInFlight.Add(1)
	defer func() {
		mediaWarmInFlight.Add(-1)
	}()
	defer warmInflight.Delete(key)
	warmSlots <- struct{}{}
	defer func() { <-warmSlots }()

	// Пока стояли в очереди слотов, файл могли докачать другие пути.
	if mediaCacheGet(u.String()) != "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", h.refererForHost(u.Hostname()))

	resp, err := MediaHTTPClient().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	sink := newMediaCacheSink(u.String())
	if sink == nil {
		return
	}
	written, werr := io.Copy(sink, io.LimitReader(resp.Body, mediaCacheMaxItem+1))
	if werr != nil || written == 0 || written > mediaCacheMaxItem {
		sink.abort()
		return
	}
	sink.commit()
}
