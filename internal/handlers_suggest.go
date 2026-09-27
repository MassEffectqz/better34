package internal

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

var suggestCache = struct {
	sync.RWMutex
	m   map[string]cachedSuggest
	ttl time.Duration
}{m: make(map[string]cachedSuggest), ttl: 5 * time.Minute}

const suggestCacheMax = 512

type cachedSuggest struct {
	tags      []TagSuggestion
	expiresAt time.Time
}

func suggestCachePut(q string, tags []TagSuggestion) {
	suggestCache.Lock()
	defer suggestCache.Unlock()
	if _, ok := suggestCache.m[q]; !ok && len(suggestCache.m) >= suggestCacheMax {
		now := time.Now()
		var oldestKey string
		var oldest time.Time
		for k, c := range suggestCache.m {
			if now.After(c.expiresAt) {
				delete(suggestCache.m, k)
				continue
			}
			if oldest.IsZero() || c.expiresAt.Before(oldest) {
				oldestKey, oldest = k, c.expiresAt
			}
		}
		if len(suggestCache.m) >= suggestCacheMax && oldestKey != "" {
			delete(suggestCache.m, oldestKey)
		}
	}
	suggestCache.m[q] = cachedSuggest{tags: tags, expiresAt: time.Now().Add(suggestCache.ttl)}
}

func (h *Handler) SuggestLocal(c *gin.Context) {
	q := suggestQueryPrefix(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"tags": []interface{}{}})
		return
	}
	db := GetDB()
	c.JSON(http.StatusOK, gin.H{"tags": db.SuggestTagsLocalFor(q, 8, suggestSourceFilter(h))})
}

// suggestSourceFilter — метка источника для фильтрации локальных
// подсказок. В режиме «все сайты» (и если провайдер неизвестен) фильтр
// пустой: показываем теги всей базы.
func suggestSourceFilter(h *Handler) string {
	p := h.provider()
	if p == nil || p.Name() == allProvidersName {
		return ""
	}
	return p.Name()
}

func (h *Handler) SuggestTags(c *gin.Context) {
	q := suggestQueryPrefix(c.Query("q"))
	// Считаем по рунам, а не байтам: один кириллический символ — 2 байта.
	if utf8.RuneCountInString(q) < 2 {
		c.JSON(http.StatusOK, gin.H{"tags": []interface{}{}})
		return
	}

	// Ключ включает активный источник: после смены провайдера старые
	// подсказки другого сайта не должны отдаваться из кэша.
	cacheKey := h.provider().Name() + "\x1f" + q

	suggestCache.RLock()
	if cached, ok := suggestCache.m[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		suggestCache.RUnlock()
		c.JSON(http.StatusOK, gin.H{"tags": cached.tags})
		return
	}
	suggestCache.RUnlock()

	tags, err := h.provider().SuggestTags(q)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"tags": []interface{}{}})
		return
	}

	suggestCachePut(cacheKey, tags)

	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

var tagCountCache = struct {
	sync.RWMutex
	m  map[string]int
	ts map[string]time.Time
}{m: make(map[string]int), ts: make(map[string]time.Time)}

const tagCountTTL = 12 * time.Hour
const tagCountCacheMax = 2000

// ── Персист счётчиков тегов ─────────────────────────────────────────────
// Без файла после каждого рестарта кэш пуст и первые запросы заново
// штурмуют autocomplete («будут из кэша» в логах). Формат — тот же
// подход, что у поискового кэша: атомарная запись JSON с debounce.

var (
	tagCountsFile        = filepath.Join("data", "cache", "tag_counts.json")
	tagCountLoadOnce     sync.Once
	tagCountsSaveMu      sync.Mutex
	tagCountsSavePending atomic.Bool
)

func tagCountsLoad() {
	tagCountLoadOnce.Do(func() {
		data, err := os.ReadFile(tagCountsFile)
		if err != nil {
			return
		}
		var snap struct {
			Entries map[string]struct {
				Count int       `json:"count"`
				Saved time.Time `json:"saved"`
			} `json:"entries"`
		}
		if json.Unmarshal(data, &snap) != nil {
			return
		}
		now := time.Now()
		tagCountCache.Lock()
		for k, e := range snap.Entries {
			if e.Count <= 0 || now.Sub(e.Saved) >= tagCountTTL {
				continue
			}
			tagCountCache.m[k] = e.Count
			tagCountCache.ts[k] = e.Saved
		}
		tagCountCache.Unlock()
	})
}

