package internal

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// POST /api/view/:id — отметить пост просмотренным (история просмотров).
// Авто-вызов из вьюера; идемпотентно, всегда 200.
func (h *Handler) RecordView(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	GetDB().RecordView(id)
	c.JSON(http.StatusOK, gin.H{"viewed": true})
}

// POST /api/views {ids:[...]} — отметить несколько постов просмотренными разом.
func (h *Handler) RecordViews(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids required"})
		return
	}
	GetDB().RecordViews(req.IDs)
	c.JSON(http.StatusOK, gin.H{"recorded": len(req.IDs)})
}

// POST /api/view/:id/forget — снять отметку «просмотрено» (откат).
func (h *Handler) ForgetView(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	GetDB().ForgetView(id)
	c.JSON(http.StatusOK, gin.H{"viewed": false})
}

// POST /api/posts/:id/parent {parent_id} — привязать пост к родителю
// (данбуровская связка «родитель/дети»). parent_id: 0 — снять привязку.
func (h *Handler) SetPostParent(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		ParentID int `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	GetDB().SetPostParent(id, req.ParentID)
	c.JSON(http.StatusOK, gin.H{"id": id, "parent_id": GetDB().PostParent(id)})
}

// GET /api/posts/:id/relations — родитель и дети поста для вьюера.
func (h *Handler) PostRelations(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	db := GetDB()
	out := gin.H{"id": id, "parent_id": db.PostParent(id), "children": []gin.H{}}
	if parentID := db.PostParent(id); parentID > 0 {
		if p := db.Get(parentID); p != nil {
			out["parent"] = gin.H{
				"id": p.ID, "file_type": p.FileType, "width": p.Width, "height": p.Height,
				"downloaded": p.Downloaded,
			}
		}
	}
	children := db.PostChildren(id)
	childList := make([]gin.H, 0, len(children))
	for _, ch := range children {
		childList = append(childList, gin.H{
			"id": ch.ID, "file_type": ch.FileType, "width": ch.Width, "height": ch.Height,
			"downloaded": ch.Downloaded,
		})
	}
	out["children"] = childList
	c.JSON(http.StatusOK, out)
}
