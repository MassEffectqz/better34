package internal

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const maxCommentRunes = 500

type commentView struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// commentViews обогащает комментарии профилями (ник/аватар).
func commentViews(list []*Comment) []*commentView {
	accs := GetAccounts()
	out := make([]*commentView, 0, len(list))
	for _, cm := range list {
		v := &commentView{
			ID:        cm.ID,
			Username:  cm.Username,
			Nickname:  cm.Username,
			Avatar:    "",
			Text:      cm.Text,
			CreatedAt: cm.CreatedAt,
		}
		if u, ok := accs.Get(cm.Username); ok {
			v.Nickname = u.Nickname
			v.Avatar = u.Avatar
		}
		out = append(out, v)
	}
	return out
}

// GET /api/comments/:postId
func (h *Handler) GetComments(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"comments": commentViews(GetDB().Comments(id))})
}

// POST /api/comments/:postId {text} — только авторизованный пользователь.
func (h *Handler) AddComment(c *gin.Context) {
	user := c.GetString("briefly_user")
	if user == "" {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		AbortWithError(c, ErrEmptyComment)
		return
	}
	if utf8.RuneCountInString(text) > maxCommentRunes {
		AbortWithError(c, ErrCommentTooLong)
		return
	}
	cm, err := GetDB().AddComment(id, user, text)
	if err != nil {
		AbortWithError(c, ErrCommentSave)
		return
	}
	views := commentViews([]*Comment{cm})
	publishSSE(map[string]any{"type": "comment", "post_id": id})
	c.JSON(http.StatusOK, gin.H{"comment": views[0]})
}

// DELETE /api/comments/:cid — удалить может только автор.
func (h *Handler) DeleteComment(c *gin.Context) {
	user := c.GetString("briefly_user")
	if user == "" {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	id, err := strconv.Atoi(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	db := GetDB()
	var owner string
	if err := db.read.QueryRow(`SELECT username FROM comments WHERE id=?`, id).Scan(&owner); err != nil {
		AbortWithError(c, ErrCommentNotFound)
		return
	}
	if owner != user {
		AbortWithError(c, ErrCommentNotYours)
		return
	}
	if !db.DeleteComment(id) {
		AbortWithError(c, ErrCommentDelete)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
