package internal

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// ── Медиа-клиент без общего таймаута ────────────────────────────────────
// buildAPIHTTPClient() имеет Timeout 30s, а http.Client.Timeout покрывает
// чтение всего тела ответа: видео, качающееся дольше 30 секунд, обрывалось
// посередине и браузер начинал сначала. Отдельный клиент на том же
// транспорте (DoH/SOCKS5) без общего Timeout; долгий стрим прерывает
// только контекст запроса (браузер отключился — загрузка отменяется).

var mediaHTTPClientPtr atomic.Pointer[http.Client]

func MediaHTTPClient() *http.Client {
	if c := mediaHTTPClientPtr.Load(); c != nil {
		return c
	}
	c := buildMediaHTTPClient()
	mediaHTTPClientPtr.Store(c)
	return c
}

func RebuildMediaClient() { mediaHTTPClientPtr.Store(buildMediaHTTPClient()) }

func buildMediaHTTPClient() *http.Client {
	tr := buildTransport()
	tr.ResponseHeaderTimeout = 15 * time.Second
	tr.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{Transport: tr}
}

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

	resp, err := MediaHTTPClient().Do(req)
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

// proxyCacheControl — Cache-Control для ответов прокси. kind=preview —
// превью поста, неизменяемое по построению (как локальная миниатюра):
// пусть живёт в браузерном кэше максимально долго, а service worker
// отдаёт их cache-first. Остальное (оригиналы, видео) — прежний режим.
func proxyCacheControl(c *gin.Context) string {
	if c.Query("kind") == "preview" {
		return "private, max-age=31536000, immutable"
	}
	return "private, max-age=86400"
}

// proxyUpstream строит запрос к CDN с нужными UA/Referer/Range.
func (h *Handler) proxyUpstream(c *gin.Context, u *url.URL, rangeHdr string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(c.Request.Context(), "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", h.refererForHost(u.Hostname()))
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	return MediaHTTPClient().Do(req)
}

// ── Singleflight для прокси ─────────────────────────────────────────────
// Параллельные /api/proxy одного URL (перезагрузка страницы во время
// загрузки превью, дубли в сетке) не должны ходить на CDN несколько раз:
// лидер качает и кладёт в RAM-кэш, остальные получают те же байты.
// Если файл оказался слишком большим для RAM-кэша, лидер уходит в
// обычный стриминг, а ожидавшие повторяют запрос самостоятельно.

var (
	errProxyStream = errors.New("proxy: response not RAM-cacheable")
	errProxyRetry  = errors.New("proxy: fetch independently")
)

type proxyCall struct {
	done  chan struct{}
	data  []byte
	ct    string
	retry bool
	err   error
}

type proxyFlightGroup struct {
	mu sync.Mutex
	m  map[string]*proxyCall
}

var proxyFetches proxyFlightGroup