func tagCountsScheduleSave() {
	if !tagCountsSavePending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		time.Sleep(3 * time.Second) // собрать пачку записей
		tagCountsSavePending.Store(false)
		tagCountsSave()
	}()
}

func tagCountsSave() {
	now := time.Now()
	type entry struct {
		Count int       `json:"count"`
		Saved time.Time `json:"saved"`
	}
	snap := struct {
		Entries map[string]entry `json:"entries"`
	}{Entries: make(map[string]entry)}
	tagCountCache.RLock()
	for k, cnt := range tagCountCache.m {
		ts := tagCountCache.ts[k]
		if now.Sub(ts) >= tagCountTTL {
			continue
		}
		snap.Entries[k] = entry{Count: cnt, Saved: ts}
	}
	tagCountCache.RUnlock()

	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	tagCountsSaveMu.Lock()
	defer tagCountsSaveMu.Unlock()
	atomicWriteFile(tagCountsFile, data, 0644)
}

func tagCountStore(provider, tag string, count int) {
	tagCountCache.Lock()
	defer tagCountCache.Unlock()
	key := provider + "|" + tag
	if _, ok := tagCountCache.m[key]; !ok && len(tagCountCache.m) >= tagCountCacheMax {
		now := time.Now()
		var oldestTag string
		var oldest time.Time
		for t, ts := range tagCountCache.ts {
			if now.Sub(ts) >= tagCountTTL {
				delete(tagCountCache.m, t)
				delete(tagCountCache.ts, t)
				continue
			}
			if oldest.IsZero() || ts.Before(oldest) {
				oldestTag, oldest = t, ts
			}
		}
		if len(tagCountCache.m) >= tagCountCacheMax && oldestTag != "" {
			delete(tagCountCache.m, oldestTag)
			delete(tagCountCache.ts, oldestTag)
		}
	}
	tagCountCache.m[key] = count
	tagCountCache.ts[key] = time.Now()
	tagCountsScheduleSave()
}

func (h *Handler) GetTagCounts(c *gin.Context) {
	tagsStr := c.Query("tags")
	if tagsStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tags required"})
		return
	}
	tagList := strings.Split(tagsStr, ",")

	result := make(map[string]int)
	var uncached []string
	now := time.Now()
	providerName := h.provider().Name()

	tagCountsLoad()

	tagCountCache.RLock()
	for _, tag := range tagList {
		tag = strings.TrimSpace(tag)
		// Пропускаем только пустые: односимвольные теги на бокорах валидны.
		if tag == "" {
			continue
		}
		key := providerName + "|" + tag
		if c, ok := tagCountCache.m[key]; ok && now.Sub(tagCountCache.ts[key]) < tagCountTTL {
			result[tag] = c
		} else {
			uncached = append(uncached, tag)
		}
	}
	tagCountCache.RUnlock()

	if len(uncached) == 0 {
		c.JSON(http.StatusOK, gin.H{"counts": result})
		return
	}

	// Ограничение фан-аута: autocomplete-API не умеет пакетный подсчёт,
	// поэтому на один запрос обрабатываем не более tagCountBatchMax тегов
	// (остальные подтянутся из 12-часового кэша следующими запросами).
	const tagCountBatchMax = 10
	if len(uncached) > tagCountBatchMax {
		log.Printf("tag-count: лимит %d тегов за запрос, %d пропущено (будут из кэша)", tagCountBatchMax, len(uncached)-tagCountBatchMax)
		uncached = uncached[:tagCountBatchMax]
	}

	sem := make(chan struct{}, 5)
	var mu sync.Mutex
	var wg sync.WaitGroup
	p := h.provider()
	for _, tag := range uncached {
		wg.Add(1)
		go func(tag string) {
			sem <- struct{}{}
			defer func() {
				<-sem
				wg.Done()
			}()
			cnt, err := p.GetTagCount(tag)
			if err == nil {
				tagCountStore(p.Name(), tag, cnt)
				mu.Lock()
				result[tag] = cnt
				mu.Unlock()
			} else {
				log.Printf("tag-count failed for %q: %v", tag, err)
			}
		}(tag)
	}
	wg.Wait()

	c.JSON(http.StatusOK, gin.H{"counts": result})
}

