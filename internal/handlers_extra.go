package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var genericTags = map[string]bool{
	"1girl": true, "1boy": true, "2girls": true, "2boys": true, "solo": true,
	"multiple_girls": true, "female": true, "male": true,
	"highres": true, "absurdres": true, "long_hair": true, "short_hair": true,
	"blush": true, "smile": true, "simple_background": true, "white_background": true,
	"photo": true, "color": true, "english_text": true, "commentary": true,
	"looking_at_viewer": true, "open_mouth": true, "closed_eyes": true,
}

var startedAt = time.Now()

// GET /api/healthz — liveness для мониторинга/Docker HEALTHCHECK.
// Намеренно без авторизации и без деталей (только статус и аптайм).
func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"uptime": int(time.Since(startedAt).Seconds()),
	})
}

func (h *Handler) SearchRandom(c *gin.Context) {
	prov := h.provider()
	if prov == nil {
		AbortWithError(c, ErrProviderUnavailable)
		return
	}
	page := 1 + rand.Intn(1500)
	posts, err := prov.SearchPosts("", page, 1, 0)
	if (err != nil || len(posts) == 0) && page != 1 {
		posts, err = prov.SearchPosts("", 1, 1, 0)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var enriched []gin.H
	db := GetDB()
	for _, p := range posts {
		entry := gin.H{
			"id": p.ID, "tags": p.Tags, "file_url": p.FileURL,
			"sample_url":  p.SampleURL,
			"preview_url": p.PreviewURL, "file_type": p.FileType,
			"width": p.Width, "height": p.Height, "file_size": p.FileSize,
			"score": p.Score, "rating": p.Rating, "downloaded": false,
		}
		if ex := db.Get(p.ID); ex != nil {
			entry["downloaded"] = ex.Downloaded
		}
		enriched = append(enriched, entry)
	}
	c.JSON(http.StatusOK, gin.H{"posts": enriched})
}

func (h *Handler) GetRelated(c *gin.Context) {
	idStr := c.Query("id")
	id, _ := strconv.Atoi(idStr)
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(c.Query("type")))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(c.Query("mode")))
	}
	if mode == "" {
		mode = "all"
	}

	db := GetDB()
	var srcPost *Post
	if p := db.Get(id); p != nil && p.Tags != "" {
		srcPost = p
	} else {
		prov := h.provider()
		if prov != nil {
			posts, err := prov.SearchPosts(fmt.Sprintf("id:%d", id), 1, 1, 0)
			if err == nil && len(posts) > 0 {
				srcPost = &Post{
					ID:         posts[0].ID,
					Tags:       posts[0].Tags,
					FileURL:    posts[0].FileURL,
					PreviewURL: posts[0].PreviewURL,
					FileType:   posts[0].FileType,
					Width:      posts[0].Width,
					Height:     posts[0].Height,
					Score:      posts[0].Score,
					Rating:     posts[0].Rating,
					Downloaded: false,
				}
			}
		}
	}

	if srcPost == nil || srcPost.Tags == "" {
		c.JSON(http.StatusOK, gin.H{"posts": []any{}, "tags": []string{}, "mode": mode, "has_artist": false, "has_character": false})
		return
	}

	// Режим visual: визуальный поиск похожих по pHash
	if mode == "visual" {
		if srcPost.Phash == "" && srcPost.Downloaded && srcPost.FilePath != "" {
			srcPost.Phash = PerceptualHashFile(srcPost.FilePath)
			db.SetPostPHash(srcPost.ID, srcPost.Phash)
		}
		if srcPost.Phash != "" {
			simPosts := db.SimilarPHash(srcPost.Phash, id, 24, 16)
			out := make([]gin.H, 0, len(simPosts))
			for _, p := range simPosts {
				d, _ := HammingPHash(srcPost.Phash, p.Phash)
				simPct := int(math.Max(30, (1.0-float64(d)/20.0)*100))
				out = append(out, gin.H{
					"id":           p.ID,
					"tags":         p.Tags,
					"file_url":     p.FileURL,
					"sample_url":   "",
					"preview_url":  p.PreviewURL,
					"file_type":    p.FileType,
					"width":        p.Width,
					"height":       p.Height,
					"score":        p.Score,
					"rating":       p.Rating,
					"downloaded":   p.Downloaded,
					"similarity":   simPct,
					"match_type":   "visual",
					"matched_tags": []string{},
				})
			}
			c.JSON(http.StatusOK, gin.H{
				"posts":         out,
				"tags":          []string{},
				"mode":          "visual",
				"has_artist":    false,
				"has_character": false,
			})
			return
		}
	}

	freqMap := globalTagFreq()
	analyzed := AnalyzeTags(srcPost.Tags, freqMap)
	hasArtist := false
	hasChar := false
	for _, ct := range analyzed {
		if ct.Kind == TagKindArtist {
			hasArtist = true
		} else if ct.Kind == TagKindCharacter {
			hasChar = true
		}
	}

	primaryQ, fallbackQ, pickedTags := BuildRelatedQueries(analyzed, mode)
	if primaryQ == "" {
		c.JSON(http.StatusOK, gin.H{
			"posts":         []any{},
			"tags":          []string{},
			"mode":          mode,
			"has_artist":    hasArtist,
			"has_character": hasChar,
		})
		return
	}

	candidateMap := make(map[int]*RelatedCandidate)
	prov := h.provider()

	// 1. Поиск в активном онлайн-провайдере
	if prov != nil {
		res, err := prov.SearchPosts(primaryQ, 1, 24, 0)
		if err == nil {
			for i := range res {
				if res[i].ID != id {
					ph := ""
					if local := db.Get(res[i].ID); local != nil {
						ph = local.Phash
					}
					candidateMap[res[i].ID] = &RelatedCandidate{
						ID:         res[i].ID,
						Tags:       res[i].Tags,
						FileURL:    res[i].FileURL,
						SampleURL:  res[i].SampleURL,
						PreviewURL: res[i].PreviewURL,
						FileType:   res[i].FileType,
						Width:      res[i].Width,
						Height:     res[i].Height,
						Score:      res[i].Score,
						Rating:     res[i].Rating,
						Phash:      ph,
						Downloaded: false,
					}
				}
			}
		}
		// Fallback при малом числе результатов
		if len(candidateMap) < 12 && fallbackQ != "" && fallbackQ != primaryQ {
			fbRes, fbErr := prov.SearchPosts(fallbackQ, 1, 24, 0)
			if fbErr == nil {
				for i := range fbRes {
					if fbRes[i].ID != id && candidateMap[fbRes[i].ID] == nil {
						ph := ""
						if local := db.Get(fbRes[i].ID); local != nil {
							ph = local.Phash
						}
						candidateMap[fbRes[i].ID] = &RelatedCandidate{
							ID:         fbRes[i].ID,
							Tags:       fbRes[i].Tags,
							FileURL:    fbRes[i].FileURL,
							SampleURL:  fbRes[i].SampleURL,
							PreviewURL: fbRes[i].PreviewURL,
							FileType:   fbRes[i].FileType,
							Width:      fbRes[i].Width,
							Height:     fbRes[i].Height,
							Score:      fbRes[i].Score,
							Rating:     fbRes[i].Rating,
							Phash:      ph,
							Downloaded: false,
						}
					}
				}
			}
		}
	}

	// 2. Добираем совпадения из локальной коллекции
	if len(pickedTags) > 0 {
		localPosts := db.SearchDownloaded(pickedTags[0])
		for _, lp := range localPosts {
			if lp.ID != id && candidateMap[lp.ID] == nil {
				candidateMap[lp.ID] = &RelatedCandidate{
					ID:         lp.ID,
					Tags:       lp.Tags,
					FileURL:    lp.FileURL,
					SampleURL:  "",
					PreviewURL: lp.PreviewURL,
					FileType:   lp.FileType,
					Width:      lp.Width,
					Height:     lp.Height,
					Score:      lp.Score,
					Rating:     lp.Rating,
					Phash:      lp.Phash,
					Downloaded: lp.Downloaded,
				}
			}
		}
	}

	candidates := make([]RelatedCandidate, 0, len(candidateMap))
	for _, cand := range candidateMap {
		candidates = append(candidates, *cand)
	}

	// Предпочтения пользователя (скрытые и избранные теги)
	hiddenTags := make(map[string]bool)
	favTags := make(map[string]bool)
	if profile := ProfileFor(c); profile != nil {
		profile.mu.RLock()
		for t := range profile.HiddenTags {
			hiddenTags[strings.ToLower(t)] = true
		}
		for t := range profile.FavTags {
			favTags[strings.ToLower(t)] = true
		}
		profile.mu.RUnlock()
	}

	// 3. Взвешенное переранжирование по коэффициенту Жаккара
	ranked := RerankRelated(srcPost.Tags, srcPost.Phash, candidates, freqMap, hiddenTags, favTags)

	limit := 24
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	out := make([]gin.H, 0, len(ranked))
	for _, sc := range ranked {
		p := sc.Candidate
		entry := gin.H{
			"id":           p.ID,
			"tags":         p.Tags,
			"file_url":     p.FileURL,
			"sample_url":   p.SampleURL,
			"preview_url":  p.PreviewURL,
			"file_type":    p.FileType,
			"width":        p.Width,
			"height":       p.Height,
			"score":        p.Score,
			"rating":       p.Rating,
			"downloaded":   p.Downloaded,
			"similarity":   sc.Similarity,
			"match_type":   sc.MatchType,
			"matched_tags": sc.MatchedTags,
		}
		if ex := db.Get(p.ID); ex != nil {
			entry["downloaded"] = ex.Downloaded
		}
		out = append(out, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"posts":         out,
		"tags":          pickedTags,
		"mode":          mode,
		"has_artist":    hasArtist,
		"has_character": hasChar,
	})
}

