package internal

import (
	"testing"
	"time"
)

func newTestLimiter(now func() time.Time) *AuthRateLimiter {
	return &AuthRateLimiter{m: make(map[string]*authAttempt), now: now}
}

func TestAuthRateLimiterAllowsFivePerWindow(t *testing.T) {
	t0 := time.Now()
	l := newTestLimiter(func() time.Time { return t0 })
	for i := 0; i < 5; i++ {
		ok, _ := l.Allow("1.2.3.4")
		if !ok {
			t.Fatalf("попытка %d отклонена, ожидалось разрешение", i+1)
		}
	}
	if ok, wait := l.Allow("1.2.3.4"); ok {
		t.Fatal("6-я попытка в окне разрешена")
	} else if wait <= 0 {
		t.Fatalf("wait = %v, должен быть положительным", wait)
	}
}

func TestAuthRateLimiterExponentialBackoff(t *testing.T) {
	t0 := time.Now()
	now := t0
	l := newTestLimiter(func() time.Time { return now })

	// Исчерпываем окно и получаем первую блокировку.
	for i := 0; i < 6; i++ {
		l.Allow("ip")
	}
	// violations = 1 -> блок ~1м; ждём её и снова переполняем.
	now = t0.Add(authBlockBase + authRateWindow)
	for i := 0; i < 6; i++ {
		l.Allow("ip")
	}
	ok, wait := l.Allow("ip")
	if ok {
		t.Fatal("вторая волна должна заблокировать")
	}
	if want := 2 * authBlockBase; wait < want-time.Second || wait > want+time.Second {
		t.Fatalf("backoff = %v, ожидался ~%v (экспоненциальный рост)", wait, want)
	}
}

func TestAuthRateLimiterResetOnSuccess(t *testing.T) {
	t0 := time.Now()
	l := newTestLimiter(func() time.Time { return t0 })
	for i := 0; i < 5; i++ {
		l.Allow("ip")
	}
	l.Reset("ip")
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("после Reset попытка должна быть разрешена")
	}
}

func TestAuthRateLimiterIndependentIPs(t *testing.T) {
	t0 := time.Now()
	l := newTestLimiter(func() time.Time { return t0 })
	for i := 0; i < 6; i++ {
		l.Allow("a")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("блокировка одного IP не должна задевать другой")
	}
}

func TestAuthRateLimiterBlockExpires(t *testing.T) {
	t0 := time.Now()
	now := t0
	l := newTestLimiter(func() time.Time { return now })
	for i := 0; i < 6; i++ {
		l.Allow("ip")
	}
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("должны быть заблокированы")
	}
	now = t0.Add(authBlockBase + time.Second)
	// Блокировка истекла — счётчик окна тоже устарел, новая волна разрешена.
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("попытка %d после истечения блокировки отклонена", i+1)
		}
	}
}

func TestAuthRateLimiterPrune(t *testing.T) {
	t0 := time.Now()
	now := t0
	l := newTestLimiter(func() time.Time { return now })
	// Забиваем до порога живыми записями.
	for i := 0; i < authLimiterMaxIP; i++ {
		if ok, _ := l.Allow(string(rune('a'+i%26)) + string(rune('0'+i/26))); !ok && i < 5 {
			t.Fatalf("раннее отклонение на %d", i)
		}
	}
	// Плюс одна заблокированная.
	for i := 0; i < 6; i++ {
		l.Allow("blocked-ip")
	}
	now = now.Add(2 * authRateWindow).Add(authBlockCap)
	l.Allow("trigger-prune")
	if len(l.m) > authLimiterMaxIP {
		t.Fatalf("prune не сработал: %d записей", len(l.m))
	}
}
