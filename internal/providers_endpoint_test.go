package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// providersRouter — роуты шапочного источника поверх той же сессионной защиты,
// что в webSecurityMiddleware (пакет main): /api/* без куки не отдаём.
func providersRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		accs := GetAccounts()
		if accs.Count() > 0 {
			if cookie, err := c.Cookie(SessionCookieName); err == nil {
				if u, ok := accs.UserBySession(cookie); ok {
					c.Set("briefly_user", u)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "auth_required"})
			return
		}
		c.Next()
	})
	api := r.Group("/api")
	{
		api.GET("/providers", h.ListProviders)
		api.POST("/providers", h.SetProvider)
		api.GET("/settings", h.GetSettings)
	}
	return r
}

// TestProvidersEndpointForNonAdmin — регрессия бейджа «Источник постов» в шапке.
//
// Раньше список источников жил только в /api/settings под requireAdmin, а
// loadSettings() у не-админа выходил раньше запроса: в шапке оставался голый
// «rule34» из HTML и пустое меню. /api/providers обязан отдавать список любому
// вошедшему (включая смену источника), а /api/settings — по-прежнему только
// администратору.
func TestProvidersEndpointForNonAdmin(t *testing.T) {
	oldWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	resetGlobalTestState(t)

	acc := GetAccounts()
	if _, err := acc.Register("boss", "secret-1"); err != nil {
		t.Fatalf("register boss: %v", err)
	}
	// CreatedAt пишется с точностью до секунды: без паузы второй юзер мог бы
	// унаследовать админство (IsAdmin — пользователь с минимальным CreatedAt).
	time.Sleep(1100 * time.Millisecond)
	if _, err := acc.Register("viewer", "secret-2"); err != nil {
		t.Fatalf("register viewer: %v", err)
	}
	if acc.IsAdmin("viewer") {
		t.Fatal("viewer оказался админом — тест перестанет проверять не-админа")
	}
	viewerToken, _, err := acc.CreateSession("viewer")
	if err != nil {
		t.Fatalf("session viewer: %v", err)
	}
	bossToken, _, err := acc.CreateSession("boss")
	if err != nil {
		t.Fatalf("session boss: %v", err)
	}

	h := NewHandler() // с провайдерами: GetProviders считает effectiveMaxQueryLen
	r := providersRouter(h)

	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 1. Без сессии — 401: список источников не публикуем анонимам.
	if w := call(http.MethodGet, "/api/providers", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/providers без куки: код %d, ожидался 401", w.Code)
	}

	// 2. Не-админ получает полный список с человеческими именами.
	w := call(http.MethodGet, "/api/providers", "", viewerToken)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/providers (не-админ): код %d, тело: %s", w.Code, w.Body.String())
	}
	var got struct {
		Providers []struct {
			Value string `json:"value"`
			Name  string `json:"name"`
		} `json:"providers"`
		Provider    string `json:"provider"`
		MaxQueryLen int    `json:"max_query_len"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("разбор /api/providers: %v (%s)", err, w.Body.String())
	}
	if len(got.Providers) == 0 {
		t.Fatal("в /api/providers пустой список источников")
	}
	if got.Provider != GetConfig().GetProvider() {
		t.Errorf("provider = %q, в конфиге %q", got.Provider, GetConfig().GetProvider())
	}
	if got.MaxQueryLen <= 0 {
		t.Errorf("max_query_len = %d, ожидалось > 0", got.MaxQueryLen)
	}
	foundRule34 := false
	for _, p := range got.Providers {
		if p.Value == "rule34" && p.Name == "rule34.xxx" {
			foundRule34 = true
		}
	}
	if !foundRule34 {
		t.Errorf("в списке нет rule34 с именем «rule34.xxx»: %+v", got.Providers)
	}

	// 3. /api/settings остаётся админским — иначе чужие ключи утекут.
	w = call(http.MethodGet, "/api/settings", "", viewerToken)
	if w.Code != http.StatusForbidden {
		t.Fatalf("GET /api/settings (не-админ): код %d, ожидалась 403, тело: %s", w.Code, w.Body.String())
	}
	// 4. Админу настройки отдаются, и список совпадает с /api/providers.
	w = call(http.MethodGet, "/api/settings", "", bossToken)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/settings (админ): код %d, тело: %s", w.Code, w.Body.String())
	}
	var adminSettings struct {
		Providers json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &adminSettings); err != nil {
		t.Fatalf("разбор /api/settings: %v", err)
	}
	var publicProviders struct {
		Providers json.RawMessage `json:"providers"`
	}
	w = call(http.MethodGet, "/api/providers", "", bossToken)
	if err := json.Unmarshal(w.Body.Bytes(), &publicProviders); err != nil {
		t.Fatalf("разбор /api/providers: %v", err)
	}
	if !bytes.Equal(adminSettings.Providers, publicProviders.Providers) {
		t.Errorf("список в настройках и в шапке разошёлся:\nsettings: %s\nproviders: %s",
			adminSettings.Providers, publicProviders.Providers)
	}

	// 5. Не-админ переключает источник.
	if len(got.Providers) < 2 {
		t.Fatal("для проверки смены источника нужно минимум два")
	}
	target := got.Providers[0].Value
	if target == got.Provider {
		target = got.Providers[1].Value
	}
	w = call(http.MethodPost, "/api/providers", `{"provider":"`+target+`"}`, viewerToken)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/providers (не-админ): код %d, тело: %s", w.Code, w.Body.String())
	}
	if cur := GetConfig().GetProvider(); cur != target {
		t.Errorf("источник не сменился: %q, ожидался %q", cur, target)
	}
	w = call(http.MethodGet, "/api/providers", "", viewerToken)
	var after struct {
		Provider string `json:"provider"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &after)
	if after.Provider != target {
		t.Errorf("GET /api/providers отдаёт %q после смены, ожидался %q", after.Provider, target)
	}

	// 6. Мусор не принимаем молча.
	w = call(http.MethodPost, "/api/providers", `{"provider":"no-such-site"}`, viewerToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /api/providers с неизвестным: код %d, ожидался 400", w.Code)
	}
	if cur := GetConfig().GetProvider(); cur != target {
		t.Errorf("неизвестный источник всё же записан: %q", cur)
	}
	// 7. Без сессии сменить нельзя.
	w = call(http.MethodPost, "/api/providers", `{"provider":"`+target+`"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("POST /api/providers без куки: код %d, ожидался 401", w.Code)
	}
}
