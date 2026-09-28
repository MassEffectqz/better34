package internal

// HTTP-слой системы друзей.
//
// Два мира эндпоинтов:
//   /api/friends*  — наш собственный UI: список, добавление, обмен.
//   /api/friend/*  — «дверь» для чужих инстансов: отдать своё / принять его.
//                    Аутентификация — заголовок X-Briefly-Friend-Key.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// friendSelfURL собирает адрес, по которому пришёл текущий запрос. Это НЕ то,
// что нужно класть в friend-код: пользователь часто открыт по localhost или
// 127.0.0.1, а друг стучится по адресу из Radmin/Tailscale-сети. Код должен
// нести адрес из SelfURL(), см. friendPublicURL.
func friendSelfURL(c *gin.Context) string {
	scheme := "http"
	if requestSecure(c) {
		scheme = "https"
	}
	h := c.Request.Host
	if h == "" {
		return ""
	}
	return scheme + "://" + h
}

// friendPublicURL — адрес, который мы РЕКЛАМИРУЕМ друзьям: сначала
// определённый приложением (BRIEFLY_FRIENDS_URL или адрес VPN-карты), и
// только если он не определён — адрес текущего запроса. Иначе код, отданный
// другу, содержал бы localhost, и синк молча не работал бы.
func friendPublicURL(c *gin.Context) string {
	if u := SelfURL(); u != "" {
		return u
	}
	return friendSelfURL(c)
}

// friendErr отдаёт ошибку с кодом для i18n.
func friendErr(c *gin.Context, err error) {
	code := "friend_error"
	switch {
	case err == ErrFriendCodeInvalid:
		code = "friend_code_invalid"
	case err == ErrFriendSelf:
		code = "friend_self"
	case err == ErrFriendExists:
		code = "friend_exists"
	case err == ErrFriendNotFound:
		code = "friend_not_found"
	case err == ErrFriendAuth:
		code = "friend_auth"
	case err == ErrFriendBody:
		code = "friend_bad_body"
	}
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": code, "message": err.Error()})
}

// friendView — как друг выглядит в UI. Ключи наружу НЕ отдаём: это bearer,
// и он не должен попадать в список, который клиент кладёт в localStorage.
type friendView struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Username string `json:"username,omitempty"`
	AddedAt  string `json:"added_at"`
	LastSync string `json:"last_sync,omitempty"`
	LastErr  string `json:"last_error,omitempty"`
}

func (f Friend) view() friendView {
	return friendView{
		ID: f.ID(), URL: f.URL, Nickname: f.Nickname, Avatar: f.Avatar,
		Username: f.Username, AddedAt: f.AddedAt,
		LastSync: f.LastSync, LastErr: f.LastError,
	}
}

// GET /api/friends — список друзей.
func (h *Handler) ListFriends(c *gin.Context) {
	user := c.GetString("briefly_user")
	store := GetFriendStore(user)
	list := store.List()
	out := make([]friendView, 0, len(list))
	for _, f := range list {
		out = append(out, f.view())
	}
	c.JSON(http.StatusOK, gin.H{"friends": out})
}

// GET /api/friends/code — мой код для передачи другу («визитка»).
func (h *Handler) MyFriendCode(c *gin.Context) {
	user := c.GetString("briefly_user")
	nickname, _ := friendIdentity(user)
	code, err := GetFriendStore(user).EncodeFriendCode(user, nickname, friendPublicURL(c))
	if err != nil {
		friendErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": code})
}

// POST /api/friends {code} — добавить друга.
func (h *Handler) AddFriend(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	user := c.GetString("briefly_user")
	nickname, _ := friendIdentity(user)
	f, err := GetFriendStore(user).AddFriend(req.Code, friendSelfURL(c), user, nickname)
	if err != nil {
		friendErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"friend": f.view()})
}

