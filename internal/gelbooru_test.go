package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newTestGelbooru(srvURL string) *booruClient {
	c := NewGelbooruClient()
	c.spec.apiURL = srvURL + "/index.php"
	c.httpClient.Store(&http.Client{})
	// Межзапусковая изоляция: gb_sc_test.json переживает прогон в %TEMP%
	// (debounced saveToDisk дописывает его в конце пакета), и следующий
	// запуск получает SWR-хит вместо запроса к мок-серверу — тесты падают
	// с «got 0 calls». Удаляем перед загрузкой: каждый тест начинается
	// с пустого кэша и обязан ходить на сервер.
	cachePath := filepath.Join(os.TempDir(), "gb_sc_test.json")
	_ = os.Remove(cachePath)
	c.cache = newBooruCache(cachePath)
	c.suggMu.Lock()
	c.suggM = make(map[string]suggestionCacheEntry)
	c.suggMu.Unlock()
	c.breaker.failures = 0
	c.breaker.openUntil = time.Time{}
	// gelbooru требует валидный api_key — сеем тестовый.
	seedTestKeys(c, []APICredential{{Name: "test", APIKey: "testkey", UserID: "1"}})
	return c
}

// seedTestKeys сеет ключи и «поглощает» текущее поколение конфига:
// иначе syncFromConfig при очередном maybeReload (раз в 5с) затирает
// сеяные ключи ключами из реального data/config.json — под -count=2
// полный прогон пересекает границу троттлинга и тесты падают с
// «все на карантине». С заморозкой lastGen resync для клиента выключен.
func seedTestKeys(c *booruClient, creds []APICredential) {
	c.keys.sync(creds)
	c.keys.mu.Lock()
	c.keys.lastGen = configGen.Load()
	c.keys.mu.Unlock()
}

