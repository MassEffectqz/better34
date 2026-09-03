package internal

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// GET /api/collections[?post_id=N] — список коллекций с количеством постов.
// С post_id дополнительно возвращается признак has — пост уже в коллекции.
func (h *Handler) ListCollections(c *gin.Context) {
	p := ProfileFor(c)
	postID := 0
	if s := c.Query("post_id"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			postID = v
		}
	}
	p.mu.RLock()
	out := make([]gin.H, 0, len(p.Collections))
	for _, col := range p.Collections {
		item := gin.H{
			"id":    col.ID,
			"name":  col.Name,
			"count": len(col.Posts),
		}
		if postID > 0 {
			has := false
			for _, pid := range col.Posts {
				if pid == postID {
					has = true
					break
				}
			}
			item["has"] = has
		}
		out = append(out, item)
	}
	p.mu.RUnlock()
	c.JSON(http.StatusOK, gin.H{"collections": out})
}

// POST /api/collection {name} — создать (существующее имя возвращается как есть).
func (h *Handler) CreateCollection(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	col, ok := p.AddCollection(req.Name)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"collection": gin.H{"id": col.ID, "name": col.Name, "count": len(col.Posts)}})
}

// PATCH /api/collection/:id {name} — переименовать.
func (h *Handler) RenameCollection(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p := ProfileFor(c)
	if !p.RenameCollection(c.Param("id"), req.Name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "renamed"})
}

// DELETE /api/collection/:id — удалить коллекцию (посты не трогаем).
func (h *Handler) DeleteCollection(c *gin.Context) {
	p := ProfileFor(c)
	if !p.DeleteCollection(c.Param("id")) {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// POST /api/collection/:id/post {id} — добавить/убрать пост (toggle).
func (h *Handler) CollectionTogglePost(c *gin.Context) {
	var req struct {
		ID int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid id required"})
		return
	}
	p := ProfileFor(c)
	added, found := p.CollectionTogglePost(c.Param("id"), req.ID)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"added": added})
}

// POST /api/collection/:id/posts {ids:[...]} — добавить в коллекцию несколько
// постов разом (массовое действие «в коллекцию» для выделенного). Уже
// присутствующие пропускаются; возвращает количество добавленных.
func (h *Handler) CollectionAddMany(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids required"})
		return
	}
	p := ProfileFor(c)
	added, found := p.CollectionAddMany(c.Param("id"), req.IDs)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	p.Save()
	c.JSON(http.StatusOK, gin.H{"added": added})
}

// GET /api/collection/:id/posts — посты коллекции через общий posts-by-ids формат.
func (h *Handler) CollectionPosts(c *gin.Context) {
	p := ProfileFor(c)
	ids, ok := p.CollectionPosts(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if len(ids) > 2000 {
		ids = ids[:2000]
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	// Переиспользуем общий рендер постов по списку id.
	c.Request.URL.RawQuery = "ids=" + strings.Join(parts, ",")
	h.GetPostsByIDs(c)
}
