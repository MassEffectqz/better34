package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
)

// Дозагрузка миниатюр по требованию.
//
// Раньше /api/thumb/<id> отдавал только локальный файл и для нескачанных
// постов отвечал 404. Сетка лайков/скрытого при этом сыпала сотни 404,
// хотя у поста есть preview_url на CDN. Теперь обработчик сам докачивает
// превью, сохраняет его в data/thumbs/<id>.jpg и отдаёт результат.

// Ограничения дозагрузки: защита от «удара» по CDN и от поедания памяти.
const (
	thumbFetchTimeout   = 15 * time.Second
	thumbFetchMaxBytes  = 24 << 20 // 24 МБ — с запасом для крупных превью
	thumbFetchMaxActive = 6        // одновременных походов на апстрим
	thumbFetchMaxWidth  = 8000     // защита от абсурдных размеров
	thumbFetchMaxHeight = 8000
)

// thumbFetchSem ограничивает число одновременных походов на CDN.
var thumbFetchSem = make(chan struct{}, thumbFetchMaxActive)

// errThumbFetchUnsupported — дозагрузка невозможна (нет поста/URL/хоста).
var errThumbFetchUnsupported = errors.New("thumb: fetch unsupported")

type thumbCall struct {
	done chan struct{}
	path string
	err  error
}

// thumbFetchGroup — singleflight по id: сетка грузит десятки миниатюр сразу,
// один и тот же id может прийти двумя карточками одновременно.
type thumbFetchGroup struct {
	mu sync.Mutex
	m  map[int]*thumbCall
}

func (g *thumbFetchGroup) Do(id int, fn func() (string, error)) (string, error) {
	g.mu.Lock()
	if cl, ok := g.m[id]; ok {
		g.mu.Unlock()
		<-cl.done
		return cl.path, cl.err
	}
	cl := &thumbCall{done: make(chan struct{})}
	if g.m == nil {
		g.m = make(map[int]*thumbCall)
	}
	g.m[id] = cl
	g.mu.Unlock()

	path, err := fn()
	cl.path, cl.err = path, err
	g.mu.Lock()
	delete(g.m, id)
	g.mu.Unlock()
	close(cl.done)
	return path, err
}

var thumbFetches thumbFetchGroup

// thumbMisses — негативный кэш: не даём долбить CDN по постам, чьё превью
// уже отдало 404 (удалённый пост). Повторяем попытку не раньше чем через час.
var thumbMisses = struct {
	mu sync.Mutex
	m  map[int]time.Time
}{m: make(map[int]time.Time)}

const thumbMissTTL = time.Hour

// errThumbUpstreamGone — апстрим ответил 404/410: превью удалено или
// пост скрыт. Единственный случай, когда негативный кэш оправдан.
// Сетевые сбои, таймауты и 5xx — временные, их кэшировать нельзя:
// иначе битое превью «залипало» бы на час.
var errThumbUpstreamGone = errors.New("thumb: upstream gone")

// thumbIsPermanentGone — ошибка означает, что изображения больше нет.
func thumbIsPermanentGone(err error) bool {
	return errors.Is(err, errThumbUpstreamGone) || errors.Is(err, errThumbFetchUnsupported)
}

func thumbMarkMiss(id int) {
	thumbMisses.mu.Lock()
	if thumbMisses.m == nil {
		thumbMisses.m = make(map[int]time.Time)
	}
	thumbMisses.m[id] = time.Now()
	thumbMisses.mu.Unlock()
}

func thumbRecentMiss(id int) bool {
	thumbMisses.mu.Lock()
	defer thumbMisses.mu.Unlock()
	at, ok := thumbMisses.m[id]
	if !ok {
		return false
	}
	if time.Since(at) > thumbMissTTL {
		delete(thumbMisses.m, id)
		return false
	}
	return true
}

