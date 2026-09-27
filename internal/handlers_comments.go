package internal

import (
	"errors"
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

// GetSourceComments отдаёт комментарии поста, подтянутые с бура-источника
// (dapi s=comment), с кэшем в SQLite. Это read-only зеркало: писать на
// чужой сайт мы не умеем и не должны.
//
// GET /api/posts/:id/source-comments?refresh=1
func (h *Handler) GetSourceComments(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	db := GetDB()
	p := db.Get(id)
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	// Пост из ленты может быть ещё не сохранён локально — тогда сайт и id
	// источника берём из тела ответа поиска, куда клиент их положил.
	postID, site := p.ID, p.Source
	if c.Query("site") != "" {
		site = strings.ToLower(strings.TrimSpace(c.Query("site")))
	}
	if v := c.Query("source_id"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			postID = n
		}
	}
	if site == "" {
		c.JSON(http.StatusOK, gin.H{"unsupported": true, "site": "", "comments": []any{}, "count": 0})
		return
	}
	prov := h.providers[site]
	if prov == nil {
		c.JSON(http.StatusOK, gin.H{"unsupported": true, "site": site, "comments": []any{}, "count": 0})
		return
	}
	refresh := c.Query("refresh") == "1"
	// cached=1 — строго из кэша: клиент открывает пост и не хочет стучаться
	// к бору за каждым просмотром. Ответ без кэша — пустой, но не «unsupported»:
	// о том, поддерживает ли сайт комментарии, мы узнаём только сходив.
	cachedOnly := c.Query("cached") == "1"

	// 1) Кэш.
	if !refresh {
		comments, count, fetchedAt, ready := db.SourceCommentState(postID, site)
		if ready || cachedOnly {
			c.JSON(http.StatusOK, gin.H{
				"site": site, "comments": comments, "count": count,
				"fetched_at": fetchedAt, "cached": true, "unsupported": false,
				"has_cache": ready,
			})
			return
		}
	}

	// 2) Источник (медленно, поэтому только по явному запросу/кнопке).
	list, err := prov.SourceComments(postID)
	if err != nil {
		// Источник не отдаёт комментарии (gelbooru отключил сервис, safebooru
		// их не поддерживает) — это не ошибка, а «фичи нет».
		if errors.Is(err, ErrSourceCommentsUnsupported) {
			c.JSON(http.StatusOK, gin.H{
				"unsupported": true, "site": site, "comments": []any{}, "count": 0,
			})
			return
		}
		if errors.Is(err, errAPIAuth) {
			AbortWithError(c, ErrSourceAuthFailed)
			return
		}
		if errors.Is(err, errAPITransient) {
			AbortWithError(c, ErrSourceRateLimited)
			return
		}
		AbortWithError(c, ErrSourceCommentsFetch)
		return
	}
	stored := make([]*SourceComment, 0, len(list))
	for i := range list {
		stored = append(stored, &list[i])
	}
	_ = db.ReplaceSourceComments(postID, site, stored)
	comments, _, fetchedAt, _ := db.SourceCommentState(postID, site)
	c.JSON(http.StatusOK, gin.H{
		"site": site, "comments": comments, "count": len(list),
		"fetched_at": fetchedAt, "cached": false, "unsupported": false,
	})
}

// GetSourceCommentsMeta — лёгкая проверка «есть ли комментарии у источника»
// для бейджа в ленте: отвечает из локальной БД, без обращения к источнику.
func (h *Handler) GetSourceCommentsMeta(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	p := GetDB().Get(id)
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"has_comments": p.HasComments,
		"count":        p.CommentCount,
		"site":         p.Source,
	})
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