func (g *proxyFlightGroup) Do(key string, leaderFn func() ([]byte, string, error)) ([]byte, string, bool, error) {
	g.mu.Lock()
	if cl, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-cl.done
		if cl.retry {
			return nil, "", false, errProxyRetry
		}
		return cl.data, cl.ct, false, cl.err
	}
	cl := &proxyCall{done: make(chan struct{})}
	if g.m == nil {
		g.m = make(map[string]*proxyCall)
	}
	g.m[key] = cl
	g.mu.Unlock()

	data, ct, err := leaderFn()
	cl.data, cl.ct, cl.err = data, ct, err
	if errors.Is(err, errProxyStream) {
		cl.retry = true
		cl.err = nil
	}
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	close(cl.done)
	return data, ct, true, err
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

	// Миниатюра по id неизменяема по построению: живёт в браузерном
	// HTTP-кэше максимально долго (service worker кэширует её cache-first,
	// но до его активации и вне его работают заголовки).
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
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

	// Большие медиа (видео >4MB не влезают в RAM-кэш) лежат в дисковом
	// кэше; отдаём через http.ServeContent — Range/перемотка работают
	// прямо из кэша, без повторного похода на CDN. Проверка до ветки
	// Range: кэш сам отвечает 206.
	if p := mediaCacheGet(u.String()); p != "" && serveMediaCacheFile(c, p) {
		return
	}

	cc := proxyCacheControl(c)
	noRange := c.GetHeader("Range") == ""
	var upstream *http.Response

	// Браузер почти всегда открывает <video> запросом `Range: bytes=0-`.
	// Такой запрос не попадает в media-cache (это диапазон!), поэтому без
	// вмешательства первый просмотр не прогревал бы кэш: повторный
	// просмотр и перемотка снова ходили бы на CDN. Качаем файл целиком
	// фоном, не завися от текущего ответа клиенту, — к следующему
	// просмотру/перемотке он уже лежит на диске.
	if !noRange && isVideoURL(u) && mediaCacheGet(u.String()) == "" {
		mediaWarmSpawned.Add(1)
		go func() {
			defer mediaWarmSpawned.Add(-1)
			warmMediaCacheFile(h, u)
		}()
	}

	// Большие видео без Range качаем сегментами: CDN семейства часто
	// режет скорость одного соединения, параллельные byte-range заполняют
	// media-cache в разы быстрее (без прокси). Проба внутри: апстрим без
	// Range или файл меньше порога → false, обычный путь ниже.
	if noRange && segmentedEnabled() && isVideoURL(u) {
		if h.streamSegmented(c, u, cc) {
			return
		}
	}

	if noRange {
		it, ok := proxyCache.get(u.String())
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
			wh.Set("Cache-Control", cc)
			c.Data(http.StatusOK, it.ct, it.data)
			return
		}

		// Singleflight: параллельные запросы одного URL обслуживаются
		// одним походом на CDN (малые файлы) или независимым повтором
		// (большие — лидер уходит в стриминг ниже).
		data, ct, isLeader, ferr := proxyFetches.Do(u.String(), func() ([]byte, string, error) {
			resp, err := h.proxyUpstream(c, u, "")
			if err != nil {
				return nil, "", err
			}
			if resp.StatusCode != http.StatusOK || resp.ContentLength <= 0 || resp.ContentLength > proxyCache.maxItemSize {
				upstream = resp
				return nil, "", errProxyStream
			}
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr != nil {
				return nil, "", rerr
			}
			ct := resp.Header.Get("Content-Type")
			proxyCache.store(u.String(), b, ct)
			return b, ct, nil
		})
		switch {
		case ferr == nil:
			wh := c.Writer.Header()
			wh.Set("Content-Type", ct)
			wh.Set("Cache-Control", cc)
			c.Data(http.StatusOK, ct, data)
			return
		case !isLeader && errors.Is(ferr, errProxyRetry):
			// свой запрос в streamUpstream ниже
		case isLeader && errors.Is(ferr, errProxyStream):
			// большой файл — стримим уже открытый ответ ниже
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed"})
			return
		}
	}

	h.streamUpstream(c, u, upstream, cc)
}

