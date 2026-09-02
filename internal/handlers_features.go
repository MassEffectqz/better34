package internal

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// GetSimilar — GET /api/similar/:id: визуально похожие локальные посты
// по pHash. Если у поста ещё нет хэша (скачан до появления фичи) —
// считается на месте и сохраняется.
func (h *Handler) GetSimilar(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	db := GetDB()
	post := db.Get(id)
	if post == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	if post.Phash == "" {
		if post.Downloaded && post.FilePath != "" {
			post.Phash = PerceptualHashFile(post.FilePath)
			db.SetPostPHash(id, post.Phash)
		}
		if post.Phash == "" {
			c.JSON(http.StatusOK, gin.H{"posts": []any{}})
			return
		}
	}
	similar := db.SimilarPHash(post.Phash, id, 40, 12)
	if similar == nil {
		similar = []*Post{}
	}
	c.JSON(http.StatusOK, gin.H{"posts": similar})
}

// RemotePush — POST /api/remote/push {id}: попросить все подключённые
// клиенты открыть этот пост (режим «пульта», задача 6).
func (h *Handler) RemotePush(c *gin.Context) {
	var req struct {
		ID int `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	publishSSE(map[string]any{"type": "remote", "id": req.ID})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