// ── Превью и смежные теги для всплывашки при наведении ─────────────────────

// TagPreview — срез библиотеки по одному тегу: счётчик постов, несколько
// обложек и теги, которые чаще всего встречаются вместе с ним.
type TagPreview struct {
	Tag     string            `json:"tag"`
	Count   int               `json:"count"`
	Posts   []*TagPreviewPost `json:"posts"`
	Related []*TagRelated     `json:"related"`
	// Source — что нашлось на бору, если в библиотеке по тегу пусто.
	Source *TagSourcePreview `json:"source,omitempty"`
}

// TagPreviewPost — обложка поста в мини-сетке превью.
type TagPreviewPost struct {
	ID         int    `json:"id"`
	Thumb      string `json:"thumb"`
	W          int    `json:"width"`
	H          int    `json:"height"`
	Downloaded bool   `json:"downloaded"` // false → превью с источника
	Site       string `json:"site,omitempty"`
}

// TagSourcePreview — результат поиска тега на активном источнике. Показываем
// его, только если в библиотеке по тегу ничего нет, и честно помечаем, что
// это не скачанные посты.
type TagSourcePreview struct {
	Site  string            `json:"site"`
	Query string            `json:"query"`
	Count int               `json:"count"`
	Posts []*TagPreviewPost `json:"posts"`
}

