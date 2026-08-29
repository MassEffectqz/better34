package internal

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	providers  map[string]Provider
	downloader *Downloader
}

func NewHandler() *Handler {
	return &Handler{
		providers: map[string]Provider{
			rule34Site.name:    NewRule34Client(),
			gelbooruSite.name:  NewGelbooruClient(),
			safebooruSite.name: NewSafebooruClient(),
			hypnohubSite.name:  NewHypnohubClient(),
		},
	}
}

// provider возвращает активный источник постов (выбирается в настройках,
// по умолчанию rule34). Переключение действует без рестарта.
func (h *Handler) provider() Provider {
	name := GetConfig().GetProvider()
	if h.providers != nil {
		if p, ok := h.providers[name]; ok {
			return p
		}
		if p, ok := h.providers[defaultProviderName]; ok {
			return p
		}
	}
	return nil
}

func (h *Handler) SetDownloader(d *Downloader) {
	h.downloader = d
}

// effectiveMaxQueryLen — безопасная длина tags для активного режима поиска.
// В режиме «Все сайты» действует самый строгий лимит среди провайдеров.
func (h *Handler) effectiveMaxQueryLen() int {
	if GetConfig().GetProvider() != allProvidersName {
		return h.provider().MaxQueryLen()
	}
	minLen := 0
	for _, name := range multiProviderOrder {
		if p, ok := h.providers[name]; ok {
			n := p.MaxQueryLen()
			if minLen == 0 || n < minLen {
				minLen = n
			}
		}
	}
	if minLen == 0 {
		return h.provider().MaxQueryLen()
	}
	return minLen
}

// refererForFileURL подбирает Referer под хост файла поста: в режиме
// «Все сайты» (и после смены источника) активный сайт не совпадает с
// сайтом конкретного поста.
func (h *Handler) refererForFileURL(fileURL string) string {
	if u, err := url.Parse(fileURL); err == nil && u.Hostname() != "" {
		return h.refererForHost(u.Hostname())
	}
	return h.provider().RefererURL()
}

