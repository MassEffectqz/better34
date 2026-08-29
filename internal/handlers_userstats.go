package internal

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GET /api/user-stats — персональная статистика пользователя: лайки,
// скрытия, коллекции, комментарии, топ тегов лайкнутого и активность.
func (h *Handler) GetUserStats(c *gin.Context) {
	p := ProfileFor(c)

	p.mu.RLock()
	likes := make([]int, 0, len(p.LikedPosts))
	for id := range p.LikedPosts {
		likes = append(likes, id)
	}
	hides := len(p.HiddenPosts)
	favN := len(p.FavTags)
	hiddenTagsN := len(p.HiddenTags)
	colN := len(p.Collections)
	likedAt := make(map[int]int64, len(p.LikedAt))
	for k, v := range p.LikedAt {
		likedAt[k] = v
	}
	p.mu.RUnlock()

	db := GetDB()

	comments := 0
	if u := c.GetString("briefly_user"); u != "" {
		comments = db.CountCommentsByUser(u)
	}

	// Метаданные лайкнутых постов из локальной БД (что успели посмотреть/скачать).
	downloadedLikes := 0
	var totalBytes int64
	tagCounts := make(map[string]int)
	const maxLookup = 3000
	if len(likes) > maxLookup {
		sort.Slice(likes, func(i, j int) bool { return likes[i] > likes[j] })
		likes = likes[:maxLookup]
	}
	for _, id := range likes {
		post := db.Get(id)
		if post == nil {
			continue
		}
		if post.Downloaded {
			downloadedLikes++
			totalBytes += int64(post.FileSize)
		}
		seen := make(map[string]bool, 8)
		for _, t := range strings.Fields(strings.ToLower(post.Tags)) {
			if genericTags[t] || strings.Contains(t, ":") || len(t) < 3 || seen[t] {
				continue
			}
			seen[t] = true
			tagCounts[t]++
		}
	}

	type tagCount struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	tags := make([]tagCount, 0, 20)
	for t, n := range tagCounts {
		tags = append(tags, tagCount{Tag: t, Count: n})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Tag < tags[j].Tag
	})
	if len(tags) > 15 {
		tags = tags[:15]
	}

	// Активность: лайки за последние 14 дней по локальному времени.
	now := time.Now()
	days := make([]gin.H, 14)
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	countsByDay := make([]int, 14)
	for _, ts := range likedAt {
		d := time.Unix(ts, 0)
		diff := int(startDay.Sub(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())).Hours() / 24)
		if diff >= 0 && diff < 14 {
			countsByDay[13-diff]++
		}
	}
	for i := 0; i < 14; i++ {
		day := startDay.AddDate(0, 0, -(13 - i))
		days[i] = gin.H{
			"date":  day.Format("01.02"),
			"count": countsByDay[i],
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"likes":            len(likes),
		"hides":            hides,
		"fav_tags":         favN,
		"hidden_tags":      hiddenTagsN,
		"collections":      colN,
		"comments":         comments,
		"downloaded_likes": downloadedLikes,
		"liked_size_mb":    float64(totalBytes) / 1024 / 1024,
		"top_tags":         tags,
		"activity":         days,
	})
}