func (h *Handler) DownloadLiked(c *gin.Context) {
	if h.downloader == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "downloader not initialized"})
		return
	}
	profile := ProfileFor(c)
	profile.mu.RLock()
	ids := make([]int, 0, len(profile.LikedPosts))
	for id := range profile.LikedPosts {
		ids = append(ids, id)
	}
	profile.mu.RUnlock()

	db := GetDB()
	queued := 0
	for _, id := range ids {
		p := db.Get(id)
		if p == nil || p.Downloaded || p.FileURL == "" {
			continue
		}
		h.downloader.Submit(DownloadJob{
			PostID:   p.ID,
			FileURL:  p.FileURL,
			FileType: p.FileType,
			Referer:  h.refererForFileURL(p.FileURL),
		})
		queued++
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": queued, "total": len(ids)})
}

func saveRoot() string {
	root := GetConfig().GetSavePath()
	if root == "" {
		root = "data/posts"
	}
	return root
}

func (h *Handler) FindFiles(c *gin.Context) {
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"files": []interface{}{}})
		return
	}
	root := saveRoot()
	var out []gin.H
	limit := 3000
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !strings.Contains(strings.ToLower(info.Name()), q) {
			return nil
		}
		out = append(out, gin.H{
			"name": info.Name(),
			"path": path,
			"size": info.Size(),
		})
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"root": root, "files": out})
}