func (h *Handler) SearchPosts(c *gin.Context) {
	tags := c.Query("tags")

	// Фильтр рейтинга (All/SFW/18+): метатеги уходят в начало запроса
	// (внутри бюджета MaxQueryLen), плюс посты фильтруются локально.
	ratingTerms, ratingExcl := ratingFilter(c.Query("rating"))
	if len(ratingTerms) > 0 {
		tags = strings.TrimSpace(strings.Join(ratingTerms, " ") + " " + tags)
	}

	profile := ProfileFor(c)
	profile.mu.RLock()
	hiddenTags := make([]string, 0, len(profile.HiddenTags))
	for tag := range profile.HiddenTags {
		hiddenTags = append(hiddenTags, tag)
	}
	profile.mu.RUnlock()
	sort.Strings(hiddenTags)

	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "40")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 || limit > 100 {
		limit = 40
	}

	cfg := GetConfig()
	minID := cfg.GetMinID()
	if midStr := c.Query("min_id"); midStr != "" {
		if v, err := strconv.Atoi(midStr); err == nil {
			minID = v
		}
	}

	// Режим «Все сайты»: параллельный опрос всех провайдеров со слиянием
	// выдачи (дедуп по md5-хэшу файла и защита от коллизий числовых id).
	multi := cfg.GetProvider() == allProvidersName
	provs := make([]Provider, 0, len(h.providers))
	if multi {
		for _, name := range multiProviderOrder {
			if p, ok := h.providers[name]; ok {
				provs = append(provs, p)
			}
		}
		if len(provs) <= 1 {
			multi = false
		}
	}

	type qResult struct {
		query   string
		omitted []string
	}
	maxQueryLen := h.provider().MaxQueryLen()
	if multi {
		for _, pr := range provs {
			if n := pr.MaxQueryLen(); n < maxQueryLen {
				maxQueryLen = n
			}
		}
	}
	buildQuery := func(q string) qResult {
		seen := make(map[string]bool)
		var omitted []string
		for _, t := range hiddenTags {
			// Приводим к нижнему регистру: посты приходят от API в lowercase,
			// а filterOmitted сравнивает токены точно (см. GetLocalPosts).
			norm := strings.ToLower(strings.ReplaceAll(strings.TrimLeft(t, "+-"), "&#039;", "'"))
			if seen[norm] {
				continue
			}
			seen[norm] = true
			add := " -" + norm
			if len(q)+len(add) > maxQueryLen {
				omitted = append(omitted, norm)
				continue
			}
			q += add
		}
		return qResult{query: strings.TrimSpace(q), omitted: omitted}
	}

	filterOmitted := func(posts []Rule34Post, omitted []string) []Rule34Post {
		if len(omitted) == 0 {
			return posts
		}
		keep := posts[:0]
		for _, p := range posts {
			bad := false
			for _, ht := range omitted {
				for _, tok := range strings.Fields(strings.ToLower(p.Tags)) {
					if strings.ReplaceAll(tok, "&#039;", "'") == ht {
						bad = true
						break
					}
				}
				if bad {
					break
				}
			}
			if !bad {
				keep = append(keep, p)
			}
		}
		return keep
	}


	var posts []Rule34Post

	// searchAcross опрашивает провайдеров параллельно (один вызов на каждый),
	// возвращает списки постов и признак «хотя бы один ответил».
	searchAcross := func(query string, perProvLimit int) ([][]Rule34Post, bool) {
		lists := make([][]Rule34Post, len(provs))
		errs := make([]error, len(provs))
		var wg sync.WaitGroup
		for i, pr := range provs {
			wg.Add(1)
			go func(i int, pr Provider) {
				defer wg.Done()
				ps, e := pr.SearchPosts(query, page, perProvLimit, minID)
				lists[i], errs[i] = ps, e
			}(i, pr)
		}
		wg.Wait()
		ok := false
		for _, e := range errs {
			if e == nil {
				ok = true
			}
		}
		return lists, ok
	}

	// mergeMulti сливает выдачу сайтов «по кругу», выкидывая дубликаты
	// файлов по md5-хэшу и коллизии числовых id между сайтами.
	mergeMulti := func(lists [][]Rule34Post) []Rule34Post {
		total := 0
		for _, l := range lists {
			total += len(l)
		}
		out := make([]Rule34Post, 0, total)
		seenMD5 := make(map[string]bool, total)
		seenID := make(map[int]bool, total)
		for pos := 0; ; pos++ {
			empty := true
			for _, l := range lists {
				if pos < len(l) {
					empty = false
					p := l[pos]
					if !seenID[p.ID] && (p.Hash == "" || !seenMD5[p.Hash]) {
						seenID[p.ID] = true
						if p.Hash != "" {
							seenMD5[p.Hash] = true
						}
						out = append(out, p)
					}
				}
			}
			if empty {
				break
			}
		}
		return out
	}

	if strings.Contains(tags, "|") {
		parts := strings.Split(tags, "|")
		n := len(parts)
		totalTarget := limit * 2
		perPartLimit := (totalTarget + n - 1) / n
		if perPartLimit < 1 {
			perPartLimit = 1
		}
		calls := 0
		failedCalls := 0
		var partResults [][]Rule34Post
		for _, part := range parts {
			rq := buildQuery(strings.TrimSpace(part))
			if multi {
				lists, anyOK := searchAcross(rq.query, perPartLimit)
				calls++
				if !anyOK {
					failedCalls++
					continue
				}
				partResults = append(partResults, filterOmitted(mergeMulti(lists), rq.omitted))
			} else {
				calls++
				p, e := h.provider().SearchPosts(rq.query, page, perPartLimit, minID)
				if e != nil {
					failedCalls++
					continue
				}
				partResults = append(partResults, filterOmitted(p, rq.omitted))
			}
		}
		// Частичный сбой даёт неполные результаты, но когда упали ВСЕ части —
		// молча отдавать «ничего не найдено» нельзя: сообщаем об ошибке.
		if failedCalls == calls {
			c.JSON(http.StatusBadGateway, gin.H{"error": "источник постов недоступен: все части запроса завершились ошибкой"})
			return
		}
		// Детерминированное слияние групп «по кругу»: в отличие от прежнего
		// rand.Shuffle порядок стабилен между запросами страниц (иначе
		// пагинация дублировала/теряла посты).
		posts = mergeMulti(partResults)
	} else {
		rq := buildQuery(tags)
		if multi {
			lists, anyOK := searchAcross(rq.query, limit)
			if !anyOK {
				c.JSON(http.StatusBadGateway, gin.H{"error": "источники постов недоступны: все сайты завершили запрос ошибкой"})
				return
			}
			posts = mergeMulti(lists)
		} else {
			var err error
			posts, err = h.provider().SearchPosts(rq.query, page, limit, minID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		posts = filterOmitted(posts, rq.omitted)
	}

	if len(ratingExcl) > 0 {
		kept := posts[:0]
		for _, p := range posts {
			if !ratingExcl[strings.ToLower(p.Rating)] {
				kept = append(kept, p)
			}
		}
		posts = kept
	}

	db := GetDB()
	enriched := make([]gin.H, 0)
	for _, p := range posts {
		entry := gin.H{
			"id":          p.ID,
			"tags":        p.Tags,
			"file_url":    p.FileURL,
			"preview_url": p.PreviewURL,
			"width":       p.Width,
			"height":      p.Height,
			"file_size":   p.FileSize,
			"file_type":   p.FileType,
			"score":       p.Score,
			"rating":      p.Rating,
			"downloaded":  false,
		}

		if existing := db.Get(p.ID); existing != nil {
			entry["downloaded"] = existing.Downloaded
			entry["file_path"] = existing.FilePath
			if existing.ThumbPath != "" {
				entry["thumb_path"] = existing.ThumbPath
			}
		}

		db.UpsertMeta(&Post{
			ID:         p.ID,
			Tags:       p.Tags,
			FileURL:    p.FileURL,
			PreviewURL: p.PreviewURL,
			FileType:   p.FileType,
			Width:      p.Width,
			Height:     p.Height,
			FileSize:   p.FileSize,
			Score:      p.Score,
			Rating:     p.Rating,
			MD5:        p.Hash,
		})

		enriched = append(enriched, entry)
	}

	sortedCopy := make([]gin.H, len(enriched))
	copy(sortedCopy, enriched)
	sort.Slice(sortedCopy, func(i, j int) bool { return sortedCopy[i]["id"].(int) < sortedCopy[j]["id"].(int) })
	body, _ := json.Marshal(gin.H{"posts": sortedCopy, "page": page, "limit": limit})
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum[:16])
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=60")
	if inm := c.Request.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimPrefix(strings.TrimSpace(part), "W/")
		if p == etag || p == "*" {
			return true
		}
	}
	return false
}

