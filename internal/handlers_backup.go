package internal

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// backupProfile — сериализуемый снимок профиля пользователя.
type backupProfile struct {
	LikedPosts  []int          `json:"liked_posts"`
	LikedAt     map[int]int64  `json:"liked_at,omitempty"`
	HiddenPosts []int          `json:"hidden_posts"`
	Presets     []QueryPreset  `json:"presets,omitempty"`
	FavTags     []string       `json:"fav_tags,omitempty"`
	HiddenTags  []string       `json:"hidden_tags,omitempty"`
	Collections []Collection   `json:"collections,omitempty"`
	RecDisliked map[string]int `json:"rec_disliked,omitempty"`
}

type backupFile struct {
	Version    int           `json:"version"`
	App        string        `json:"app"`
	ExportedAt string        `json:"exported_at"`
	User       string        `json:"user,omitempty"`
	Profile    backupProfile `json:"profile"`
	Comments   []*Comment    `json:"comments,omitempty"`
}

// GET /api/profile/export — полный бэкап: лайки, скрытия, пресеты, теги,
// коллекции, штрафы рекомендаций и собственные комментарии пользователя.
func (h *Handler) ExportProfile(c *gin.Context) {
	p := ProfileFor(c)
	p.mu.RLock()
	b := backupProfile{
		LikedPosts:  intSetSorted(p.LikedPosts),
		LikedAt:     copyI64Map(p.LikedAt),
		HiddenPosts: intSetSorted(p.HiddenPosts),
		Presets:     append([]QueryPreset{}, p.Presets...),
		FavTags:     stringSetSorted(p.FavTags),
		HiddenTags:  stringSetSorted(p.HiddenTags),
		Collections: append([]Collection{}, p.Collections...),
		RecDisliked: copyIntMap(p.RecDisliked),
	}
	p.mu.RUnlock()

	var comments []*Comment
	if u := c.GetString("briefly_user"); u != "" {
		comments = GetDB().CommentsByUser(u)
	}

	c.Header("Content-Disposition", `attachment; filename="briefly-profile.json"`)
	c.JSON(http.StatusOK, backupFile{
		Version:    1,
		App:        "briefly",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		User:       c.GetString("briefly_user"),
		Profile:    b,
		Comments:   comments,
	})
}

// POST /api/profile/import — слить бэкап в текущий профиль (merge):
// существующие данные не удаляются, новые добавляются.
func (h *Handler) ImportProfile(c *gin.Context) {
	var req struct {
		Profile  *backupProfile `json:"profile"`
		Comments []*Comment     `json:"comments"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Profile == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ожидается объект вида {\"profile\": {...}}"})
		return
	}
	in := req.Profile
	p := ProfileFor(c)

	p.mu.Lock()
	for _, id := range in.LikedPosts {
		p.LikedPosts[id] = true
		delete(p.HiddenPosts, id)
		if _, ok := p.LikedAt[id]; !ok && in.LikedAt != nil {
			if ts, ok := in.LikedAt[id]; ok {
				p.LikedAt[id] = ts
			} else {
				p.LikedAt[id] = time.Now().Unix()
			}
		}
	}
	for _, id := range in.HiddenPosts {
		if !p.LikedPosts[id] {
			p.HiddenPosts[id] = true
		}
	}
	existingPresets := make(map[string]bool, len(p.Presets))
	for _, pr := range p.Presets {
		if pr.Name != "" {
			existingPresets[pr.Name] = true
		}
	}
	for _, pr := range in.Presets {
		if pr.Name != "" && !existingPresets[pr.Name] {
			p.AddPreset(pr.Name, pr.Query, pr.Kind, pr.Tags, pr.HiddenTags, pr.FavTags)
		}
	}
	for _, t := range in.FavTags {
		if t != "" {
			p.FavTags[t] = true
		}
	}
	for _, t := range in.HiddenTags {
		t = strings.TrimLeft(t, "+-")
		if t != "" {
			p.HiddenTags[t] = true
		}
	}
	existingCols := make(map[string]bool, len(p.Collections))
	for _, col := range p.Collections {
		existingCols[col.Name] = true
	}
	importedCols := 0
	for _, col := range in.Collections {
		if col.Name == "" || existingCols[col.Name] {
			continue
		}
		nc := Collection{ID: newPresetID(), Name: col.Name, CreatedAt: col.CreatedAt}
		nc.Posts = append(nc.Posts, col.Posts...)
		p.Collections = append(p.Collections, nc)
		importedCols++
	}
	for tag, n := range in.RecDisliked {
		if tag != "" && p.RecDisliked[tag] < n {
			p.RecDisliked[tag] = n
		}
	}
	p.mu.Unlock()

	addedComments := 0
	if u := c.GetString("briefly_user"); u != "" && len(req.Comments) > 0 {
		addedComments = GetDB().ImportComments(u, req.Comments)
	}

	p.Save()
	c.JSON(http.StatusOK, gin.H{
		"imported": gin.H{
			"liked_posts":  len(in.LikedPosts),
			"hidden_posts": len(in.HiddenPosts),
			"presets":      len(in.Presets),
			"fav_tags":     len(in.FavTags),
			"hidden_tags":  len(in.HiddenTags),
			"collections":  importedCols,
			"comments":     addedComments,
		},
	})
}

func intSetSorted(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func stringSetSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func copyI64Map(m map[int]int64) map[int]int64 {
	out := make(map[int]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyIntMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