// DELETE /api/friends/:id — удалить друга.
func (h *Handler) DeleteFriend(c *gin.Context) {
	user := c.GetString("briefly_user")
	if err := GetFriendStore(user).Remove(c.Param("id")); err != nil {
		friendErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/friends/sync — обмен прямо сейчас (кнопка в UI). Фоновый цикл
// делает то же само, но ждать пять минут ради разовой проверки не нужно.
func (h *Handler) SyncFriends(c *gin.Context) {
	user := c.GetString("briefly_user")
	stats := SyncAllFriends(user, friendSelfURL(c))
	c.JSON(http.StatusOK, gin.H{"synced": stats})
}

// ── Дверь для чужих инстансов ────────────────────────────────────────────────
// Эти два эндпоинта — весь сетевой интерфейс friend-синка. Они сознательно
// живут вне /api/friends и авторизуются НЕ сессией, а ключом друга из
// заголовка: инстанс-друг не имеет и не может иметь нашу cookie.

// friendKeyFrom достаёт ключ из заголовка. Заголовок, а не query: query
// попадает в access-логи и в историю браузера.
func friendKeyFrom(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Briefly-Friend-Key"))
}

// friendByKey ищет пользователя-владельца присланного ключа.
//
// Запрос от чужого инстанса идёт БЕЗ сессии (нашей cookie у него нет), и по
// одному заголовку непонятно, чей это ключ. Ищем среди аккаунтов: их единицы,
// ключ имеет 32 байта энтропии, перебор по сети бессмыслен.
//
// Ключ не различает друзей: он один на пользователя, поэтому «кто именно
// пришёл» определяется отдельно — по заголовку X-Briefly-Friend-From, и
// адрес обязан совпасть с записью в нашем списке (см. friendByURL).
func friendByKey(got string) (string, bool) {
	got = strings.TrimSpace(got)
	if got == "" {
		return "", false
	}
	accs := GetAccounts()
	if accs.Count() == 0 {
		// Режим без аккаунтов: владелец — legacy-профиль.
		if GetFriendStore("").MatchesKey(got) {
			return "", true
		}
		return "", false
	}
	for _, u := range accs.Users() {
		if GetFriendStore(u.Username).MatchesKey(got) {
			return u.Username, true
		}
	}
	return "", false
}

// friendByURL ищет запись друга по адресу среди друзей пользователя. Это
// источник правды об отправителе: адрес пришёл с friend-кодом при добавлении,
// поэтому подменить его без ключа нельзя. Он же служит пространством имён
// для чужих комментариев (см. friendCommentUsername).
func friendByURL(user, addr string) (Friend, bool) {
	addr = strings.TrimSpace(strings.ToLower(addr))
	if addr == "" {
		return Friend{}, false
	}
	norm, err := normalizeFriendURL(addr)
	if err != nil {
		return Friend{}, false
	}
	for _, f := range GetFriendStore(user).List() {
		if f.URL == norm {
			return f, true
		}
	}
	return Friend{}, false
}

// friendFromHeader читает адрес инстанса-отправителя.
func friendFromHeader(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Briefly-Friend-From"))
}

// friendAuthorized проверяет ключ на входе в «дверь».
func friendAuthorized(c *gin.Context) bool {
	return AuthorizeFriendKey(friendKeyFrom(c))
}

// GET /api/friend/share — отдать свой payload другу.
//
// Здесь НЕ используется authLimiter (он защищает перебор паролей: 5 попыток
// в минуту с блокировкой до 30 минут). Для синка это неверно: несколько
// друзей за одним NAT делят IP и блокировали бы друг друга, а получаса
// блокировки для фонового обмена — слишком сурово. Защиту даёт сам ключ:
// 32 байта энтропии, перебор бессмыслен, а общий троттлинг /api (60 req/s) и
// капы на размер payload стоят в apiRateLimitMiddleware и friends.go.
func (h *Handler) FriendShare(c *gin.Context) {
	// Сессии у чужого инстанса нет, поэтому владельца определяем по ключу:
	// это тот пользователь, который выдал этот ключ в своём friend-коде.
	user, ok := friendByKey(friendKeyFrom(c))
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "friend_auth"})
		return
	}
	nickname, avatar := friendIdentity(user)
	// instance — адрес нашего инстанса глазами того, кто стучится. Сверять его
	// на приёме нельзя: в запросе Host — адрес ПОЛУЧАТЕЛЯ, а не отправителя,
	// поэтому адрес отправителя берут из своего списка друзей (FriendIngest).
	payload := buildFriendPayload(friendPublicURL(c), user, nickname, avatar, GetAccounts().Profile(user))
	c.JSON(http.StatusOK, payload)
}

// POST /api/friend/ingest — принять payload друга и влить в профиль.
// Троттлинг здесь — тот же аргумент, что и в FriendShare: его даёт ключ,
// общий лимит /api и капы на размер принимаемых данных.
func (h *Handler) FriendIngest(c *gin.Context) {
	user, ok := friendByKey(friendKeyFrom(c))
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "friend_auth"})
		return
	}
	// Ключ один на пользователя, поэтому «кто пришёл» определяем по адресу
	// отправителя: он обязан совпасть с записью в нашем списке друзей (туда он
	// попал из friend-кода при добавлении). Чужой адрес — 403.
	fr, ok := friendByURL(user, friendFromHeader(c))
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "friend_not_known"})
		return
	}
	// 8 МБ — с запасом над нашими капами (см. fetchFromFriend): payload
	// обычного пользователя весит сотни килобайт.
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "friend_bad_body"})
		return
	}
	var in friendPayload
	if json.Unmarshal(raw, &in) != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "friend_bad_body"})
		return
	}
	// fr.URL — пространство имён для чужих комментариев: логины с разных
	// инстансов не сливаются (см. friendCommentUsername).
	sender := fr.URL
	stats, err := applyFriendPayload(&in, sender, GetAccounts().Profile(user))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "friend_bad_body"})
		return
	}
	// Клиенту нужен сигнал перерисовать лайки/коллекции/комментарии.
	publishSSE(map[string]any{"type": "friends", "incoming": sender})
	c.JSON(http.StatusOK, gin.H{"ok": true, "merged": stats})
}
