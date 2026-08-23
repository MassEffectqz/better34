package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	minRecLikes    = 5
	recLikedWindow = 50
	recMaxTags     = 12
	recWeightsTTL  = 90 * time.Second
	recFreqTTL     = 5 * time.Minute
	recMaxQueryLen = 3700
)

type RecTagWeight struct {
	Tag    string  `json:"tag"`
	Weight float64 `json:"weight"`
}

// recWeightEntry — кэш весов тегов для одного пользователя.
type recWeightEntry struct {
	sig     string
	ts      time.Time
	weights []RecTagWeight
}

var (
	recWeightCache = struct {
		sync.RWMutex
		m map[string]recWeightEntry
	}{m: make(map[string]recWeightEntry)}
)

func invalidateRecWeights(user string) {
	recWeightCache.Lock()
	delete(recWeightCache.m, user)
	recWeightCache.Unlock()
}

// globalTagFreq — частотность тегов по всем постам локальной БД,
// приближение «популярности» тега. Кэшируется на recFreqTTL.
func globalTagFreq() map[string]int {
	recFreqCache.RLock()
	fresh := !recFreqCache.ts.IsZero() && time.Since(recFreqCache.ts) < recFreqTTL
	freq := recFreqCache.freq
	recFreqCache.RUnlock()
	if fresh {
		return freq
	}
	computed := GetDB().AllTagsFreq()
	recFreqCache.Lock()
	recFreqCache.freq = computed
	recFreqCache.ts = time.Now()
	recFreqCache.Unlock()
	return computed
}

var recFreqCache = struct {
	sync.RWMutex
	freq map[string]int
	ts   time.Time
}{}

// fetchPostsByIDs достаёт посты по id: из локальной БД, недостающие — из API.
func (h *Handler) fetchPostsByIDs(ids []int) []Rule34Post {
	db := GetDB()
	var posts []Rule34Post
	var apiIDs []int
	for _, id := range ids {
		if ex := db.Get(id); ex != nil && ex.PreviewURL != "" {
			posts = append(posts, Rule34Post{
				ID: ex.ID, Tags: ex.Tags, FileURL: ex.FileURL, PreviewURL: ex.PreviewURL,
				FileType: ex.FileType, Width: ex.Width, Height: ex.Height,
				FileSize: ex.FileSize, Score: ex.Score, Rating: ex.Rating,
			})
		} else {
			apiIDs = append(apiIDs, id)
		}
	}
	if len(apiIDs) == 0 {
		return posts
	}
	type result struct {
		post *Rule34Post
	}
	ch := make(chan result, len(apiIDs))
	sem := make(chan struct{}, 10)
	for _, postID := range apiIDs {
		sem <- struct{}{}
		go func(id int) {
			defer func() { <-sem }()
			found, e := h.provider().SearchPosts(fmt.Sprintf("id:%d", id), 1, 1, 0)
			if e != nil || len(found) == 0 {
				ch <- result{}
				return
			}
			ch <- result{&found[0]}
		}(postID)
	}
	for i := 0; i < len(apiIDs); i++ {
		r := <-ch
		if r.post != nil {
			posts = append(posts, *r.post)
		}
	}
	return posts
}

// recSig — отпечаток состояния профиля для инвалидации кэша весов.
func recSig(p *Profile) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]int, 0, len(p.LikedPosts))
	for id := range p.LikedPosts {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var hidden, disliked []string
	for t := range p.HiddenTags {
		hidden = append(hidden, t)
	}
	for t, n := range p.RecDisliked {
		disliked = append(disliked, fmt.Sprintf("%s:%d", t, n))
	}
	sort.Strings(hidden)
	sort.Strings(disliked)
	h := sha256.Sum256([]byte(strings.Join(append(
		intsToStrings(ids),
		append(hidden, disliked...)...), "|")))
	return hex.EncodeToString(h[:])
}

func intsToStrings(ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.Itoa(id)
	}
	return out
}