type dupGroup struct {
	Size  int64    `json:"size"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
	// Posts — посты, соответствующие файлам (id извлекается из каталога
	// файла, downloaded — есть ли ссылка в БД). Для UI «объединить дубли».
	Posts []gin.H `json:"posts,omitempty"`
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (h *Handler) FindDuplicates(c *gin.Context) {
	root := saveRoot()
	bySize := make(map[int64][]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		bySize[info.Size()] = append(bySize[info.Size()], path)
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var sizes []int64
	for size, list := range bySize {
		if size > 0 && len(list) > 1 {
			sizes = append(sizes, size)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })

	var groups []dupGroup
	for _, size := range sizes {
		byHash := make(map[string][]string)
		for _, path := range bySize[size] {
			hash, err := fileSHA256(path)
			if err != nil {
				continue
			}
			byHash[hash] = append(byHash[hash], path)
		}
		for hash, files := range byHash {
			if len(files) > 1 {
				sort.Strings(files)
				groups = append(groups, dupGroup{Size: size, Hash: hash, Files: files, Posts: postsForDupFiles(files)})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"dups": groups})
}

// postsForDupFiles сопоставляет файлам-дубликатам посты: id берётся из имени
// каталога файла (<save>/<id>/original.ext), downloaded — есть ли строка в БД.
func postsForDupFiles(files []string) []gin.H {
	db := GetDB()
	out := make([]gin.H, 0, len(files))
	for _, f := range files {
		id, err := strconv.Atoi(filepath.Base(filepath.Dir(f)))
		if err != nil {
			continue
		}
		p := db.Get(id)
		out = append(out, gin.H{
			"id":         id,
			"downloaded": p != nil && p.Downloaded,
		})
	}
	return out
}

// ── Объединение дубликатов ───────────────────────────────────────────────
// В отличие от CleanDuplicates (удаляет лишние файлы, оставляя сирот в БД),
// merge переносит лайки/скрытия/коллекции/комментарии дубликата на пост-
// оригинал и удаляет запись целиком.

// MergeDuplicates — POST /api/dups/merge {keep_id, remove_ids}.
// Переносит данные remove-постов на keep-пост и удаляет их записи и файлы.
func (h *Handler) MergeDuplicates(c *gin.Context) {
	var req struct {
		KeepID  int   `json:"keep_id"`
		Removes []int `json:"remove_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.KeepID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keep_id required"})
		return
	}
	db := GetDB()
	if keep := db.Get(req.KeepID); keep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "keep post not found"})
		return
	}
	merged := 0
	for _, rid := range req.Removes {
		if rid <= 0 || rid == req.KeepID {
			continue
		}
		if p := db.Get(rid); p == nil {
			continue
		}
		db.ReplaceCommentsPost(rid, req.KeepID)
		replacePostIDEverywhere(rid, req.KeepID)
		removeDownloadedFilesForPost(rid)
		db.DeletePostRow(rid)
		merged++
	}
	c.JSON(http.StatusOK, gin.H{"merged": merged, "keep_id": req.KeepID})
}

