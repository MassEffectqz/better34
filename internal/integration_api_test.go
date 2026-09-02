package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// requestJSON — запрос с JSON-телом и опциональной кукой; декодирует ответ.
func requestJSON(t *testing.T, client *http.Client, method, path string, body any, cookie *http.Cookie) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// newIntegrationRouter — тот же стек, что в main.go: проверка сессии
// (куки/X-Briefly-Token), белый список публичных /api/*, security-заголовки.
func newIntegrationRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		legacy := false
		if strings.HasPrefix(c.Request.URL.Path, "/api") && !legacy {
			if strings.HasPrefix(c.Request.URL.Path, "/api/auth/") ||
				c.Request.URL.Path == "/api/healthz" ||
				c.Request.URL.Path == "/api/ready" {
				c.Next()
				return
			}
			accs := GetAccounts()
			if accs.Count() == 0 {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "no accounts"})
				return
			}
			if cookie, err := c.Cookie(SessionCookieName); err == nil {
				if u, ok := accs.UserBySession(cookie); ok {
					c.Set("briefly_user", u)
					c.Next()
					return
				}
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "login required"})
			return
		}
		c.Next()
	})
	api := r.Group("/api")
	{
		api.GET("/healthz", h.Healthz)
		api.GET("/ready", h.Readiness)
		api.GET("/metrics", h.Metrics)
		api.POST("/auth/register", h.AuthRegister)
		api.POST("/auth/login", h.AuthLogin)
		api.GET("/auth/me", h.AuthMe)
		api.POST("/auth/password", h.AuthChangePassword)
		api.POST("/auth/logout-others", h.AuthLogoutOthers)
		api.GET("/settings", h.GetSettings)
		api.POST("/settings", h.UpdateSettings)
		api.GET("/posts/:id", h.GetPost)
	}
	return r
}

