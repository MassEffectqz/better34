package internal

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
)

// ── QR-вход на другом устройстве ─────────────────────────────────────────
// Залогиненный пользователь показывает QR с одноразовым токеном; телефон
// сканирует его, страница /qr отправляет токен на claim и получает готовую
// сессию — пароль на втором устройстве вводить не нужно. Токен живёт 5
// минут, сгорает после первого использования.

const qrLoginTTL = 5 * time.Minute

type qrLoginEntry struct {
	username string
	expires  time.Time
}

type qrLoginStore struct {
	mu      sync.Mutex
	pending map[string]qrLoginEntry
}

var qrLogins = &qrLoginStore{pending: make(map[string]qrLoginEntry)}

func (q *qrLoginStore) create(username string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// rand.Read из crypto/rand падает только при системном сбое.
		panic(err)
	}
	token := hex.EncodeToString(buf)
	now := time.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	for t, e := range q.pending {
		if now.After(e.expires) {
			delete(q.pending, t)
		}
	}
	q.pending[token] = qrLoginEntry{username: username, expires: now.Add(qrLoginTTL)}
	return token
}

func (q *qrLoginStore) claim(token string) (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.pending[token]
	if !ok {
		return "", false
	}
	delete(q.pending, token) // одноразовый: сгорает даже при истёкшем сроке
	if time.Now().After(e.expires) {
		return "", false
	}
	return e.username, true
}

// lanHost возвращает host:port для QR-ссылок, подставляя LAN IP
// вместо localhost/127.x.
func lanHost(c *gin.Context) string {
	h := c.Request.Host
	if h == "" || strings.HasPrefix(h, "localhost") || strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "[::1]") {
		// Извлечём порт из оригинального Host.
		port := ""
		if i := strings.LastIndex(h, ":"); i != -1 {
			port = h[i:]
		}
		ifaces, err := net.Interfaces()
		if err == nil {
			for _, ifc := range ifaces {
				if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
					continue
				}
				addrs, err := ifc.Addrs()
				if err != nil {
					continue
				}
				for _, a := range addrs {
					var ip net.IP
					switch v := a.(type) {
					case *net.IPNet:
						ip = v.IP
					case *net.IPAddr:
						ip = v.IP
					}
					if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
						continue
					}
					s := ip.String()
					if strings.Contains(s, ".") {
						return s + port
					}
				}
			}
		}
	}
	return h
}

// UserExists проверяет, что аккаунт ещё есть (QR мог прожить дольше аккаунта).
func (a *Accounts) UserExists(username string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.users[strings.ToLower(strings.TrimSpace(username))]
	return ok
}

// QRCreate выдаёт новый одноразовый токен и ссылку для QR. Только для
// залогиненных: владелец сессии «одалживает» свой вход другому устройству.
func (h *Handler) QRCreate(c *gin.Context) {
	user := sessionUser(c)
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	token := qrLogins.create(user)
	scheme := "http"
	if requestSecure(c) {
		scheme = "https"
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"ttl":   int(qrLoginTTL.Seconds()),
		"url":   scheme + "://" + lanHost(c) + "/qr?t=" + token,
	})
}

// QRSVG отдаёт QR-картинку (PNG) со ссылкой подтверждения — для <img>.
func (h *Handler) QRSVG(c *gin.Context) {
	user := sessionUser(c)
	if user == "" {
		c.Status(http.StatusUnauthorized)
		return
	}
	token := qrLogins.create(user)
	scheme := "http"
	if requestSecure(c) {
		scheme = "https"
	}
	png, err := qrcode.Encode(scheme+"://"+lanHost(c)+"/qr?t="+token, qrcode.Medium, 256)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "qr encode failed"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "image/png", png)
}

// QRClaim обмен токена на сессию. Без авторизации — это и есть вход.
func (h *Handler) QRClaim(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}
	user, ok := qrLogins.claim(req.Token)
	if !ok {
		AbortWithError(c, ErrQRInvalid)
		return
	}
	acc := GetAccounts()
	if !acc.UserExists(user) {
		AbortWithError(c, ErrQRNoAccount)
		return
	}
	token, expires, err := acc.CreateSession(user)
	if err != nil {
		AbortWithError(c, ErrSessionCreate)
		return
	}
	setSessionCookie(c, token, expires)
	c.JSON(http.StatusOK, gin.H{"ok": true, "username": user})
}

// QRPage отдаёт страницу подтверждения для телефона (открывается по QR).
func (h *Handler) QRPage(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(qrClaimHTML))
}

const qrClaimHTML = `<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>better34 — QR login</title>
<style>
body{background:#0f0f0f;color:#eee;font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1a1a;border-radius:16px;padding:32px;text-align:center;max-width:320px}
.ok{color:#7ee787}.err{color:#f85149}
a{color:#a78bfa}
</style>
</head>
<body>
<div class="card" id="msg"></div>
<script>
const I18N = {
  ru: {
    confirming: 'Подтверждаем вход…',
    noToken: 'Ссылка неполная: нет кода.',
    success: (u) => 'Вход выполнен как ' + u,
    openApp: 'Открыть better34',
    failed: 'Не удалось подтвердить вход.',
    offline: 'Сервер недоступен.',
  },
  en: {
    confirming: 'Confirming login…',
    noToken: 'Incomplete link: no code.',
    success: (u) => 'Logged in as ' + u,
    openApp: 'Open better34',
    failed: 'Login failed.',
    offline: 'Server unavailable.',
  }
};
let lang = 'ru';
try {
  const c = document.cookie.match(/briefly_lang=(ru|en)/);
  if (c) lang = c[1];
  else if ((navigator.language||'').toLowerCase().startsWith('en')) lang = 'en';
} catch {}
const m = I18N[lang];
const msg = document.getElementById('msg');
msg.textContent = m.confirming;
(async () => {
  const token = new URLSearchParams(location.search).get('t');
  if (!token) { msg.textContent = m.noToken; msg.className = 'err'; return; }
  try {
    const res = await fetch('/api/auth/qr/claim', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token })
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok && data.ok) {
      msg.innerHTML = '<span class="ok">' + m.success(data.username) + '</span><br><br><a href="/">' + m.openApp + '</a>';
      setTimeout(() => { location.href = '/'; }, 1200);
    } else {
      msg.textContent = data.error || m.failed;
      msg.className = 'err';
    }
  } catch (e) {
    msg.textContent = m.offline;
    msg.className = 'err';
  }
})();
</script>
</body>
</html>`