// computeRecWeights считает веса тегов: свежие лайки влияют сильнее,
// популярные теги штрафуются (TF-IDF-подобно), «мусорные» и скрытые
// исключаются, дизлайк-фидбек снимает теги с подборки.
func (h *Handler) computeRecWeights(p *Profile) ([]RecTagWeight, int, error) {
	p.mu.RLock()
	likedCount := len(p.LikedPosts)
	if likedCount < minRecLikes {
		p.mu.RUnlock()
		return nil, likedCount, fmt.Errorf("need_more_likes")
	}
	type likedEntry struct {
		id int
		ts int64
	}
	entries := make([]likedEntry, 0, likedCount)
	for id := range p.LikedPosts {
		entries = append(entries, likedEntry{id, p.LikedAt[id]})
	}
	hiddenTags := make([]string, 0, len(p.HiddenTags))
	for t := range p.HiddenTags {
		hiddenTags = append(hiddenTags, t)
	}
	disliked := make(map[string]int, len(p.RecDisliked))
	for t, n := range p.RecDisliked {
		disliked[t] = n
	}
	p.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ts != entries[j].ts {
			return entries[i].ts > entries[j].ts
		}
		return entries[i].id > entries[j].id
	})
	if len(entries) > recLikedWindow {
		entries = entries[:recLikedWindow]
	}

	ids := make([]int, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	posts := h.fetchPostsByIDs(ids)

	hidden := make(map[string]bool, len(hiddenTags))
	for _, t := range hiddenTags {
		hidden[t] = true
	}

	freq := globalTagFreq()
	n := float64(len(entries))
	score := make(map[string]float64)
	for r, post := range posts {
		recency := 1.0
		if n > 1 {
			recency = 1 - 0.5*float64(r)/n
		}
		for _, t := range strings.Fields(post.Tags) {
			if hidden[t] || genericTags[t] || disliked[t] > 0 {
				continue
			}
			score[t] += recency
		}
	}
	weights := make([]RecTagWeight, 0, len(score))
	for t, s := range score {
		pop := float64(freq[t])
		s = s / (1 + 0.9*math.Log10(1+pop))
		if s < 0.05 {
			continue
		}
		weights = append(weights, RecTagWeight{t, math.Round(s*1000) / 1000})
	}
	sort.Slice(weights, func(i, j int) bool { return weights[i].Weight > weights[j].Weight })
	if len(weights) > recMaxTags {
		weights = weights[:recMaxTags]
	}
	return weights, likedCount, nil
}

// recRareTags — редкие теги: самые «низкочастотные» (по локальной БД)
// среди топовых весов, за исключением первых трёх.
func recRareTags(weights []RecTagWeight) []string {
	freq := globalTagFreq()
	type wt struct {
		tag string
		w   float64
		pop int
	}
	pool := make([]wt, 0, len(weights))
	for i, w := range weights {
		if i < 3 {
			continue
		}
		pool = append(pool, wt{w.Tag, w.Weight, freq[w.Tag]})
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].pop != pool[j].pop {
			return pool[i].pop < pool[j].pop
		}
		return pool[i].w > pool[j].w
	})
	out := make([]string, 0, 4)
	for _, w := range pool {
		out = append(out, w.tag)
		if len(out) == 4 {
			break
		}
	}
	return out
}

// normalizeHiddenTag приводит тег к виду, в котором он встречается в постах.
func normalizeHiddenTag(t string) string {
	return strings.ReplaceAll(strings.TrimLeft(t, "+-"), "&#039;", "'")
}

// capHiddenTags делит скрытые теги на те, что попадут в rule34-запрос
// (пока запрос не превысит recMaxQueryLen — как в обычном поиске, иначе
// API возвращает пусто), и остальные, которые фильтруются постфактум
// по тегам постов.
func capHiddenTags(hidden []string) (inQuery []string, extra []string) {
	budget := 0
	for _, h := range hidden {
		norm := normalizeHiddenTag(h)
		if norm == "" {
			continue
		}
		add := len("-"+norm) + 1
		if budget+add > recMaxQueryLen {
			extra = append(extra, norm)
			continue
		}
		budget += add
		inQuery = append(inQuery, norm)
	}
	return inQuery, extra
}

