package internal

import (
	"sync"
	"time"
)

// Rate-limit для эндпоинтов входа/регистрации: сервер слушает все
// интерфейсы, перебор паролей должен упираться в задержки.
// Схема: не более authRateMax попыток за authRateWindow на IP;
// превышение -> блокировка с экспоненциальным ростом (1м, 2м, 4м...,
// кап 30м). Успешный вход сбрасывает счётчик IP.
const (
	authRateMax      = 5
	authRateWindow   = time.Minute
	authBlockBase    = time.Minute
	authBlockCap     = 30 * time.Minute
	authLimiterMaxIP = 4096
)

type authAttempt struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
	violations   int
}

type AuthRateLimiter struct {
	mu  sync.Mutex
	m   map[string]*authAttempt
	now func() time.Time
}

var authLimiter = &AuthRateLimiter{m: make(map[string]*authAttempt), now: time.Now}

// Allow проверяет и учитывает попытку. Возвращает (разрешено, остаток блокировки).
func (l *AuthRateLimiter) Allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	a, ok := l.m[ip]
	if !ok {
		if len(l.m) >= authLimiterMaxIP {
			l.pruneLocked(now)
		}
		a = &authAttempt{}
		l.m[ip] = a
	}

	if now.Before(a.blockedUntil) {
		return false, a.blockedUntil.Sub(now)
	}
	if a.windowStart.IsZero() || now.Sub(a.windowStart) > authRateWindow {
		a.windowStart = now
		a.count = 0
	}
	a.count++
	if a.count > authRateMax {
		a.violations++
		backoff := authBlockBase << (a.violations - 1)
		if backoff > authBlockCap || backoff <= 0 {
			backoff = authBlockCap
		}
		a.blockedUntil = now.Add(backoff)
		return false, backoff
	}
	return true, 0
}

// Reset снимает ограничения после успешного логина.
func (l *AuthRateLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

func (l *AuthRateLimiter) pruneLocked(now time.Time) {
	for ip, a := range l.m {
		if now.After(a.blockedUntil) && (a.windowStart.IsZero() || now.Sub(a.windowStart) > authRateWindow) {
			delete(l.m, ip)
		}
	}
	// Всё ещё переполнено (флуд живых записей) — грубая очистка.
	if len(l.m) >= authLimiterMaxIP {
		l.m = make(map[string]*authAttempt)
	}
}

func authRateResetIP(ip string) { authLimiter.Reset(ip) }
