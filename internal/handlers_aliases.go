package internal

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GET /api/tag-aliases — список всех алиасов тегов (синонимов).
func (h *Handler) ListTagAliases(c *gin.Context) {
	aliases := GetDB().ListTagAliases()
	if aliases == nil {
		aliases = []TagAlias{}
	}
	c.JSON(http.StatusOK, gin.H{"aliases": aliases})
}

// POST /api/tag-alias {alias, target} — добавить/перезаписать алиас
// (danbooru-style: «catgirl» → «neko», поиск и провайдеры видят канон).
func (h *Handler) AddTagAlias(c *gin.Context) {
	var req struct {
		Alias  string `json:"alias"`
		Target string `json:"target"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !GetDB().AddTagAlias(req.Alias, req.Target) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias and target required (different, non-empty)"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"alias": normalizeAlias(req.Alias), "target": normalizeAlias(req.Target)})
}

// DELETE /api/tag-alias/:alias — удалить алиас.
func (h *Handler) DeleteTagAlias(c *gin.Context) {
	if !GetDB().DeleteTagAlias(c.Param("alias")) {
		c.JSON(http.StatusNotFound, gin.H{"error": "alias not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func normalizeAlias(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