// removeDownloadedFilesForPost удаляет файл поста (и каталог, если он опустел)
// и миниатюру — та же логика, что в DeletePost-хендлере.
func removeDownloadedFilesForPost(id int) {
	db := GetDB()
	thumb := filepath.Join("data", "thumbs", fmt.Sprintf("%d.jpg", id))
	os.Remove(thumb)
	if p := db.Get(id); p != nil && p.FilePath != "" {
		os.Remove(p.FilePath)
		if dir := filepath.Dir(p.FilePath); dir != "." && dir != "" {
			if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
				os.Remove(dir)
			}
		}
	}
}

// replacePostIDEverywhere переносит упоминание поста id→keep во всех профилях
// (аккаунты + legacy data/profile.json): лайки, скрытия, коллекции.
func replacePostIDEverywhere(from, to int) {
	if from == to {
		return
	}
	GetAccounts().ForEachProfile(func(p *Profile) {
		p.ReplacePostID(from, to)
		_ = p.Save()
	})
	if _, err := os.Stat(legacyProfileFile); err == nil {
		p := GetProfile()
		p.ReplacePostID(from, to)
		_ = p.Save()
	}
}

func (h *Handler) CleanDuplicates(c *gin.Context) {
	if c.GetHeader("X-Confirm-Dupes") == "" {
		AbortWithError(c, ErrConfirmRequired)
		return
	}
	root := saveRoot()

	// Пути, на которые ссылается БД, считаем оригиналами — их не удаляем.
	db := GetDB()
	dbPaths := make(map[string]bool)
	for _, p := range db.GetDownloaded() {
		if p.FilePath != "" {
			dbPaths[p.FilePath] = true
		}
	}

	bySize := make(map[int64][]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() <= 0 {
			return nil
		}
		bySize[info.Size()] = append(bySize[info.Size()], path)
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Детерминированный порядок обхода: размеры по убыванию, пути внутри
	// группы отсортированы — выбор «оригинала» не зависит от map-итерации.
	var sizes []int64
	for size, paths := range bySize {
		if len(paths) > 1 {
			sizes = append(sizes, size)
			sort.Strings(paths)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })

	var removed []string
	for _, size := range sizes {
		byHash := make(map[string][]string)
		for _, path := range bySize[size] {
			hash, err := fileSHA256(path)
			if err != nil {
				continue
			}
			byHash[hash] = append(byHash[hash], path)
		}
		var hashes []string
		for hash, paths := range byHash {
			if len(paths) > 1 {
				hashes = append(hashes, hash)
			}
		}
		sort.Strings(hashes)
		for _, hash := range hashes {
			paths := byHash[hash]
			sort.Strings(paths)
			// Оригинал — файл, на который ссылается БД; иначе лексически первый.
			keep := paths[0]
			for _, p := range paths {
				if dbPaths[p] {
					keep = p
					break
				}
			}
			for _, p := range paths {
				if p == keep {
					continue
				}
				removed = append(removed, p)
				os.Remove(p)
				db.UnsetDownloadedByPath(p)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"removed": removed, "count": len(removed)})
}