// Обёрнутый ответ {"@attributes":..,"post":[..]} должен разворачиваться.
func TestGelbooruWrappedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "dapi" {
			t.Errorf("expected page=dapi, got %q", r.URL.Query().Get("page"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"@attributes":{"limit":"2","offset":"0","count":"12345"},"post":[` +
			`{"id":900,"tags":"cat_girl solo","file_url":"https://img3.gelbooru.com/s/x.jpg","preview_url":"https://img3.gelbooru.com/t/x.jpg","width":800,"height":600,"score":10,"rating":"explicit","file_size":12345},` +
			`{"id":901,"tags":"1girl","file_url":"https://img3.gelbooru.com/s/y.png","preview_url":"https://img3.gelbooru.com/t/y.jpg","width":100,"height":100,"score":1,"rating":"general","file_size":42}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	posts, err := c.SearchPosts("cat_girl", 1, 2, 0)
	if err != nil {
		t.Fatalf("SearchPosts failed: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if posts[0].ID != 900 || posts[0].FileType != "jpg" {
		t.Errorf("bad first post: %+v", posts[0])
	}
}

// Пачка id должна уходить отдельным параметром id=1,2,3 (не tags).
func TestGelbooruIDListParam(t *testing.T) {
	var gotID, gotTags string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotID = q.Get("id")
		gotTags = q.Get("tags")
		w.Write([]byte(`{"@attributes":{},"post":[{"id":5,"tags":"a","file_url":"a.jpg","preview_url":"t.jpg"}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	if _, err := c.SearchPosts("id:1,2,3", 1, 3, 0); err != nil {
		t.Fatalf("SearchPosts failed: %v", err)
	}
	if gotID != "1,2,3" {
		t.Errorf("expected id=1,2,3 param, got %q (tags=%q)", gotID, gotTags)
	}
	if gotTags != "" {
		t.Errorf("tags should be empty for id batch, got %q", gotTags)
	}
}

// min_id и sort:id:desc — фичи rule34; для gelbooru не должны попадать в запрос.
func TestGelbooruNoUnsupportedParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("min_id") != "" {
			t.Errorf("min_id should not be sent to gelbooru")
		}
		if tags := q.Get("tags"); strings.Contains(tags, "sort:") {
			t.Errorf("sort meta should not be sent to gelbooru, tags=%q", tags)
		}
		w.Write([]byte(`{"@attributes":{},"post":[]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	if _, err := c.SearchPosts("", 1, 10, 777); err != nil {
		t.Fatalf("SearchPosts failed: %v", err)
	}
}

// Анонимный доступ закрыт: без ключей — явная ошибка (см. TestGelbooruRequiresAuth),
// а с ключом api_key/user_id обязаны уходить в запросе.
func TestGelbooruSendsCredentials(t *testing.T) {
	var gotKey, gotUID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("api_key")
		gotUID = r.URL.Query().Get("user_id")
		w.Write([]byte(`{"@attributes":{},"post":[]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	if _, err := c.SearchPosts("solo", 1, 5, 0); err != nil {
		t.Fatalf("SearchPosts failed: %v", err)
	}
	if gotKey == "" || gotUID == "" {
		t.Errorf("credentials must be sent, key=%q uid=%q", gotKey, gotUID)
	}
}

// Автодополнение через dapi tag index: требует ключ, префикс — name_pattern=cat%.
func TestGelbooruSuggestTagIndex(t *testing.T) {
	var gotPattern, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("s") != "tag" {
			t.Errorf("expected s=tag, got %q", q.Get("s"))
		}
		// SuggestTags делает два запроса: префиксный (name_pattern=cat%)
		// и, если базового тега нет, точный (name=cat). Пишем параметры
		// ПРЕФИКСНОГО запроса — иначе их перетрёт точный.
		if q.Get("name") == "" {
			gotPattern = q.Get("name_pattern")
		}
		gotKey = q.Get("api_key")
		w.Write([]byte(`{"@attributes":{},"tag":[{"name":"cat_ears","count":42000,"type":0},{"name":"cat_girl","count":99000,"type":0},{"name":"zero_count","count":0,"type":0}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	seedTestKeys(c, []APICredential{{Name: "gb", APIKey: "gbkey", UserID: "1"}})
	sugg, err := c.SuggestTags("cat")
	if err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if gotPattern != "cat%" {
		t.Errorf("expected name_pattern=cat%%, got %q", gotPattern)
	}
	if gotKey != "gbkey" {
		t.Errorf("tag index requires auth: expected api_key sent, got %q", gotKey)
	}
	if len(sugg) != 2 {
		t.Fatalf("expected 2 suggestions (count=0 отфильтрован), got %d", len(sugg))
	}
	if sugg[0].Value != "cat_girl" || sugg[0].Count != 99000 {
		t.Errorf("expected cat_girl первым (по count), got %+v", sugg[0])
	}
}

// Фильтрация ключей по провайдеру: пустой Provider подходит всем,
// указанный — только своему сайту.
// Живой формат gelbooru: directory — строка "42\/9b", у r34 это число;
// битый пост не должен убивать весь ответ.
func TestDapiParsingTypeTolerance(t *testing.T) {
	body := []byte(`{"@attributes":{"count":"2"},"post":[` +
		`{"id":14748034,"created_at":"Fri Aug 21 2026","score":0,"width":2894,"height":4093,` +
		`"md5":"abc","directory":"42\/9b","image":"x.jpg","rating":"general","change":1787352326,` +
		`"tags":"1girl cat_girl highres","file_url":"https://img3.gelbooru.com/images/xx.jpg",` +
		`"preview_url":"https://img3.gelbooru.com/thumbnails/xx.jpg","file_size":812345},` +
		`{"broken": true}` +
		`]}`)
	posts, err := parseDapiPosts(body)
	if err != nil {
		t.Fatalf("parseDapiPosts failed: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected 1 good post (битый пропущен), got %d", len(posts))
	}
	p := posts[0]
	if p.ID != 14748034 || p.Width != 2894 || p.FileSize != 812345 || p.Directory != 0 {
		t.Errorf("numeric fields wrong: %+v", p)
	}
	if !strings.Contains(p.Tags, "cat_girl") || p.FileType != "jpg" {
		t.Errorf("tags/filetype wrong: %+v", p)
	}

	// Голый массив (rule34) тоже продолжает парситься.
	arrBody := []byte(`[{"id":5,"tags":"a","file_url":"a.mp4","preview_url":"t.jpg"}]`)
	posts2, err := parseDapiPosts(arrBody)
	if err != nil || len(posts2) != 1 || posts2[0].FileType != "video" {
		t.Errorf("bare array parsing broken: %v %+v", err, posts2)
	}

	// Числа-строки тоже валидны.
	strNums := []byte(`[{"id":"42","width":"100","height":"50"}]`)
	posts3, err := parseDapiPosts(strNums)
	if err != nil || len(posts3) != 1 || posts3[0].ID != 42 || posts3[0].Width != 100 {
		t.Errorf("string numbers must parse: %v %+v", err, posts3)
	}
}

// Gelbooru dapi без ключа отдаёт пустоту (anon → count=0), поэтому
// requireAuth=true: без ключей — явная ошибка, а не тихая пустая лента.
func TestGelbooruRequiresAuth(t *testing.T) {
	c := newTestGelbooru("http://127.0.0.1:1") // никуда не дойдёт
	seedTestKeys(c, nil)                       // ключей нет
	c.cache = newBooruCache(filepath.Join(os.TempDir(), "gb_sc_auth.json"))
	if _, err := c.SearchPosts("solo", 1, 5, 0); err == nil || !strings.Contains(err.Error(), "ключ") {
		t.Fatalf("expected keys-required error, got %v", err)
	}
}

// Невалидный ключ (gelbooru отвечает 401) должен выкидываться из ротации,
// а запрос — успешно повторяться со следующим ключом.
func TestGelbooruAuthKeyRotationOn401(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("api_key") == "bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"@attributes":{},"post":[{"id":7,"tags":"ok","file_url":"a.jpg","preview_url":"t.jpg"}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	seedTestKeys(c, []APICredential{{Name: "b", APIKey: "bad"}, {Name: "g", APIKey: "good"}})
	posts, err := c.SearchPosts("x", 1, 5, 0)
	if err != nil {
		t.Fatalf("rotation on 401 failed: %v", err)
	}
	if len(posts) != 1 || posts[0].ID != 7 {
		t.Errorf("unexpected posts: %+v", posts)
	}
	if calls.Load() < 2 {
		t.Errorf("expected retry after 401, got %d calls", calls.Load())
	}
	if c.keys.healthyCount() != 1 {
		t.Errorf("bad key must be quarantined, healthy=%d", c.keys.healthyCount())
	}
}

// Hotlink-защита CDN gelbooru: прокси обязан ставить Referer по хосту
// апстрима, иначе 302 → hotlink.php.
func TestProxyRefererPerHost(t *testing.T) {
	h := NewHandler()
	cases := []struct {
		host string
		want string
	}{
		{"img4.gelbooru.com", "https://gelbooru.com/"},
		{"gelbooru.com", "https://gelbooru.com/"},
		{"api-cdn.rule34.xxx", "https://rule34.xxx/"},
		{"evil.example.com", "https://rule34.xxx/"},
	}
	for _, tc := range cases {
		if got := h.refererForHost(tc.host); got != tc.want {
			t.Errorf("refererForHost(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// Лимит длины запроса: у gelbooru он меньше r34 (нестабильность бэкенда
// на длинных тегах), хвост фильтруется на клиенте.
func TestGelbooruMaxQueryLen(t *testing.T) {
	gb := NewGelbooruClient()
	if got := gb.MaxQueryLen(); got != 1900 {
		t.Errorf("gelbooru MaxQueryLen = %d, want 1900", got)
	}
	r34 := NewRule34Client()
	if got := r34.MaxQueryLen(); got != 3800 {
		t.Errorf("rule34 MaxQueryLen = %d, want 3800", got)
	}
}

// Сквозной тест хендлера: параметр rating=sfw вырезает explicit даже
// если сайт вернул их (локальная фильтрация по полю Rating).
func TestSearchPostsRatingParam(t *testing.T) {
	var expectRating atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectRating.Load() && !strings.Contains(r.URL.Query().Get("tags"), "-rating:explicit") {
			t.Errorf("server must receive -rating metatag, tags=%q", r.URL.Query().Get("tags"))
		}
		w.Write([]byte(`[` +
			`{"id":1,"tags":"a","file_url":"a.jpg","preview_url":"t.jpg","rating":"explicit"},` +
			`{"id":2,"tags":"b","file_url":"b.jpg","preview_url":"t.jpg","rating":"general"},` +
			`{"id":3,"tags":"c","file_url":"c.jpg","preview_url":"t.jpg","rating":"EXPLICIT"}` +
			`]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "k"}})
	cl.cache = newBooruCache(filepath.Join(os.TempDir(), "r34_rf.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}
	h := &Handler{providers: map[string]Provider{"rule34": cl}}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/posts?tags=cat_girl&limit=10&rating=sfw", nil)
	expectRating.Store(true)
	h.SearchPosts(c)

	var resp struct {
		Posts []struct {
			ID     int    `json:"id"`
			Rating string `json:"rating"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response: %v (%s)", err, w.Body.String())
	}
	if len(resp.Posts) != 1 || resp.Posts[0].ID != 2 {
		t.Errorf("expected only id=2 (general), got %+v", resp.Posts)
	}

	// Без режима — все посты на месте.
	expectRating.Store(false)
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/api/posts?tags=cat_girl&limit=10", nil)
	h.SearchPosts(c2)
	resp.Posts = nil
	json.Unmarshal(w2.Body.Bytes(), &resp)
	if len(resp.Posts) != 3 {
		t.Errorf("no-filter mode must return all 3, got %d", len(resp.Posts))
	}
}

// Fuzzy-мусор апстрима (undefined_* для запроса bocchi) отфильтровывается:
// остаются только теги с нужным префиксом, без пустых и дублей.
func TestSuggestRelevanceGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[` +
			`{"label":"undefined_fantastic_object","value":"undefined_fantastic_object","count":837},` +
			`{"value":"bocchi_the_rock!","count":31339},` +
			`{"value":"bocchi_the_rock!","count":9},` +
			`{"value":"","count":5},` +
			`{"value":"Bocchi_the_Rock_Movie","count":12}` +
			`]`))
	}))
	defer srv.Close()

	c := NewRule34Client()
	c.spec.apiURL = srv.URL + "/index.php"
	c.httpClient.Store(&http.Client{})
	seedTestKeys(c, []APICredential{{Name: "t", APIKey: "k"}})
	c.suggMu.Lock()
	c.suggM = make(map[string]suggestionCacheEntry)
	c.suggMu.Unlock()
	c.breaker.failures = 0
	c.breaker.openUntil = time.Time{}

	sugg, err := c.SuggestTags("bocchi")
	if err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if len(sugg) != 2 {
		t.Fatalf("expected 2 suggestions, got %d: %+v", len(sugg), sugg)
	}
	for _, s := range sugg {
		if strings.HasPrefix(s.Value, "undefined") {
			t.Errorf("junk leaked through: %+v", s)
		}
	}
}

func TestRatingFilter(t *testing.T) {
	// Фильтр словаре-агностичный: rule34 с 2026 на danbooru-стиле
	// (general/sensitive/questionable/explicit, "safe" исчез), gelbooru и
	// safebooru — там же с самого начала. Исключаем оба словаря.
	terms, excl := ratingFilter("sfw")
	if len(terms) != 3 || !excl["explicit"] || !excl["questionable"] || !excl["sensitive"] {
		t.Errorf("sfw wrong: %v %v", terms, excl)
	}
	for _, term := range terms {
		if term != "-rating:explicit" && term != "-rating:questionable" && term != "-rating:sensitive" {
			t.Errorf("unexpected sfw metatag %q", term)
		}
	}
	terms, excl = ratingFilter("nsfw")
	if len(terms) != 2 || !excl["general"] || !excl["safe"] || excl["explicit"] {
		t.Errorf("nsfw wrong: %v %v", terms, excl)
	}
	foundGeneral, foundSafe := false, false
	for _, term := range terms {
		switch term {
		case "-rating:general":
			foundGeneral = true
		case "-rating:safe":
			foundSafe = true
		}
	}
	if !foundGeneral || !foundSafe {
		t.Errorf("nsfw must exclude both general and safe: %v", terms)
	}
	// без режима — ничего
	t2, e2 := ratingFilter("")
	if t2 != nil || e2 != nil {
		t.Errorf("empty mode must be no-op, got %v %v", t2, e2)
	}
}

func TestCredentialsForSite(t *testing.T) {
	creds := []APICredential{
		{Name: "r34", APIKey: "k1", Provider: "rule34"},
		{Name: "gb", APIKey: "k2", Provider: "Gelbooru"},
		{Name: "legacy", APIKey: "k3"},
	}
	r34 := credentialsForSite(creds, "rule34")
	if len(r34) != 2 || r34[0].APIKey != "k1" || r34[1].APIKey != "k3" {
		t.Errorf("rule34 keys wrong: %+v", r34)
	}
	gb := credentialsForSite(creds, "gelbooru")
	if len(gb) != 2 || gb[0].APIKey != "k2" || gb[1].APIKey != "k3" {
		t.Errorf("gelbooru keys wrong: %+v", gb)
	}
}

func TestProviderHostAllowlists(t *testing.T) {
	r34 := NewRule34Client()
	if !r34.AllowsHost("rule34.xxx") || !r34.AllowsHost("api-cdn.rule34.xxx") {
		t.Error("rule34 hosts rejected")
	}
	if r34.AllowsHost("gelbooru.com") || r34.AllowsHost("evil.example.com") {
		t.Error("rule34 allowlist too broad")
	}
	gb := NewGelbooruClient()
	if !gb.AllowsHost("gelbooru.com") || !gb.AllowsHost("img3.gelbooru.com") {
		t.Error("gelbooru hosts rejected")
	}
	if gb.AllowsHost("rule34.xxx") || gb.AllowsHost("notgelbooru.com") {
		t.Error("gelbooru allowlist too broad")
	}
}

// Config: неизвестное/пустое имя провайдера откатывается к дефолту,
// валидное сохраняется.
func TestConfigProviderNormalization(t *testing.T) {
	cfg := &Config{}
	cfg.SetProvider("Gelbooru ")
	if cfg.Provider != "gelbooru" {
		t.Errorf("expected normalized 'gelbooru', got %q", cfg.Provider)
	}
	cfg.SetProvider("danbooru")
	if cfg.Provider != "gelbooru" {
		t.Errorf("unknown provider must be ignored, got %q", cfg.Provider)
	}
	if got := (&Config{Provider: "bogus"}).GetProvider(); got != defaultProviderName {
		t.Errorf("bogus stored value must fall back to %q, got %q", defaultProviderName, got)
	}
	if got := (&Config{Provider: ""}).GetProvider(); got != defaultProviderName {
		t.Errorf("empty stored value must fall back to %q, got %q", defaultProviderName, got)
	}
}
