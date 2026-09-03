package internal

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ── Booru-API наружу ──────────────────────────────────────────────────────
// Свою скачанную библиотеку can serve другим приложениям (Hydrus, скрипты)
// в gelbooru-0.2-совместимом JSON:
//   GET /api/booru/posts?tags=&limit=&pid=      → {"@attributes":..,"post":[..]}
//   GET /api/booru/posts.json                   → danbooru-style массив
// Аутентификация — как у остальных /api (сессия или X-Briefly-Token).

// booruPost — модель, которую понимают внешние приложения.
type booruPost struct {
	ID         int    `json:"id"`
	Tags       string `json:"tags"`
	FileURL    string `json:"file_url"`
	SampleURL  string `json:"sample_url"`
	PreviewURL string `json:"preview_url"`
	FileSize   int    `json:"file_size"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Score      int    `json:"score"`
	Rating     string `json:"rating"`
	Source     string `json:"source"`
	MD5        string `json:"md5"`
}

// booruBaseURL строит абсолютный URL API (для file_url внешних клиентов).
func booruBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	} else if v := c.GetHeader("X-Forwarded-Proto"); v == "https" {
		scheme = v
	}
	host := c.Request.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + "/api"
}

// BooruPosts — единая выдача библиотеки. format=json (по умолчанию) отдаёт
// gelbooru-0.2-обёртку, path /posts.json — голый массив (danbooru-style).
func (h *Handler) BooruPosts(c *gin.Context) {
	tags := strings.TrimSpace(c.Query("tags"))
	limit := 40
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	page := 1
	if v := c.Query("pid"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	offset := (page - 1) * limit

	db := GetDB()
	var posts []*Post
	if tags != "" {
		posts = db.SearchDownloaded(ResolveAliasesInQuery(tags))
	} else {
		posts = db.GetDownloaded()
	}
	if offset >= len(posts) {
		offset = len(posts)
	}
	end := offset + limit
	if end > len(posts) {
		end = len(posts)
	}
	base := booruBaseURL(c)
	out := make([]booruPost, 0, end-offset)
	for _, p := range posts[offset:end] {
		fileURL := base + "/save/" + strconv.Itoa(p.ID)
		out = append(out, booruPost{
			ID:         p.ID,
			Tags:       p.Tags,
			FileURL:    fileURL,
			SampleURL:  fileURL,
			PreviewURL: base + "/thumb/" + strconv.Itoa(p.ID),
			FileSize:   p.FileSize,
			Width:      p.Width,
			Height:     p.Height,
			Score:      p.Score,
			Rating:     p.Rating,
			Source:     p.Source,
			MD5:        p.MD5,
		})
	}

	// format=gelbooru (по умолчанию) — обёртка 0.2, format=array — голый массив (danbooru-style).
	// Тот же голый массив отдаёт и /posts.json.
	if strings.HasSuffix(c.FullPath()+"/", ".json/") || c.Query("format") == "array" {
		c.JSON(http.StatusOK, out)
		return
	}
	attrs := gin.H{
		"limit": limit,
		"pid":   page,
		"count": len(posts),
	}
	if minID := GetConfig().GetMinID(); minID > 0 {
		attrs["min_id"] = minID
	}
	c.JSON(http.StatusOK, gin.H{"@attributes": attrs, "post": out})
}

// BooruTags — лёгкий список тегов с количеством (полезно для импорта правил).
func (h *Handler) BooruTags(c *gin.Context) {
	db := GetDB()
	counts := db.AllTagsFreq()
	limit := 500
	out := make([]gin.H, 0, len(counts))
	for tag, n := range counts {
		out = append(out, gin.H{"tag": tag, "count": n})
		if len(out) >= limit {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"tags": out})
}
