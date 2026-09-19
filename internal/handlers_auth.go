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
	acc := GetAccounts()
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	user, err := acc.Register(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	token, expires, err := acc.CreateSession(user.Username)
	if err != nil {
		AbortWithError(c, ErrSessionCreate)
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
		AbortWithError(c, ErrInvalidRequest)
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
		AbortWithError(c, ErrSessionCreate)
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
		"error":       ErrRateLimited.Code,
		"message":     ErrRateLimited.Message,
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
				"users_exist":   true,
				"is_admin":      acc.IsAdmin(u),
				"register_open": true,
			})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"authed":        false,
		"users_exist":   acc.Count() > 0,
		"register_open": true,
	})
}

// POST /api/profile/meta {nickname, avatar}
func (h *Handler) UpdateProfileMeta(c *gin.Context) {
	u := c.GetString("briefly_user")
	if u == "" {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
		Avatar   string `json:"avatar"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	if err := GetAccounts().SetMeta(u, req.Nickname, req.Avatar); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/auth/password {current_password, new_password} — смена пароля.
// Текущий пароль обязателен; после смены остальные сессии отзываются.
func (h *Handler) AuthChangePassword(c *gin.Context) {
	u := sessionUser(c)
	if u == "" {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	if ok, wait := authLimiter.Allow(c.ClientIP()); !ok {
		authTooMany(c, wait)
		return
	}
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	if err := GetAccounts().ChangePassword(u, req.Current, req.New); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if token, err := c.Cookie(sessionCookieName); err == nil {
		GetAccounts().RevokeOtherSessions(u, token)
	}
	authRateResetIP(c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/auth/logout-others — отозвать все сессии, кроме текущей
// (например, если пароль или устройство попали в чужие руки).
func (h *Handler) AuthLogoutOthers(c *gin.Context) {
	u := sessionUser(c)
	if u == "" {
		AbortWithError(c, ErrAuthRequired)
		return
	}
	token, _ := c.Cookie(sessionCookieName)
	revoked := GetAccounts().RevokeOtherSessions(u, token)
	c.JSON(http.StatusOK, gin.H{"ok": true, "revoked": revoked})
}
