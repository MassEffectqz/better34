package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// apiRateLimiter — token bucket rate limiter для API-эндпоинтов.
// Каждый IP получает burstsPerSec токенов в секунду; при превышении — 429.
type apiRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	burst    float64
	rate     float64
	maxAge   time.Duration
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
	burst:   120,
	rate:    60,
	maxAge:  5 * time.Minute,
	lastPrune: time.Now(),
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