// streamUpstream отдаёт ответ апстрима клиенту: большие файлы стримятся
// с параллельной записью в дисковый кэш, чтобы следующие просмотры и
// перемотка не ходили на CDN. resp != nil означает «ответ уже открыт»
// (лидер singleflight); иначе запрос выполняется здесь.
func (h *Handler) streamUpstream(c *gin.Context, u *url.URL,
	resp *http.Response, cc string) {
	if resp == nil {
		var err error
		resp, err = h.proxyUpstream(c, u, c.GetHeader("Range"))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed"})
			return
		}
	}
	// Закрываем resp.Body ВСЕГДА: и для собственного запроса, и для ответа
	// лидера singleflight, переданного из ProxyRemote (errProxyStream). До
	// фикса это было только при ownResp — соединение с CDN не возвращалось
	// в пул после отдачи клиенту, копилась утечка на каждый большой файл.
	defer func() {
		resp.Body.Close()
	}()

	cacheable := c.GetHeader("Range") == "" && resp.StatusCode == http.StatusOK &&
		(resp.ContentLength < 0 || resp.ContentLength <= mediaCacheMaxItem)

	wh := c.Writer.Header()
	for k, vs := range resp.Header {
		switch strings.ToLower(k) {
		// cache-control задаём сами (proxyCacheControl): upstream-значение
		// иначе перебивало бы immutable для kind=preview.
		case "content-type", "content-length", "content-range", "accept-ranges", "etag", "last-modified":
			for _, v := range vs {
				wh.Add(k, v)
			}
		}
	}
	wh.Set("Cache-Control", cc)

	var body io.Reader = resp.Body
	var sink *mediaCacheSink
	if cacheable {
		if sink = newMediaCacheSink(u.String()); sink != nil {
			body = io.TeeReader(resp.Body, sink)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)
	copyErr := func() error {
		buf := make([]byte, 64<<10)
		const flushEvery = 256 << 10
		flusher, _ := c.Writer.(http.Flusher)
		pending := 0
		for {
			n, rerr := body.Read(buf)
			if n > 0 {
				if _, werr := c.Writer.Write(buf[:n]); werr != nil {
					return werr
				}
				pending += n
				if flusher != nil && pending >= flushEvery {
					flusher.Flush()
					pending = 0
				}
			}
			if rerr != nil {
				if flusher != nil && pending > 0 {
					flusher.Flush()
				}
				if rerr == io.EOF {
					return nil
				}
				return rerr
			}
		}
	}()
	if copyErr != nil || sink == nil {
		sink.abort()
	} else {
		sink.commit()
	}
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

// proxyCacheDir — дисковый каталог proxy-cache (переменная: тесты
// подменяют на временный каталог).
var proxyCacheDir = "data/proxy-cache"

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

// get возвращает элемент и обновляет его позицию до конца очереди
// (LRU: горячие превью не должны вытесняться новыми).
func (pc *proxyCacheStore) get(key string) (proxyCacheEntry, bool) {
	pc.Lock()
	defer pc.Unlock()
	it, ok := pc.m[key]
	if ok {
		for i, k := range pc.order {
			if k == key {
				copy(pc.order[i:], pc.order[i+1:])
				pc.order[len(pc.order)-1] = key
				break
			}
		}
	}
	return it, ok
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

// ── Дисковый кэш больших медиа (видео) ──────────────────────────────────
// RAM-кэш ограничен 4MB на элемент — видео туда не попадают и каждый
// просмотр перекачивался с CDN целиком. Здесь файлы стримятся на диск
// при первом просмотре; повторные запросы (включая Range-перемотку)
// обслуживает http.ServeContent из кэша.

var (
	mediaCacheDir = "data/media-cache"
	mediaDiskOnce sync.Once
)

const mediaCacheMaxItem = 256 << 20 // не кэшируем файлы больше 256MB

// mediaCacheDiskBudget — суммарный бюджет диска под media-cache.
// BRIEFLY_MEDIA_CACHE_GB (целое, GB) позволяет поднять его: кэш общий
// для всех устройств в LAN, чем он больше, тем чаще второй клиент
// (телефон) получает контент с локального диска, а не с CDN.
func mediaCacheDiskBudget() int64 {
	const defGB = 2
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_MEDIA_CACHE_GB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int64(n) << 30
		}
		log.Printf("BRIEFLY_MEDIA_CACHE_GB=%q не распознано, использую %dGB", v, defGB)
	}
	return int64(defGB) << 30
}

// mediaCacheBudgetOverride — тесты/настройки могут перекрыть бюджет; 0 — не задано.
var mediaCacheBudgetOverride int64

// mediaCacheMaxDiskBytes — суммарный бюджет диска под media-cache, читается
// при каждом вызове: значение из окружения (BRIEFLY_MEDIA_CACHE_GB)
// подхватывается и при прямом `go run .` с .env.
func mediaCacheMaxDiskBytes() int64 {
	if mediaCacheBudgetOverride > 0 {
		return mediaCacheBudgetOverride
	}
	return mediaCacheDiskBudget()
}

func mediaCacheInit() {
	mediaDiskOnce.Do(func() {
		os.MkdirAll(mediaCacheDir, 0755)
		cutoff := time.Now().Add(-24 * time.Hour)
		entries, err := os.ReadDir(mediaCacheDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".tmp") {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(mediaCacheDir, e.Name()))
			}
		}
		mediaCacheEvict()
	})
}

func mediaCacheKeyPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(mediaCacheDir, hex.EncodeToString(sum[:]))
}

// mediaCacheGet возвращает путь к закэшированному файлу или "".
func mediaCacheGet(key string) string {
	p := mediaCacheKeyPath(key)
	fi, err := os.Stat(p)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	return p
}

// serveMediaCacheFile отдаёт файл через http.ServeContent (Range/206/If-*).
// false — файла нет или не открылся: вызывающий идёт на апстрим.
func serveMediaCacheFile(c *gin.Context, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ct := http.DetectContentType(head[:n])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	wh := c.Writer.Header()
	wh.Set("Content-Type", ct)
	wh.Set("Accept-Ranges", "bytes")
	wh.Set("Cache-Control", proxyCacheControl(c))
	http.ServeContent(c.Writer, c.Request, "", fi.ModTime(), f)
	return true
}

// mediaCacheSink пишет тело апстрима во временный файл; commit переименовывает
// его в кэш только если загрузка дошла до конца. Write никогда не рвёт
// основной стрим: ошибки кэша глотаются (кэш — best effort). Запись
// буферизованная, чтобы дисковые операции не тормозили стрим клиенту.
type mediaCacheSink struct {
	key string
	f   *os.File
	w   *bufio.Writer
	n   int64
}

