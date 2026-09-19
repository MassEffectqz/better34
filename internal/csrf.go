package internal

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const CsrfCookieName = "briefly_csrf"

// CsrfMiddleware генерирует и валидирует CSRF-токены для cookie-сессий.
func CsrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		if IsTokenAuth(c) {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/auth/login") ||
			strings.HasPrefix(path, "/api/auth/register") ||
			strings.HasPrefix(path, "/api/auth/qr/") {
			c.Next()
			return
		}

		cookie, err := c.Cookie(CsrfCookieName)
		if err != nil || cookie == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "csrf_missing", "message": "CSRF token cookie not found",
			})
			return
		}

		header := c.GetHeader("X-CSRF-Token")
		if header == "" || header != cookie {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "csrf_invalid", "message": "CSRF token mismatch",
			})
			return
		}

		c.Next()
	}
}

// SetCSRFToken ставит httpOnly=false cookie с случайным токеном.
func SetCSRFToken(c *gin.Context) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return
	}
	token := hex.EncodeToString(buf)
	c.SetCookie(CsrfCookieName, token, 0, "/", "", false, true)
}

// IsTokenAuth проверяет, что запрос использует Bearer token (не cookie-сессию).
func IsTokenAuth(c *gin.Context) bool {
	if t := c.GetHeader("X-Briefly-Token"); t != "" {
		return true
	}
	auth := c.GetHeader("Authorization")
	return strings.HasPrefix(auth, "Bearer ")
}
