package internal

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
)

// ── Публичный QR-вход (с экрана авторизации) ─────────────────────────────
// Пользователь ещё НЕ залогинен. ПК создаёт QR-сессию, телефон (уже
// залогиненный или вводящий пароль на странице /qr) забирает её и
// привязывает к своему аккаунту. ПК опрашивает статус, получает одноразовый
// код обмена и обменивает его на сессию — вход без ввода пароля на ПК.

const qrSessTTL = 5 * time.Minute

type qrSt string

const (
	qrPend qrSt = "pending"
	qrClm  qrSt = "claimed"
	qrDone qrSt = "exchanged"
)

type qrSess struct {
	tok    string
	user   string
	code   string
	st     qrSt
	expiry time.Time
}

type qrStore struct {
	mu sync.Mutex
	m  map[string]*qrSess
}

var qrStr = &qrStore{m: make(map[string]*qrSess)}

func qtok() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) QRCreateSession(c *gin.Context) {
	// Публичный эндпоинт: генерация токена без авторизации — ограничиваем.
	if ok, wait := authLimiter.Allow(c.ClientIP()); !ok {
		authTooMany(c, wait)
		return
	}
	t := qtok()
	now := time.Now()
	qrStr.mu.Lock()
	// Выметаем истёкшие сессии: иначе карта растёт бесконечно.
	for k, s := range qrStr.m {
		if now.After(s.expiry) {
			delete(qrStr.m, k)
		}
	}
	qrStr.m[t] = &qrSess{tok: t, st: qrPend, expiry: now.Add(qrSessTTL)}
	qrStr.mu.Unlock()
	c.JSON(200, gin.H{"token": t, "expires_in": int(qrSessTTL.Seconds())})
}

func (h *Handler) QRPollSession(c *gin.Context) {
	t := c.Query("token")
	qrStr.mu.Lock()
	s, ok := qrStr.m[t]
	if !ok {
		qrStr.mu.Unlock()
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if time.Now().After(s.expiry) {
		delete(qrStr.m, t)
		qrStr.mu.Unlock()
		c.JSON(410, gin.H{"status": "expired"})
		return
	}
	if s.st == qrPend {
		qrStr.mu.Unlock()
		c.JSON(200, gin.H{"status": "pending"})
		return
	}
	if s.st == qrClm {
		code := s.code
		qrStr.mu.Unlock()
		c.JSON(200, gin.H{"status": "claimed", "exchange_code": code})
		return
	}
	qrStr.mu.Unlock()
	c.JSON(200, gin.H{"status": "exchanged"})
}

func (h *Handler) QRExchangeSession(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Code == "" {
		c.JSON(400, gin.H{"error": "code required"})
		return
	}
	qrStr.mu.Lock()
	var s *qrSess
	var tk string
	for k, v := range qrStr.m {
		if v.code == req.Code && v.st == qrClm {
			s, tk = v, k
			break
		}
	}
	if s == nil {
		qrStr.mu.Unlock()
		c.JSON(403, gin.H{"error": "invalid code"})
		return
	}
	u := s.user
	s.st = qrDone
	delete(qrStr.m, tk)
	qrStr.mu.Unlock()
	if !GetAccounts().UserExists(u) {
		c.JSON(403, gin.H{"error": "account not found"})
		return
	}
	tok, exp, err := GetAccounts().CreateSession(u)
	if err != nil {
		c.JSON(500, gin.H{"error": "session failed"})
		return
	}
	setSessionCookie(c, tok, exp)
	c.JSON(200, gin.H{"ok": true, "username": u})
}

func (h *Handler) QRImagePublic(c *gin.Context) {
	t := c.Query("t")
	if t == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	// Публичный эндпоинт: PNG генерируется на каждый вызов — ограничиваем,
	// чтобы флуд не грузил CPU. Токен высокоэнтропийный, перебор невозможен.
	if ok, wait := authLimiter.Allow(c.ClientIP()); !ok {
		authTooMany(c, wait)
		return
	}
	scheme := "http"
	if requestSecure(c) {
		scheme = "https"
	}
	png, err := qrcode.Encode(scheme+"://"+lanHost(c)+"/qr?t="+t, qrcode.Medium, 256)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "image/png", png)
}
