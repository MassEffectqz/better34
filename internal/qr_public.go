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
	t := qtok()
	qrStr.mu.Lock()
	qrStr.m[t] = &qrSess{tok: t, st: qrPend, expiry: time.Now().Add(qrSessTTL)}
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

func qrPubPage(token string) string {
	return `<!DOCTYPE html><html lang="ru"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>better34 QR</title>
<style>body{background:#0f0f0f;color:#eee;font-family:system-ui;margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center}
.c{background:#1a1a1a;border-radius:16px;padding:26px;max-width:340px;width:92%;text-align:center;box-shadow:0 8px 40px rgba(0,0,0,.5)}
input{width:100%;box-sizing:border-box;padding:10px;margin:6px 0;border-radius:8px;border:1px solid #333;background:#111;color:#eee}
button{width:100%;padding:11px;margin-top:8px;border:none;border-radius:8px;background:#7c5cbf;color:#fff;cursor:pointer}
.e{color:#f85149;min-height:18px}.o{color:#7ee787}</style></head><body>
<div class="c" id="a"><div id="s">Загрузка...</div></div>
<script>
var T="` + token + `";function $(d){return document.getElementById(d)}function esc(s){var e=document.createElement("div");e.textContent=s;return e.innerHTML}
function post(u,b){return fetch(u,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(b)}).then(function(r){return r.json().then(function(j){return{ok:r.ok,r:r,j:j}})})}
function claim(){$("s").textContent="Передаём...";post("/api/auth/qr/claim",{token:T}).then(function(x){if(x.ok){$("a").innerHTML='<div class="o" style="font-size:44px">✓</div><h3>Вход передан!</h3><p>Закройте вкладку.</p>'}else $("a").innerHTML='<div class="e">'+esc(x.j.error||("HTTP "+x.r.status))+'</div><button onclick="location.reload()">Повторить</button>'})}
function lf(m){$("a").innerHTML='<h3 style="margin-top:0">Войдите для передачи</h3>'+(m?'<div class="e">'+esc(m)+"</div>":"")+'<input id="u" placeholder="Логин"><input id="p" type="password" placeholder="Пароль"><button id="g">Войти и передать</button><div class="e" id="e"></div>';$("g").onclick=function(){var u=$("u").value.trim(),p=$("p").value;if(!u||!p){$("e").textContent="Заполните поля";return}post("/api/auth/login",{username:u,password:p}).then(function(x){if(!x.ok){$("e").textContent=x.j.error||"Ошибка";return}claim()})}}
if(!T){$("a").innerHTML='<div class="e">Нет кода в ссылке.</div>';}else{fetch("/api/auth/me").then(function(r){return r.json()}).then(function(d){if(d&&d.user){claim()}else{lf()}}).catch(function(){lf()})}
</script></body></html>`
}