// filterHiddenTags убирает посты, содержащие любой из скрытых тегов.
func filterHiddenTags(posts []Rule34Post, hidden []string) []Rule34Post {
	if len(hidden) == 0 {
		return posts
	}
	norm := make([]string, 0, len(hidden))
	seen := make(map[string]bool)
	for _, h := range hidden {
		t := normalizeHiddenTag(h)
		if t != "" && !seen[t] {
			seen[t] = true
			norm = append(norm, t)
		}
	}
	if len(norm) == 0 {
		return posts
	}
	keep := posts[:0]
	for _, p := range posts {
		bad := false
		for _, tok := range strings.Fields(p.Tags) {
			tt := strings.ReplaceAll(tok, "&#039;", "'")
			for _, h := range norm {
				if tt == h {
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

// recQueries строит пачку запросов для страницы: вращающийся срез + похожие
// на последний лайк + редкие теги. Возвращает список rule34-запросов и их
// пропорции (в порядке следования). Скрытые теги должны быть уже ограничены
// recMaxQueryLen символами.
func recQueries(weights []RecTagWeight, page int, likedPosts []Rule34Post, hidden []string) []struct {
	query string
	quota int
	label string
} {
	slice := (page - 1) % 3
	var anchor, or []string
	switch slice {
	case 0:
		anchor = []string{weights[0].Tag}
		if len(weights) > 1 {
			anchor = append(anchor, weights[1].Tag)
		}
		if len(weights) > 3 {
			or = []string{weights[2].Tag, weights[3].Tag}
		}
	case 1:
		if len(weights) > 3 {
			anchor = []string{weights[2].Tag, weights[3].Tag}
			if len(weights) > 5 {
				or = []string{weights[4].Tag, weights[5].Tag}
			}
		} else {
			anchor = []string{weights[0].Tag}
		}
	default:
		rare := recRareTags(weights)
		if len(rare) > 1 {
			anchor = []string{rare[0], rare[1]}
			if len(rare) > 3 {
				or = []string{rare[2], rare[3]}
			}
		} else {
			anchor = []string{weights[0].Tag}
		}
	}

	mk := func(tags []string) string {
		parts := append([]string{}, tags...)
		for _, h := range hidden {
			parts = append(parts, "-"+h)
		}
		return strings.Join(parts, " ")
	}

	queries := make([]struct {
		query string
		quota int
		label string
	}, 0, 5)
	queries = append(queries, struct {
		query string
		quota int
		label string
	}{mk(anchor), 40, "anchor"})
	for _, o := range or {
		queries = append(queries, struct {
			query string
			quota int
			label string
		}{mk([]string{o}), 20, "or"})
	}

	// похожие на последний лайк
	var lastPost *Rule34Post
	if len(likedPosts) > 0 {
		lastPost = &likedPosts[0]
	}
	if lastPost != nil {
		wset := make(map[string]float64, len(weights))
		for _, w := range weights {
			wset[w.Tag] = w.Weight
		}
		type tw struct {
			tag string
			w   float64
		}
		var cand []tw
		for _, t := range strings.Fields(lastPost.Tags) {
			if w, ok := wset[t]; ok {
				cand = append(cand, tw{t, w})
			}
		}
		sort.Slice(cand, func(i, j int) bool { return cand[i].w > cand[j].w })
		if len(cand) > 2 {
			cand = cand[:2]
		}
		if len(cand) > 0 {
			tags := make([]string, len(cand))
			for i, c := range cand {
				tags[i] = c.tag
			}
			queries = append(queries, struct {
				query string
				quota int
				label string
			}{mk(tags), 15, "related"})
		}
	}

	// свежие по редким тегам
	rareTags := recRareTags(weights)
	if len(rareTags) >= 2 {
		queries = append(queries, struct {
			query string
			quota int
			label string
		}{mk([]string{rareTags[0], rareTags[1]}), 10, "rare"})
	}
	return queries
}

func recEnrich(posts []Rule34Post) []gin.H {
	db := GetDB()
	out := make([]gin.H, 0, len(posts))
	for _, p := range posts {
		entry := gin.H{
			"id": p.ID, "tags": p.Tags, "file_url": p.FileURL,
			"preview_url": p.PreviewURL, "width": p.Width, "height": p.Height,
			"file_size": p.FileSize, "file_type": p.FileType,
			"score": p.Score, "rating": p.Rating, "downloaded": false,
		}
		if ex := db.Get(p.ID); ex != nil {
			entry["downloaded"] = ex.Downloaded
			if ex.FilePath != "" {
				entry["file_path"] = ex.FilePath
			}
			if ex.ThumbPath != "" {
				entry["thumb_path"] = ex.ThumbPath
			}
		}
		db.UpsertMeta(&Post{
			ID: p.ID, Tags: p.Tags, FileURL: p.FileURL, PreviewURL: p.PreviewURL,
			FileType: p.FileType, Width: p.Width, Height: p.Height,
			FileSize: p.FileSize, Score: p.Score, Rating: p.Rating,
		})
		out = append(out, entry)
	}
	return out
}

// GET /api/recommend?page=1&limit=60&exclude=id1,id2
func (h *Handler) Recommend(c *gin.Context) {
	page := 1
	if v, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && v > 0 {
		page = v
	}
	limit := 60
	if v, err := strconv.Atoi(c.DefaultQuery("limit", "60")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	exclude := make(map[int]bool)
	for _, s := range strings.Split(c.Query("exclude"), ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && id > 0 {
			exclude[id] = true
		}
	}

	profile := ProfileFor(c)
	user := c.GetString("briefly_user")

	// кэш весов
	sig := recSig(profile)
	recWeightCache.RLock()
	cached, ok := recWeightCache.m[user]
	fresh := ok && cached.sig == sig && time.Since(cached.ts) < recWeightsTTL
	var weights []RecTagWeight
	if fresh {
		weights = cached.weights
	}
	recWeightCache.RUnlock()

	if !fresh {
		var likedCount int
		var err error
		weights, likedCount, err = h.computeRecWeights(profile)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"error": "need_more_likes", "liked": likedCount, "need": minRecLikes - likedCount})
			return
		}
		recWeightCache.Lock()
		recWeightCache.m[user] = recWeightEntry{sig: sig, ts: time.Now(), weights: weights}
		recWeightCache.Unlock()
	}

	if len(weights) == 0 {
		c.JSON(http.StatusOK, gin.H{"posts": []gin.H{}, "tags": []RecTagWeight{}, "page": page, "slice": 0})
		return
	}

	// скрытые теги и скрытые/лайкнутые посты — исключаем
	profile.mu.RLock()
	hiddenTags := make([]string, 0, len(profile.HiddenTags))
	for t := range profile.HiddenTags {
		hiddenTags = append(hiddenTags, t)
	}
	skip := make(map[int]bool, len(profile.HiddenPosts)+len(profile.LikedPosts)+len(exclude))
	for id := range profile.HiddenPosts {
		skip[id] = true
	}
	for id := range profile.LikedPosts {
		skip[id] = true
	}
	for id := range exclude {
		skip[id] = true
	}
	profile.mu.RUnlock()

	// последние лайкнутые посты (для «похожих»)
	profile.mu.RLock()
	entries := make([]struct {
		id int
		ts int64
	}, 0, len(profile.LikedPosts))
	for id := range profile.LikedPosts {
		entries = append(entries, struct {
			id int
			ts int64
		}{id, profile.LikedAt[id]})
	}
	profile.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ts != entries[j].ts {
			return entries[i].ts > entries[j].ts
		}
		return entries[i].id > entries[j].id
	})
	likeIDs := make([]int, 0, 3)
	for _, e := range entries {
		likeIDs = append(likeIDs, e.id)
		if len(likeIDs) == 3 {
			break
		}
	}
	likedPosts := h.fetchPostsByIDs(likeIDs)

	inQueryHidden, extraHidden := capHiddenTags(hiddenTags)
	queries := recQueries(weights, page, likedPosts, inQueryHidden)
	if len(queries) == 0 {
		c.JSON(http.StatusOK, gin.H{"posts": []gin.H{}, "tags": weights, "page": page, "slice": (page - 1) % 3})
		return
	}

	type result struct {
		posts []Rule34Post
		err   error
	}
	ch := make(chan result, len(queries))
	for _, q := range queries {
		perPart := limit * q.quota / 100
		if perPart < 1 {
			perPart = 1
		}
		go func(query string) {
			p, e := h.provider().SearchPosts(query, page, perPart, 0)
			ch <- result{p, e}
		}(q.query)
	}

	seen := make(map[int]bool)
	posts := make([]Rule34Post, 0, limit)
	for i := 0; i < len(queries); i++ {
		r := <-ch
		if r.err != nil {
			continue
		}
		for _, p := range r.posts {
			if !seen[p.ID] && !skip[p.ID] {
				seen[p.ID] = true
				posts = append(posts, p)
			}
		}
	}

	// скрытые теги, не попавшие в rule34-запрос, отфильтровываем по тегам постов;
	// заодно страхуемся от постов со скрытыми тегами из любой части запроса.
	posts = filterHiddenTags(posts, append(inQueryHidden, extraHidden...))

	// если подборка пуста (слишком узкий якорь/переименованные теги) —
	// пробуем один топ-тег с ограниченным числом исключений
	if len(posts) == 0 {
		retry := weights[0].Tag
		parts := []string{retry}
		for _, h := range inQueryHidden {
			parts = append(parts, "-"+h)
		}
		if rp, err := h.provider().SearchPosts(strings.Join(parts, " "), page, limit, 0); err == nil {
			rp = filterHiddenTags(rp, hiddenTags)
			for _, p := range rp {
				if !seen[p.ID] && !skip[p.ID] {
					seen[p.ID] = true
					posts = append(posts, p)
				}
			}
		}
	}

	// лёгкое перемешивание, чтобы подборка не была монотонной
	rand.Shuffle(len(posts), func(i, j int) {
		posts[i], posts[j] = posts[j], posts[i]
	})
	if len(posts) > limit {
		posts = posts[:limit]
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"posts": recEnrich(posts),
		"tags":  weights,
		"page":  page,
		"slice": (page - 1) % 3,
	})
}

// POST /api/recommend/dislike {tags: [...]} — обратная связь «не интересно».
func (h *Handler) RecommendDislike(c *gin.Context) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	clean := make([]string, 0, len(req.Tags))
	for _, t := range req.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !genericTags[t] {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	profile := ProfileFor(c)
	profile.AddRecDisliked(clean)
	_ = profile.Save()
	invalidateRecWeights(c.GetString("briefly_user"))
	log.Printf("recommend dislike: %d tags for user %q", len(clean), c.GetString("briefly_user"))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
