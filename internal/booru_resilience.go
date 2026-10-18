package internal

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"
)

// singleflightGroup объединяет параллельные одинаковые запросы в один.
type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	done  chan struct{}
	posts []Rule34Post
	err   error
}

func (g *singleflightGroup) Do(key string, fn func() ([]Rule34Post, error)) ([]Rule34Post, error) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.posts, c.err
	}
	c := &sfCall{done: make(chan struct{})}
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	g.m[key] = c
	g.mu.Unlock()
	c.posts, c.err = fn()
	g.mu.Lock()
	delete(g.m, key)
	close(c.done)
	g.mu.Unlock()
	return c.posts, c.err
}

// suggestFlightGroup — singleflight для подсказок: при быстрой печати
// одинаковые префиксы (to → tou → touh) дедуплицируются в один запрос,
// вместо параллельного шторма по autocomplete.php.
type suggestFlightGroup struct {
	mu sync.Mutex
	m  map[string]*suggFlightCall
}

type suggFlightCall struct {
	done chan struct{}
	tags []TagSuggestion
	err  error
}

func (g *suggestFlightGroup) Do(key string, fn func() ([]TagSuggestion, error)) ([]TagSuggestion, error) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.tags, c.err
	}
	c := &suggFlightCall{done: make(chan struct{})}
	if g.m == nil {
		g.m = make(map[string]*suggFlightCall)
	}
	g.m[key] = c
	g.mu.Unlock()
	c.tags, c.err = fn()
	g.mu.Lock()
	delete(g.m, key)
	close(c.done)
	g.mu.Unlock()
	return c.tags, c.err
}

// circuitBreaker останавливает запросы к API после серии ошибок.
type circuitBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const breakerMaxFailures = 5
const breakerCoolDown = 30 * time.Second

func (b *circuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.openUntil)
}

func (b *circuitBreaker) Fail(site string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.openUntil) {
		return
	}
	b.failures++
	if b.failures >= breakerMaxFailures {
		b.openUntil = time.Now().Add(breakerCoolDown)
		b.failures = 0
		log.Printf("[breaker] %s API открыт на %dс — слишком много ошибок подряд", site, int(breakerCoolDown.Seconds()))
	}
}

func (b *circuitBreaker) Success() {
	b.mu.Lock()
	b.failures = 0
	b.mu.Unlock()
}

func keyTail(key string) string {
	if len(key) > 6 {
		return key[len(key)-6:]
	}
	return key
}

func jitteredBackoff(attempt int) time.Duration {
	base := 300 * time.Millisecond * time.Duration(1<<(attempt-1))
	if base > 3*time.Second {
		base = 3 * time.Second
	}
	return base + time.Duration(rand.Intn(150))*time.Millisecond
}

// Глобальный троттлинг на сайт (token bucket): сглаживает всплески —
// fan-out tag-count, подсказки на каждое нажатие клавиши, ретраи —
// чтобы апстрим не отвечал 429. Значения — package-level переменные,
// чтобы тесты могли их отпустить.
var (
	apiRatePerSecond = 2.5
	apiRateBurst     = 5.0
	// Выборка постов по id идёт через ОТДЕЛЬНОЕ ведро. Пока она делила
	// троттлинг с поиском, одна выборка профиля (сотня id, каждый —
	// отдельный запрос: gelbooru не понимает списки id) съедала все токены
	// сайта, и обычный поиск ленты вставал в очередь — клиент на телефоне
	// получал таймаут вместо выдачи. Ведро поиска остаётся прежним, т.е.
	// суммарная частота на сайт выросла, но интерактивный поиск больше не
	// голодает.
	idRatePerSecond = 5.0
	idRateBurst     = 10.0
)

// logAPIKeyDebug — включается BRIEFLY_DEBUG_API=1. Лог [api-key-check]

type rateLimiter struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	perSec float64
	burst  float64
}

func newRateLimiter() *rateLimiter {
	return newRateLimiterAt(apiRatePerSecond, apiRateBurst)
}

func newRateLimiterAt(perSec, burst float64) *rateLimiter {
	return &rateLimiter{tokens: burst, last: time.Now(), perSec: perSec, burst: burst}
}

// Wait nil-безопасен: клиенты из тестов с limiter=nil не троттлятся.
func (r *rateLimiter) Wait() {
	_ = r.WaitCtx(context.Background())
}

// WaitCtx — Wait с отменой. Ожидание токена — тоже часть работы: клиент,
// ушедший по своему дедлайну, не должен висеть в троттлинге и потом
// отправить запрос в источник.
func (r *rateLimiter) WaitCtx(ctx context.Context) error {
	if r == nil {
		return nil
	}
	for {
		r.mu.Lock()
		now := time.Now()
		r.tokens += now.Sub(r.last).Seconds() * r.perSec
		if r.tokens > r.burst {
			r.tokens = r.burst
		}
		r.last = now
		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - r.tokens) / r.perSec * float64(time.Second))
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// sleepCtx — пауза перед ретраем с учётом отмены. false означает «контекст
// убит, ретрай бессмысленен».
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