func (h *Handler) GetPostsByIDs(c *gin.Context) {
	idsStr := c.Query("ids")
	if idsStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids required"})
		return
	}

	parts := strings.Split(idsStr, ",")
	var ids []int
	for _, p := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid ids"})
		return
	}

	if len(ids) > 2000 {
		ids = ids[:2000]
	}

	db := GetDB()
	enriched := make([]gin.H, 0)

	var apiIDs []int
	for _, id := range ids {
		existing := db.Get(id)
		if existing != nil && existing.PreviewURL != "" {
			entry := gin.H{
				"id":          existing.ID,
				"tags":        existing.Tags,
				"file_url":    existing.FileURL,
				"preview_url": existing.PreviewURL,
				"width":       existing.Width,
				"height":      existing.Height,
				"file_size":   existing.FileSize,
				"file_type":   existing.FileType,
				"score":       existing.Score,
				"rating":      existing.Rating,
				"downloaded":  existing.Downloaded,
			}
			if existing.ThumbPath != "" {
				entry["thumb_path"] = existing.ThumbPath
			}
			enriched = append(enriched, entry)
		} else {
			apiIDs = append(apiIDs, id)
		}
	}

	if len(apiIDs) > 0 {
		prov := h.provider()
		upsert := func(p Rule34Post) {
			db.UpsertMeta(&Post{
				ID:         p.ID,
				Tags:       p.Tags,
				FileURL:    p.FileURL,
				PreviewURL: p.PreviewURL,
				FileType:   p.FileType,
				Width:      p.Width,
				Height:     p.Height,
				FileSize:   p.FileSize,
				Score:      p.Score,
				Rating:     p.Rating,
				MD5:        p.Hash,
			})
			enriched = append(enriched, gin.H{
				"id":          p.ID,
				"tags":        p.Tags,
				"file_url":    p.FileURL,
				"preview_url": p.PreviewURL,
				"width":       p.Width,
				"height":      p.Height,
				"file_size":   p.FileSize,
				"file_type":   p.FileType,
				"score":       p.Score,
				"rating":      p.Rating,
				"downloaded":  false,
			})
		}

		if bc, ok := prov.(*booruClient); ok && bc.spec.batchIDs {
			// Пакетный режим: rule34/gelbooru принимают список id и
			// возвращают именно запрошенные посты.
			const idsPerBatch = 50
			for start := 0; start < len(apiIDs); start += idsPerBatch {
				end := start + idsPerBatch
				if end > len(apiIDs) {
					end = len(apiIDs)
				}
				batch := apiIDs[start:end]
				want := make(map[int]bool, len(batch))
				for _, id := range batch {
					want[id] = true
				}
				idTags := make([]string, len(batch))
				for i, id := range batch {
					idTags[i] = strconv.Itoa(id)
				}
				posts, e := prov.SearchPosts("id:"+strings.Join(idTags, ","), 1, len(batch), 0)
				if e != nil {
					continue
				}
				for _, p := range posts {
					if !want[p.ID] {
						continue // страховка от сайтов, игнорирующих id-список
					}
					upsert(p)
				}
			}
		} else if prefix := providerSingleID(prov); prefix != "" {
			// Сайт игнорирует списки id (safebooru): одиночные запросы
			// tags=id:N с ограниченным параллелизмом; порядок — как в запросе.
			fetched := make(map[int]Rule34Post, len(apiIDs))
			var mu sync.Mutex
			var wg sync.WaitGroup
			sem := make(chan struct{}, 8)
			for _, id := range apiIDs {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					posts, e := prov.SearchPosts(prefix+strconv.Itoa(id), 1, 1, 0)
					if e != nil {
						return
					}
					for _, p := range posts {
						if p.ID != id {
							continue
						}
						mu.Lock()
						fetched[id] = p
						mu.Unlock()
						return
					}
				}(id)
			}
			wg.Wait()
			for _, id := range apiIDs {
				if p, ok := fetched[id]; ok {
					upsert(p)
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"posts": enriched,
		"page":  1,
		"limit": len(enriched),
	})
}

