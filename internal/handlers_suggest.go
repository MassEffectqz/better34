package internal

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < 1 {
		c.JSON(http.StatusOK, gin.H{"tags": []interface{}{}})
		return
	}
	db := GetDB()
	tags := db.SuggestTagsLocal(q, 8)
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

func (h *Handler) SuggestTags(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" || len(q) < 2 {
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

	tagCountCache.RLock()
	for _, tag := range tagList {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) < 2 {
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
