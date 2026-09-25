package main

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// apiRateLimiter — token bucket rate limiter для API-эндпоинтов.
// Каждый IP получает burstsPerSec токенов в секунду; при превышении — 429.
type apiRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	burst     float64
	rate      float64
	maxAge    time.Duration
	lastPrune time.Time
}

type tokenBucket struct {
	tokens   float64
	lastTime time.Time
}

var apiLimiter = &apiRateLimiter{
	buckets: make(map[string]*tokenBucket),
	// S-5: middleware подключён к группе /api (раньше был мёртвым кодом).
	// Параметры консервативные: burst 120 покрывает первичную загрузку ленты
	// (~60 превью + /posts + /profile + /events), а 60 req/s всё равно
	// отсекает абуз. Ложные 429 на легитимном трафике исключены.
	burst:     120,
	rate:      60,
	maxAge:    5 * time.Minute,
	lastPrune: time.Now(),
}

// mediaLimiter — отдельное ведро для медиавыдачи (/api/thumb, /api/file,
// /api/proxy). Сетка профиля запрашивает сотни миниатюр разом; общий лимит
// 60 req/s превращал это в каскад 429, и превью оставались битыми. Запросы
// идемпотентные и кэшируются, поэтому здесь нужен запас по burst и rate,
// а не полное снятие троттлинга.
var mediaLimiter = &apiRateLimiter{
	buckets: make(map[string]*tokenBucket),
	burst:   1000,
	rate:    480,
	maxAge:  5 * time.Minute,
}

func (rl *apiRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, lastTime: now}
		rl.buckets[ip] = b
	}

	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *apiRateLimiter) prune() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	if now.Sub(rl.lastPrune) < rl.maxAge {
		return
	}
	rl.lastPrune = now

	cutoff := now.Add(-rl.maxAge)
	for ip, b := range rl.buckets {
		if b.lastTime.Before(cutoff) {
			delete(rl.buckets, ip)
		}
	}
}

func apiRateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Служебные эндпоинты мониторинга не троттлим: healthz опрашивается
		// внешним мониторингом чаще чем 60 req/s невозможен, но лимит не должен
		// ломать /metrics скрейпера или readiness-пробы оркестратора.
		switch c.FullPath() {
		case "/api/healthz", "/api/ready", "/api/metrics":
			c.Next()
			return
		}
		// Медиа (превью, файлы, прокси) — это сотни запросов на одну
		// страницу: сетка лайков дергает миниатюры пачками по 60+ штук.
		// Общий лимит 60 req/s превращался в каскад 429 и битые превью,
		// хотя запросы идемпотентные и у каждого свой кэш. Троттлим их
		// отдельным, в 8 раз более щедрым ведром.
		if isMediaPath(c.Request.URL.Path) {
			if !mediaLimiter.allow(c.ClientIP()) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error":   "rate_limited",
					"message": "too many media requests, try again later",
				})
				return
			}
			mediaLimiter.prune()
			c.Next()
			return
		}
		ip := c.ClientIP()
		if !apiLimiter.allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limited",
				"message": "too many requests, try again later",
			})
			return
		}
		// prune дёшев (внутри throttle по lastPrune), горутина на каждый запрос
		// не нужна — вызываем синхронно.
		apiLimiter.prune()
		c.Next()
	}
}

// isMediaPath — запросы медиавыдачи. Идемпотентны, отдаются из кэша/CDN
// и не должны конкурировать за общий лимит с логикой приложения.
func isMediaPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/api/thumb/"):
		return true
	case strings.HasPrefix(p, "/api/file/"):
		return true
	case strings.HasPrefix(p, "/api/proxy"):
		return true
	}
	return false
}