func (h *Handler) GetPost(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	db := GetDB()
	if post := db.Get(id); post != nil {
		c.JSON(http.StatusOK, post)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
}

func (h *Handler) GetLocalPosts(c *gin.Context) {
	tags := c.Query("tags")
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "40")

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(limitStr)
	if limit < 1 || limit > 100 {
		limit = 40
	}

	db := GetDB()
	var posts []*Post
	if tags != "" {
		posts = db.SearchDownloaded(tags)
	} else {
		posts = db.GetDownloaded()
	}

	profile := ProfileFor(c)
	profile.mu.RLock()
	hiddenLocal := make([]string, 0, len(profile.HiddenTags))
	for t := range profile.HiddenTags {
		hiddenLocal = append(hiddenLocal, strings.ToLower(strings.TrimLeft(t, "+-")))
	}
	profile.mu.RUnlock()

	if len(hiddenLocal) > 0 {
		filtered := make([]*Post, 0, len(posts))
	outer:
		for _, p := range posts {
			tagSet := make(map[string]struct{}, 16)
			for _, t := range strings.Fields(strings.ToLower(p.Tags)) {
				tagSet[t] = struct{}{}
			}
			for _, ht := range hiddenLocal {
				if _, ok := tagSet[ht]; ok {
					continue outer
				}
			}
			filtered = append(filtered, p)
		}
		posts = filtered
	}

	minID := GetConfig().GetMinID()
	if midStr := c.Query("min_id"); midStr != "" {
		if v, err := strconv.Atoi(midStr); err == nil {
			minID = v
		}
	}
	if minID > 0 {
		filtered := posts[:0]
		for _, p := range posts {
			if p.ID >= minID {
				filtered = append(filtered, p)
			}
		}
		posts = filtered
	}

	start := (page - 1) * limit
	if start >= len(posts) {
		c.JSON(http.StatusOK, gin.H{"posts": []interface{}{}, "page": page, "limit": limit, "total": len(posts)})
		return
	}

	end := start + limit
	if end > len(posts) {
		end = len(posts)
	}

	pagePosts := posts[start:end]
	var result []gin.H
	for _, p := range pagePosts {
		thumbPath := ""
		if p.ThumbPath != "" {
			rel, _ := filepath.Rel("data", p.ThumbPath)
			thumbPath = rel
		}
		if thumbPath == "" {
			thumbPath = fmt.Sprintf("thumbs/%d.jpg", p.ID)
		}

		result = append(result, gin.H{
			"id":         p.ID,
			"tags":       p.Tags,
			"file_type":  p.FileType,
			"width":      p.Width,
			"height":     p.Height,
			"file_size":  p.FileSize,
			"score":      p.Score,
			"downloaded": true,
			"file_path":  p.FilePath,
			"thumb_path": thumbPath,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"posts": result,
		"page":  page,
		"limit": limit,
		"total": len(posts),
	})
}