func thumbClearMiss(id int) {
	thumbMisses.mu.Lock()
	delete(thumbMisses.m, id)
	thumbMisses.mu.Unlock()
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// thumbSourceURL выбирает URL, с которого стоит взять изображение: сперва
// preview_url (лёгкое превью), затем file_url для картинок. Видео не тянем —
// для него нужен кадр ffmpeg, и это делает генератор миниатюр при скачивании.
func thumbSourceURL(post *Post) (string, error) {
	if post == nil {
		return "", errThumbFetchUnsupported
	}
	if post.PreviewURL != "" {
		return post.PreviewURL, nil
	}
	if post.FileURL != "" {
		if ext := strings.ToLower(filepath.Ext(strings.SplitN(post.FileURL, "?", 2)[0])); !videoExtensions[ext] {
			return post.FileURL, nil
		}
	}
	return "", errThumbFetchUnsupported
}

// ensureThumb возвращает путь к локальной миниатюре поста, докачивая её при
// необходимости. Пустой путь = миниатюру получить не удалось.
func (h *Handler) ensureThumb(id int) (string, error) {
	tg := NewThumbnailGenerator()

	// Уже готовая миниатюра: сначала путь из БД, потом дефолтный.
	if post := GetDB().Get(id); post != nil && fileExists(post.ThumbPath) {
		return post.ThumbPath, nil
	}
	if p := tg.ThumbPath(id); fileExists(p) {
		return p, nil
	}
	if thumbRecentMiss(id) {
		return "", errThumbUpstreamGone
	}

	path, err := thumbFetches.Do(id, func() (string, error) {
		// Повторная проверка после ожидания лидера singleflight: файл мог
		// появиться на диске, пока мы стояли в очереди.
		if p := tg.ThumbPath(id); fileExists(p) {
			return p, nil
		}
		// Пост читаем здесь, а не в лидере: к моменту загрузки он уже есть.
		return h.downloadThumb(id, GetDB().Get(id), tg)
	})
	if err != nil {
		// Негативный кэш ставим только когда превью действительно больше
		// нет. Сетевой сбой/таймаут/5xx — временные: кэшировать их нельзя,
		// иначе после неудачного открытия вкладки превью не подтянулось бы
		// ещё час, хотя CDN давно отвечает нормально.
		if thumbIsPermanentGone(err) {
			thumbMarkMiss(id)
		}
		log.Printf("thumb: failed %d: %v", id, err)
		return "", err
	}
	thumbClearMiss(id)
	return path, nil
}

// thumbFallbackURL — запасной источник: оригинал поста, если он не видео
// и отличается от уже неудачного URL. Прежде всего спасает старые лайки,
// у которых thumbnail на CDN протух, а images/… ещё жив.
func thumbFallbackURL(post *Post, tried string) string {
	if post == nil || post.FileURL == "" || post.FileURL == tried {
		return ""
	}
	if ext := strings.ToLower(filepath.Ext(strings.SplitN(post.FileURL, "?", 2)[0])); videoExtensions[ext] {
		return ""
	}
	return post.FileURL
}

// downloadThumb качает превью, декодирует и сохраняет уменьшенную копию.
func (h *Handler) downloadThumb(id int, post *Post, tg *ThumbnailGenerator) (string, error) {
	rawURL, err := thumbSourceURL(post)
	if err != nil {
		return "", err
	}
	return h.downloadThumbFrom(rawURL, id, post, tg)
}

// downloadThumbFrom — сам поход на CDN по конкретному URL.
func (h *Handler) downloadThumbFrom(rawURL string, id int, post *Post, tg *ThumbnailGenerator) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", errThumbFetchUnsupported
	}
	if (u.Scheme != "https" && u.Scheme != "http") || !h.proxyHostAllowed(u.Hostname()) {
		return "", errThumbFetchUnsupported
	}

	// Контекст запроса намеренно НЕ используем: клиент может уйти, а
	// скачанный файл пригодится следующему. Свой таймаут — превью с CDN
	// иногда отдаётся по нескольку секунд.
	ctx, cancel := context.WithTimeout(context.Background(), thumbFetchTimeout)
	defer cancel()

	select {
	case thumbFetchSem <- struct{}{}:
		defer func() { <-thumbFetchSem }()
	case <-ctx.Done():
		return "", fmt.Errorf("thumb: sem wait: %w", ctx.Err())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", h.refererForHost(u.Hostname()))
	resp, err := MediaHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Немного читаем тела, чтобы соединение вернулось в пул.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			// Превью удалено: пробуем оригинал, у него отдельный URL.
			if alt := thumbFallbackURL(post, u.String()); alt != "" {
				return h.downloadThumbFrom(alt, id, post, tg)
			}
			return "", fmt.Errorf("%w: status %d", errThumbUpstreamGone, resp.StatusCode)
		}
		return "", fmt.Errorf("thumb: upstream status %d", resp.StatusCode)
	}

	img, err := imaging.Decode(io.LimitReader(resp.Body, thumbFetchMaxBytes), imaging.AutoOrientation(true))
	if err != nil {
		// Ответ не декодируется — возможно, это страница ошибки CDN.
		// Пробуем оригинал, прежде чем считать превью битым.
		if alt := thumbFallbackURL(post, u.String()); alt != "" {
			return h.downloadThumbFrom(alt, id, post, tg)
		}
		return "", fmt.Errorf("thumb: decode: %w", err)
	}
	if b := img.Bounds(); b.Dx() > thumbFetchMaxWidth || b.Dy() > thumbFetchMaxHeight {
		return "", fmt.Errorf("thumb: image too large %dx%d", b.Dx(), b.Dy())
	}

	thumbPath, err := tg.GenerateFromReader(img, id)
	if err != nil {
		return "", err
	}
	// Путь запоминаем в БД, чтобы следующие запросы не искали файл заново.
	// Только для поста, который действительно есть в базе: превью могли
	// дозагрузить для id без записи, и тогда UPDATE не изменил бы ничего.
	if post != nil {
		GetDB().SetThumbPathOnly(id, thumbPath)
	}
	log.Printf("thumb: fetched %d from %s", id, u.Hostname())
	return thumbPath, nil
}