func newMediaCacheSink(key string) *mediaCacheSink {
	mediaCacheInit()
	sum := sha256.Sum256([]byte(key))
	f, err := os.CreateTemp(mediaCacheDir, hex.EncodeToString(sum[:8])+"-*.tmp")
	if err != nil {
		return nil
	}
	return &mediaCacheSink{key: key, f: f, w: bufio.NewWriterSize(f, 256<<10)}
}

func (s *mediaCacheSink) Write(p []byte) (int, error) {
	if s == nil || s.f == nil {
		return len(p), nil
	}
	if s.n+int64(len(p)) > mediaCacheMaxItem {
		s.abort() // слишком большой — перестаём писать, стрим не трогаем
		return len(p), nil
	}
	n, err := s.w.Write(p)
	s.n += int64(n)
	if err != nil {
		s.abort()
	}
	return len(p), nil
}

func (s *mediaCacheSink) commit() {
	if s == nil {
		return
	}
	if s.f == nil || s.n == 0 {
		s.abort()
		return
	}
	tmp := s.f.Name()
	if err := s.w.Flush(); err != nil {
		s.abort()
		return
	}
	s.f.Close()
	s.f = nil
	final := mediaCacheKeyPath(s.key)
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return
	}
	mediaCacheEvict()
}

func (s *mediaCacheSink) abort() {
	if s == nil || s.f == nil {
		return
	}
	tmp := s.f.Name()
	s.f.Close()
	s.f = nil
	os.Remove(tmp)
}

// mediaCacheEvict держит суммарный размер кэша в бюджете, выкидывая
// самые старые по mtime файлы. Сканирование каталога троттлится: коммит
// происходит на каждое просмотренное видео, а O(n) обход тут не нужен.
var (
	mediaCacheEvictInterval = time.Minute
	lastMediaCacheEvict     atomic.Int64
)

func mediaCacheEvict() {
	now := time.Now().UnixNano()
	last := lastMediaCacheEvict.Load()
	if last != 0 && time.Duration(now-last) < mediaCacheEvictInterval {
		return
	}
	if !lastMediaCacheEvict.CompareAndSwap(last, now) {
		return
	}
	entries, err := os.ReadDir(mediaCacheDir)
	if err != nil {
		return
	}
	type fe struct {
		name string
		size int64
		mod  time.Time
	}
	var total int64
	list := make([]fe, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		total += fi.Size()
		list = append(list, fe{e.Name(), fi.Size(), fi.ModTime()})
	}
	if total <= mediaCacheMaxDiskBytes() {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.Before(list[j].mod) })
	for _, f := range list {
		if total <= mediaCacheMaxDiskBytes() {
			break
		}
		if os.Remove(filepath.Join(mediaCacheDir, f.name)) == nil {
			total -= f.size
		}
	}
}

// ── Сегментированная параллельная закачка видео ─────────────────────────
// Один поток к «медленным» CDN даёт узкую трубу; N параллельных
// byte-range запросов качают тот же файл в N раз быстрее. Сегменты пишутся
// по смещениям в один temp-файл media-cache, клиенту байты отдаются
// по порядку по мере готовности сегментов (первый сегмент — уже через 1/N
// файла). Файл, закачанный целиком, коммитится в кэш: повторные просмотры
// и перемотка обслуживаются локально. Выключается BRIEFLY_SEG=0.

var (
	segParallel          = 4        // сегментов на файл
	segParallelBig       = 6        // для файлов от 64 МБ
	segMinSize     int64 = 16 << 20 // меньше — обычный стрим
	segProbeMax    int64 = 4 << 20  // лимит чтения «мусора» после пробы
)

func segmentedEnabled() bool { return os.Getenv("BRIEFLY_SEG") != "0" }

func isVideoURL(u *url.URL) bool {
	switch strings.ToLower(filepath.Ext(u.Path)) {
	case ".mp4", ".webm", ".mov", ".mkv", ".avi", ".flv", ".m4v":
		return true
	}
	return false
}