// TagRelated — смежный тег с числом совместных постов.
type TagRelated struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// GET /api/tags/:tag/preview?limit=6&related=8&source=0
//
// source=0 запрещает поиск на бору (для офлайна и тестов). По умолчанию,
// если в библиотеке по тегу нет ни одной обложки, подтягиваем превью с
// активного источника — иначе всплывашка упиралась бы в «ничего нет».
func (h *Handler) TagPreview(c *gin.Context) {
	tag := normalizeTagParam(c.Param("tag"))
	if tag == "" {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	limit := 6
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= 12 {
		limit = n
	}
	relLimit := 8
	if n, err := strconv.Atoi(c.Query("related")); err == nil && n >= 0 && n <= 20 {
		relLimit = n
	}
	out := TagPreview{Tag: tag}
	out.Count, out.Posts = GetDB().TagPreviewPosts(tag, limit)
	if relLimit > 0 {
		out.Related = GetDB().RelatedTags(tag, relLimit)
	}
	// Сначала библиотека; на бур идём, только если её нечего показать.
	if len(out.Posts) == 0 && c.Query("source") != "0" {
		out.Source = h.sourceTagPreview(tag, limit, c.Query("rating"))
	}
	c.JSON(http.StatusOK, out)
}

// sourceTagPreview ищет тег на активном источнике. Найденные посты
// записываем в БД метаданными — иначе по клику открыть их нечем (пост-by-ids
// смотрит в базу, а без строки вьюер не покажет теги), и поиск по такой строке
// потом найдёт их локально. Сами файлы не скачиваются.
func (h *Handler) sourceTagPreview(tag string, limit int, ratingMode string) *TagSourcePreview {
	prov := h.provider()
	if prov == nil {
		return nil
	}
	out := &TagSourcePreview{Site: prov.Name(), Query: tag}
	// Фильтр рейтинга — тот же, что у обычного поиска: превью не должно
	// показывать то, что пользователь запретил показывать.
	ratingTerms, ratingExcl := ratingFilter(ratingMode)
	query := tag
	if len(ratingTerms) > 0 {
		query = strings.Join(ratingTerms, " ") + " " + tag
	}
	posts, err := prov.SearchPosts(query, 1, limit, 0)
	if err != nil {
		// Сеть/лимит: показывать «ничего не нашлось» было бы враньём —
		// отдаём nil, и всплывашка честно скажет, что в библиотеке пусто.
		log.Printf("tag preview: поиск %q на %s не удался: %v", tag, prov.Name(), err)
		return nil
	}
	if len(ratingExcl) > 0 {
		// Сайт мог обрезать хвост запроса — досчищаем локально, как это делает
		// хендлер поиска. Нам хватает первых limit постов, а не полной страницы.
		kept := make([]Rule34Post, 0, len(posts))
		for _, p := range posts {
			if !ratingExcl[strings.ToLower(p.Rating)] {
				kept = append(kept, p)
			}
		}
		posts = kept
	}
	if len(posts) == 0 {
		return out
	}
	upserts := make([]*Post, 0, len(posts))
	out.Posts = make([]*TagPreviewPost, 0, limit)
	for i := range posts {
		p := &posts[i]
		upserts = append(upserts, &Post{
			ID: p.ID, Tags: p.Tags, FileURL: p.FileURL, PreviewURL: p.PreviewURL,
			FileType: p.FileType, Width: p.Width, Height: p.Height,
			FileSize: p.FileSize, Score: p.Score, Rating: p.Rating,
			MD5: p.Hash, Source: prov.Name(),
		})
		thumb := sourcePreviewURL(p)
		if thumb == "" {
			continue // без превью показывать нечего
		}
		out.Posts = append(out.Posts, &TagPreviewPost{
			ID: p.ID, Thumb: thumb, W: p.Width, H: p.Height, Site: prov.Name(),
		})
	}
	if len(upserts) > 0 {
		GetDB().UpsertMetaMany(upserts)
	}
	out.Count = len(out.Posts)
	return out
}

// sourcePreviewURL — превью с бура через прокси, как в ленте для
// нескачанных постов (наш /api/thumb отдаёт только для скачанных).
func sourcePreviewURL(p *Rule34Post) string {
	u := p.PreviewURL
	if u == "" {
		u = p.SampleURL
	}
	if u == "" {
		return ""
	}
	return "/api/proxy?url=" + url.QueryEscape(u) + "&kind=preview"
}

// normalizeTagParam приводит тег к каноническому виду: теги хранятся
// lowercase, пробелы заменены на подчёркивание (как в выдаче буров).
func normalizeTagParam(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "+", " ")
	s = strings.Join(strings.Fields(s), "_")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func (h *Handler) GetLocalTagStats(c *gin.Context) {
	db := GetDB()
	stats := db.TagStats(60)
	c.JSON(http.StatusOK, gin.H{"tags": stats})
}

var popularTagsCache = struct {
	sync.RWMutex
	provider string
	tags     []TagSuggestion
	ts       time.Time
}{}

const popularTagsTTL = 6 * time.Hour

// GET /api/tags/popular?limit=16 — самые популярные теги активного
// источника для фона экрана входа. Кэшируются на 6 часов.
func (h *Handler) GetPopularTags(c *gin.Context) {
	limit := 40
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 60 {
		limit = l
	}

	serve := func(tags []TagSuggestion) {
		if tags == nil {
			tags = []TagSuggestion{}
		}
		c.JSON(http.StatusOK, gin.H{"tags": tags})
	}

	prov := h.provider()

	popularTagsCache.RLock()
	fresh := prov.Name() == popularTagsCache.provider &&
		!popularTagsCache.ts.IsZero() && time.Since(popularTagsCache.ts) < popularTagsTTL
	cached := popularTagsCache.tags
	cachedProvider := popularTagsCache.provider
	popularTagsCache.RUnlock()
	if fresh {
		serve(cached)
		return
	}

	tags, err := prov.GetPopularTags(limit)
	if err != nil {
		log.Printf("popular tags failed: %v", err)
		if len(cached) > 0 && cachedProvider == prov.Name() {
			serve(cached)
			return
		}
		serve(nil)
		return
	}

	popularTagsCache.Lock()
	popularTagsCache.provider = prov.Name()
	popularTagsCache.tags = tags
	popularTagsCache.ts = time.Now()
	popularTagsCache.Unlock()
	serve(tags)
}
