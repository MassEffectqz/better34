package internal

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ToggleLike(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	p := ProfileFor(c)
	liked := p.ToggleLike(id)
	p.Save()
	c.JSON(http.StatusOK, gin.H{"liked": liked})
}

func (h *Handler) ToggleHide(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	p := ProfileFor(c)
	hidden := p.ToggleHide(id)
	p.Save()
	c.JSON(http.StatusOK, gin.H{"hidden": hidden})
}

func (h *Handler) GetProfileData(c *gin.Context) {
	p := ProfileFor(c)
	p.mu.Lock()
	p.ensurePresetIDs()
	p.mu.Unlock()
	p.mu.RLock()
	defer p.mu.RUnlock()

	likes := make([]int, 0, len(p.LikedPosts))
	for id := range p.LikedPosts {
		likes = append(likes, id)
	}
	hides := make([]int, 0, len(p.HiddenPosts))
	for id := range p.HiddenPosts {
		hides = append(hides, id)
	}
	favTags := make([]string, 0, len(p.FavTags))
	for t := range p.FavTags {
		favTags = append(favTags, t)
	}
	hiddenTags := make([]string, 0, len(p.HiddenTags))
	for t := range p.HiddenTags {
		hiddenTags = append(hiddenTags, t)
	}

	nickname := ""
	avatar := ""
	if u := c.GetString("briefly_user"); u != "" {
		if acc, ok := GetAccounts().Get(u); ok {
			nickname = acc.Nickname
			if nickname == "" {
				nickname = u
			}
			avatar = acc.Avatar
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"liked_posts":  likes,
		"hidden_posts": hides,
		"presets":      p.Presets,
		"fav_tags":     favTags,
		"hidden_tags":  hiddenTags,
		"nickname":     nickname,
		"avatar":       avatar,
	})
}

func (h *Handler) AddPreset(c *gin.Context) {
	var req struct {
		Name       string   `json:"name"`
		Query      string   `json:"query"`
		Kind       string   `json:"kind"`
		Tags       []string `json:"tags"`
		HiddenTags []string `json:"hidden_tags"`
		FavTags    []string `json:"fav_tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	p := ProfileFor(c)
	p.AddPreset(req.Name, req.Query, req.Kind, req.Tags, req.HiddenTags, req.FavTags)
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "preset added"})
}

func (h *Handler) ApplyPreset(c *gin.Context) {
	var req struct {
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Type != "fav" && req.Type != "hidden" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be fav or hidden"})
		return
	}
	p := ProfileFor(c)
	n := p.ApplyTagPreset(c.Param("id"), req.Type)
	if n < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "preset not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"applied": n})
}

func (h *Handler) DeletePreset(c *gin.Context) {
	p := ProfileFor(c)
	if !p.DeletePreset(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"error": "preset not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "preset deleted"})
}

func (h *Handler) UpdatePreset(c *gin.Context) {
	var req struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	if !p.UpdatePreset(c.Param("id"), req.Name, req.Query) {
		c.JSON(http.StatusNotFound, gin.H{"error": "preset not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "preset updated"})
}

func (h *Handler) MovePreset(c *gin.Context) {
	var req struct {
		Dir int `json:"dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Dir != -1 && req.Dir != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dir must be -1 or 1"})
		return
	}
	p := ProfileFor(c)
	if !p.MovePreset(c.Param("id"), req.Dir) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot move preset"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "preset moved"})
}

func (h *Handler) ExportPresets(c *gin.Context) {
	p := ProfileFor(c)
	p.mu.RLock()
	defer p.mu.RUnlock()
	c.JSON(http.StatusOK, gin.H{"presets": p.Presets})
}

func (h *Handler) ImportPresets(c *gin.Context) {
	var req struct {
		Presets []QueryPreset `json:"presets"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	for _, pr := range req.Presets {
		if pr.Name != "" {
			p.AddPreset(pr.Name, pr.Query, pr.Kind, pr.Tags, pr.HiddenTags, pr.FavTags)
		}
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"imported": len(req.Presets)})
}

func (h *Handler) ToggleFavTag(c *gin.Context) {
	var req struct {
		Tag string `json:"tag"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	liked := p.ToggleFavTag(req.Tag)
	p.Save()
	c.JSON(http.StatusOK, gin.H{"liked": liked})
}

func (h *Handler) ToggleHiddenTag(c *gin.Context) {
	var req struct {
		Tag string `json:"tag"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	hidden := p.ToggleHiddenTag(req.Tag)
	p.Save()
	c.JSON(http.StatusOK, gin.H{"hidden": hidden})
}

func (h *Handler) ClearFavTags(c *gin.Context) {
	p := ProfileFor(c)
	p.mu.Lock()
	p.FavTags = map[string]bool{}
	p.mu.Unlock()
	p.Save()
	c.JSON(http.StatusOK, gin.H{"cleared": true})
}

func (h *Handler) ClearHiddenTags(c *gin.Context) {
	p := ProfileFor(c)
	p.mu.Lock()
	p.HiddenTags = map[string]bool{}
	p.mu.Unlock()
	p.Save()
	c.JSON(http.StatusOK, gin.H{"cleared": true})
}
