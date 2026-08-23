package internal

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// requestSecure определяет, пришёл ли запрос по HTTPS: напрямую по TLS
// или через reverse proxy, проставивший X-Forwarded-Proto.
func requestSecure(c *gin.Context) bool {
	if c.Request != nil && c.Request.TLS != nil {
		return true
	}
	return c.GetHeader("X-Forwarded-Proto") == "https"
}

func setSessionCookie(c *gin.Context, token string, expires time.Time) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, token, int(time.Until(expires).Seconds()),
		"/", "", requestSecure(c), true)
}

func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionCookieName, "", -1, "/", "", requestSecure(c), true)
}

func sessionUser(c *gin.Context) string {
	token, err := c.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	u, _ := GetAccounts().UserBySession(token)
	return u
}

// POST /api/auth/register {username, password}
func (h *Handler) AuthRegister(c *gin.Context) {
	if ok, wait := authLimiter.Allow(c.ClientIP()); !ok {
		authTooMany(c, wait)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверный запрос"})
		return
	}
	acc := GetAccounts()
	user, err := acc.Register(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	token, expires, err := acc.CreateSession(user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "не удалось создать сессию"})
		return
	}
	setSessionCookie(c, token, expires)
	authRateResetIP(c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"user": gin.H{
		"username": user.Username,
		"nickname": user.Nickname,
		"avatar":   user.Avatar,
	}})
}

// POST /api/auth/login {username, password}
func (h *Handler) AuthLogin(c *gin.Context) {
	if ok, wait := authLimiter.Allow(c.ClientIP()); !ok {
		authTooMany(c, wait)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверный запрос"})
		return
	}
	acc := GetAccounts()
	user, err := acc.Authenticate(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	token, expires, err := acc.CreateSession(user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "не удалось создать сессию"})
		return
	}
	setSessionCookie(c, token, expires)
	authRateResetIP(c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"user": gin.H{
		"username": user.Username,
		"nickname": user.Nickname,
		"avatar":   user.Avatar,
	}})
}

func authTooMany(c *gin.Context, wait time.Duration) {
	secs := int(wait.Seconds())
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":       "слишком много попыток входа, повторите позже",
		"retry_after": secs,
	})
}

// POST /api/auth/logout
func (h *Handler) AuthLogout(c *gin.Context) {
	token, err := c.Cookie(sessionCookieName)
	if err == nil {
		GetAccounts().DeleteSession(token)
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/auth/me
func (h *Handler) AuthMe(c *gin.Context) {
	acc := GetAccounts()
	if u := sessionUser(c); u != "" {
		user, ok := acc.Get(u)
		if ok {
			c.JSON(http.StatusOK, gin.H{
				"authed": true,
				"user": gin.H{
					"username": user.Username,
					"nickname": user.Nickname,
					"avatar":   user.Avatar,
				},
				"users_exist": true,
			})
			return
		}
	}
	if TokenMatches(c) {
		c.JSON(http.StatusOK, gin.H{
			"authed":      true,
			"user":        gin.H{"username": "token", "nickname": "", "avatar": ""},
			"users_exist": acc.Count() > 0,
			"token_mode":  true,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"authed":      false,
		"users_exist": acc.Count() > 0,
	})
}

// POST /api/profile/meta {nickname, avatar}
func (h *Handler) UpdateProfileMeta(c *gin.Context) {
	u := c.GetString("briefly_user")
	if u == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется вход"})
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "неверный запрос"})
		return
	}
	if err := GetAccounts().SetMeta(u, req.Nickname, req.Avatar); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