// streamSegmented качает файл сегментами и параллельно отдаёт клиенту.
// true — ответ клиенту отправлен (или начат и оборван из-за ошибки);
// false — не смогли даже начать (CDN без Range, сорвалась проба):
// вызывающий идёт обычным стримом.
func (h *Handler) streamSegmented(c *gin.Context, u *url.URL, cc string) bool {
	// Проба: Range-запрос первого байта даёт полный размер из Content-Range.
	probe, err := h.proxyUpstream(c, u, "bytes=0-0")
	if err != nil {
		return false
	}
	io.Copy(io.Discard, io.LimitReader(probe.Body, segProbeMax))
	probe.Body.Close()
	if probe.StatusCode != http.StatusPartialContent {
		return false
	}
	ct := probe.Header.Get("Content-Type")
	cr := probe.Header.Get("Content-Range") // "bytes 0-0/123456"
	idx := strings.LastIndex(cr, "/")
	if idx < 0 {
		return false
	}
	total, perr := strconv.ParseInt(strings.TrimSpace(cr[idx+1:]), 10, 64)
	if perr != nil || total < segMinSize || total > mediaCacheMaxItem {
		return false
	}

	segs := segParallel
	if total >= 64<<20 {
		segs = segParallelBig
	}
	segSize := (total + int64(segs) - 1) / int64(segs)

	mediaCacheInit()
	sum := sha256.Sum256([]byte(u.String()))
	tmp, err := os.CreateTemp(mediaCacheDir, hex.EncodeToString(sum[:8])+"-*.tmp")
	if err != nil {
		return false
	}
	cleanup := func() {
		name := tmp.Name()
		tmp.Close()
		os.Remove(name)
	}
	if err := tmp.Truncate(total); err != nil {
		cleanup()
		return false
	}

	ctx := c.Request.Context()
	segCtx, cancelSegs := context.WithCancel(ctx)
	defer cancelSegs()
	referer := h.refererForHost(u.Hostname())
	doneChans := make([]chan error, segs)
	var wg sync.WaitGroup
	for i := 0; i < segs; i++ {
		start := int64(i) * segSize
		end := start + segSize - 1
		if end >= total {
			end = total - 1
		}
		doneChans[i] = make(chan error, 1)
		wg.Add(1)
		go func(i int, start, end int64) {
			defer wg.Done()
			doneChans[i] <- fetchSegment(segCtx, u, referer, start, end, tmp)
		}(i, start, end)
	}

	// Отдаём клиенту по порядку, дожидаясь готовности каждого сегмента.
	wh := c.Writer.Header()
	wh.Set("Content-Type", ct)
	wh.Set("Accept-Ranges", "bytes")
	wh.Set("Content-Length", strconv.FormatInt(total, 10))
	wh.Set("Cache-Control", cc)
	c.Writer.WriteHeader(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	for i := 0; i < segs; i++ {
		var serr error
		select {
		case serr = <-doneChans[i]:
		case <-ctx.Done():
			wg.Wait()
			cleanup()
			return true
		}
		if serr != nil {
			// Сегмент сорвался после старта ответа: остальные сегменты
			// отменяем и быстро завершаем (клиент повторит запрос; кэш
			// не коммитится).
			cancelSegs()
			wg.Wait()
			cleanup()
			return true
		}
		start := int64(i) * segSize
		end := start + segSize - 1
		if end >= total {
			end = total - 1
		}
		if cerr := copySegmentRegion(c.Writer, tmp, start, end, flusher); cerr != nil {
			cancelSegs()
			wg.Wait()
			cleanup()
			return true
		}
	}
	wg.Wait()

	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return true
	}
	if err := os.Rename(name, mediaCacheKeyPath(u.String())); err != nil {
		os.Remove(name)
		return true
	}
	mediaCacheEvict()
	return true
}

// fetchSegment качает один byte-range в общий temp-файл по смещению.
func fetchSegment(ctx context.Context, u *url.URL, referer string, start, end int64, f *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Referer", referer)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := MediaHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("segment %d-%d: upstream status %d", start, end, resp.StatusCode)
	}
	buf := make([]byte, 128<<10)
	off := start
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return werr
			}
			off += int64(n)
		}
		if rerr == io.EOF {
			if off != end+1 {
				return fmt.Errorf("segment %d-%d: short read (%d bytes)", start, end, off-start)
			}
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// copySegmentRegion отдаёт клиенту готовый отрезок temp-файла.
func copySegmentRegion(w io.Writer, f *os.File, start, end int64, flusher http.Flusher) error {
	rd := io.NewSectionReader(f, start, end-start+1)
	buf := make([]byte, 64<<10)
	pending := 0
	for {
		n, rerr := rd.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			pending += n
			if flusher != nil && pending >= 256<<10 {
				flusher.Flush()
				pending = 0
			}
		}
		if rerr == io.EOF {
			if flusher != nil && pending > 0 {
				flusher.Flush()
			}
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