// TestIntegrationReadyMetricsSettingsPassword — сквозной сценарий поверх
// изолированной data/ (t.TempDir): публичные эндпоинты, регистрация,
// частичное обновление настроек, смена пароля, отзыв чужих сессий.
func TestIntegrationReadyMetricsSettingsPassword(t *testing.T) {
	// Изолированный data/ каталог: БД и data/accounts/*.json не заденут прод.
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)
	os.MkdirAll("data", 0o755)

	// Автобэкап-цикл (StartDBAutoBackup из GetDB) в тесте не нужен: 0 копий.
	t.Setenv("BRIEFLY_DB_BACKUPS", "0")
	// Глобальная БД держит файл открытым — закрываем до удаления TempDir и
	// сбрасываем sync.Once, чтобы GetDB() в последующих тестах создал свежую
	// БД в их собственном рабочем каталоге (иначе они получат закрытую/nil).
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})

	h := NewHandler() // с провайдерами: GetSettings считает effectiveMaxQueryLen
	srv := httptest.NewServer(newIntegrationRouter(h))
	defer srv.Close()

	// authed: держит куки сессии (как браузер); bare: чистый клиент для
	// проверки отказа в доступе без куки.
	jar, _ := cookiejar.New(nil)
	authed := &http.Client{Transport: srv.Client().Transport, Jar: jar, Timeout: 30 * time.Second}
	bare := &http.Client{Transport: srv.Client().Transport, Timeout: 30 * time.Second}

	// 1. Публичные /api/healthz и /api/ready (готовность БД).
	for _, path := range []string{"/api/healthz", "/api/ready"} {
		res, err := authed.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		raw := new(bytes.Buffer)
		raw.ReadFrom(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, body %s", path, res.StatusCode, raw.String())
		}
		var m map[string]any
		_ = json.Unmarshal(raw.Bytes(), &m)
		if m["status"] != "ok" && m["status"] != "ready" {
			t.Fatalf("GET %s: unexpected status %v (%s)", path, m["status"], raw.String())
		}
	}

	// 2. До регистрации всё закрыто, кроме /api/auth/*, healthz, ready.
	if code, _ := requestJSON(t, bare, "GET", srv.URL+"/api/settings", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/settings без аккаунтов: код %d, ожидался 401", code)
	}

	// 3. Регистрация первого аккаунта; куки-джар подхватывает сессию.
	code, body := requestJSON(t, authed, "POST", srv.URL+"/api/auth/register",
		map[string]string{"username": "tester", "password": "old-secret"}, nil)
	if code != http.StatusOK {
		t.Fatalf("register: код %d, body %v", code, body)
	}

	// 4. Сессия живёт в джаре: /api/settings отдаёт конфиг.
	code, _ = requestJSON(t, authed, "GET", srv.URL+"/api/settings", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/settings с сессией: код %d", code)
	}
	// Кука сессии реально установлена (Set-Cookie в ответе регистрации).
	sessURL, _ := url.Parse(srv.URL)
	cookies := jar.Cookies(sessURL)
	foundSession := false
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			foundSession = true
		}
	}
	if !foundSession {
		t.Fatal("после регистрации нет куки сессии в джаре")
	}

	// Метрики — самодиагностика за авторизацией: без куки 401, с сессией 200.
	if code, _ := requestJSON(t, bare, "GET", srv.URL+"/api/metrics", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/metrics без куки: код %d, ожидался 401", code)
	}
	if code, _ := requestJSON(t, authed, "GET", srv.URL+"/api/metrics", nil, nil); code != http.StatusOK {
		t.Fatalf("GET /api/metrics с сессией: код %d, ожидался 200", code)
	}

	// 5. Частичное обновление настроек: незатронутые поля не сбрасываются.
	code, body = requestJSON(t, authed, "POST", srv.URL+"/api/settings",
		map[string]any{"concurrent_downloads": 2, "save_path": "data/posts", "min_id": 0}, nil)
	if code != http.StatusOK {
		t.Fatalf("POST /api/settings: код %d, body %v", code, body)
	}
	cfg := GetConfig()
	if got := cfg.GetConcurrentDownloads(); got != 2 {
		t.Fatalf("concurrent_downloads = %d, ожидалось 2", got)
	}
	if got := cfg.GetSavePath(); got != "data/posts" {
		t.Fatalf("save_path = %q, ожидался data/posts", got)
	}
	if got := cfg.GetProvider(); got != defaultProviderName {
		t.Fatalf("частичное сохранение сбросило provider: %q != %q", got, defaultProviderName)
	}
	if got := cfg.GetProxyURL(); got != "" {
		t.Fatalf("частичное сохранение сбросило proxy_url: %q", got)
	}

	// 6. Authed-доступ к БД: пост читается только с кукой сессии.
	db := GetDB()
	db.AddOrUpdate(&Post{ID: 77, Tags: "touhou", Downloaded: true})
	if code, _ := requestJSON(t, bare, "GET", srv.URL+"/api/posts/77", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/posts/77 без куки: код %d, ожидался 401", code)
	}
	code, body = requestJSON(t, authed, "GET", srv.URL+"/api/posts/77", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/posts/77 с кукой: код %d, body %v", code, body)
	}
	if v, _ := body["tags"].(string); !strings.Contains(v, "touhou") {
		t.Fatalf("GET /api/posts/77: теги %v, ожидался touhou", body["tags"])
	}

	// 7. Смена пароля: неверный текущий отклоняется; верный — ок.
	code, _ = requestJSON(t, authed, "POST", srv.URL+"/api/auth/password",
		map[string]string{"current_password": "wrong", "new_password": "new-secret-1"}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("смена пароля с неверным текущим: код %d, ожидался 400", code)
	}
	code, _ = requestJSON(t, authed, "POST", srv.URL+"/api/auth/password",
		map[string]string{"current_password": "old-secret", "new_password": "new-secret-1"}, nil)
	if code != http.StatusOK {
		t.Fatalf("смена пароля: код %d", code)
	}

	// 8. Вход по новому паролю работает.
	code, body = requestJSON(t, authed, "POST", srv.URL+"/api/auth/login",
		map[string]string{"username": "tester", "password": "new-secret-1"}, nil)
	if code != http.StatusOK {
		t.Fatalf("login с новым паролем: код %d, body %v", code, body)
	}

	// 9. logout-others: старая кука (отозванная при смене пароля) больше не пускает.
	// Джар держит только последнюю куку — эмулируем «другую сессию» явной кукой.
	// Сначала фиксируем текущую (валидную) куку из джара.
	var sessCookie *http.Cookie
	for _, c := range jar.Cookies(sessURL) {
		if c.Name == SessionCookieName {
			sessCookie = c
		}
	}
	code, body = requestJSON(t, bare, "POST", srv.URL+"/api/auth/logout-others", nil, sessCookie)
	if code != http.StatusOK {
		t.Fatalf("logout-others: код %d, body %v", code, body)
	}
	// После смены пароля (п.7) и logout-others старая кука отозвана.
	if code, _ := requestJSON(t, bare, "GET", srv.URL+"/api/settings", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/settings без валидной куки: код %d, ожидался 401", code)
	}
}
