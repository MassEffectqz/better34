package main

import (
	"briefly/internal"

	"github.com/gin-gonic/gin"
)

func csrfMiddleware() gin.HandlerFunc {
	return internal.CsrfMiddleware()
}
