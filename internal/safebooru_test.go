package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Спека Safebooru зафиксирована живыми запросами к API (2026-08):
// анонимный dapi, голый массив, медиа на своём домене, autocomplete.php.
func TestSafebooruSpec(t *testing.T) {
	c := NewSafebooruClient()
	if c.Name() != "safebooru" {
		t.Errorf("Name = %q, want safebooru", c.Name())
	}
	if c.DisplayName() != "Safebooru" {
		t.Errorf("DisplayName = %q, want Safebooru", c.DisplayName())
	}
	if c.MaxQueryLen() != 1900 {
		t.Errorf("MaxQueryLen = %d, want 1900", c.MaxQueryLen())
	}
	if !c.AllowsHost("safebooru.org") || !c.AllowsHost("cdn.safebooru.org") {
		t.Errorf("safebooru должен разрешать свой домен и поддомены")
	}
	if c.AllowsHost("rule34.xxx") || c.AllowsHost("notsafebooru.org") {
		t.Errorf("safebooru не должен разрешать чужие домены")
	}
}

// Safebooru молча игнорирует списки id (возвращая свежие посты!) —
// пакетный путь GetPostsByIDs для него обязан быть выключен.
func TestSafebooruNoIDBatches(t *testing.T) {
	if safebooruSite.batchIDs || safebooruSite.idListParam || safebooruSite.supportsMinID || safebooruSite.supportsSort {
		t.Errorf("batchIDs/idListParam/supportsMinID/supportsSort должны быть false")
	}
	if !rule34Site.batchIDs {
		t.Errorf("rule34: пакетный список id работает, batchIDs обязан быть true")
	}
	if !hypnohubSite.batchIDs {
		t.Errorf("hypnohub: список id=1,2,3 проверен живым запросом, batchIDs обязан быть true")
	}
}

// ── Gelbooru: список id НЕ поддержан ──────────────────────────────────────
// Проверено живыми запросами к dapi (2026-09):
//
//	id=1,2,3            → 3 поста, но id=[14980512, 14980511, 14980510] (свежие)
//	id=900000,900001,.. → те же свежие посты, прос��анные id не учтены
//	без id вовсе         → ровно то же самое
//	id=14856425 (один)  → возвращает именно пост 14856425
//
// То есть параметр id у Gelbooru понимает РОВНО ОДИН id, а список молча
// игнорирует, подставляя обычную выдачу. Раньше спека считала, что списки
// работают, и пакетный путь GetPostsByIDs слал id=1,2,3: при 84 непроверенных
// постах пользователь получал 84 чужих поста в ответе, все id уходили в
// unresolved, и автоперепроверка выглядела как «ничего не происходит».
func TestGelbooruNoIDBatches(t *testing.T) {
	if gelbooruSite.batchIDs {
		t.Errorf("gelbooruSite.batchIDs = true: список id Gelbooru не поддерживает, нужен путь одиночных запросов")
	}
	if !gelbooruSite.idListParam {
		t.Errorf("gelbooruSite.idListParam = false: одиночный id= работает и ходит через отдельный параметр")
	}
	if !rule34Site.batchIDs {
		t.Errorf("rule34Site.batchIDs = false: у rule34 пакетный список id работает")
	}
	if hypnohubSite.batchIDs == false {
		t.Errorf("hypnohubSite.batchIDs = false: список id=1,2,3 проверен живым запросом")
	}
}

// Сквозной сценарий: сервер-подделка повторяет поведение Gelbooru — список id
// игнорирует и отдаёт «свежие» посты, одиночный id отдаёт как есть. Без
// одиночных запросов GetPostsByIDs не вернёт ничего, и плитки останутся
// заглушками навсегда.
func TestGelbooruPostsByIDsUseSingleRequests(t *testing.T) {
	const fresh = 14980512 // «свежий» пост, который сервер подсовывает вместо списка
	var mu sync.Mutex
	seen := make([]string, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("id")
		mu.Lock()
		seen = append(seen, raw)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// preview_url намеренно пустой: иначе фоновый warmPreviewCache после
		// ответа полезет в сеть и уронит тест — здесь проверяется только
		// разбор ответа на нужные id.
		post := func(id int) string {
			return `{"id":` + strconv.Itoa(id) + `,"file_url":"","preview_url":"","tags":"a b","width":100,"height":100}`
		}
		if raw == "" || strings.Contains(raw, ",") {
			// Список (или его отсутствие): отдаём посты, которых не просили.
			w.Write([]byte("[" + post(fresh) + "]"))
			return
		}
		w.Write([]byte("[" + post(atoiOr(raw, fresh)) + "]"))
	}))
	defer srv.Close()

	cl := NewGelbooruClient()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "fake-gelbooru-key", UserID: "1"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	h := &Handler{providers: map[string]Provider{"gelbooru": cl}}
	// provider() смотрит активного провайдера в конфиге и только потом
	// откатывается на defaultProviderName, поэтому без этого тест ушёл бы в
	// nil и объявил все id unresolved — ровно тот симптом, который чиним.
	cfg := GetConfig()
	prev := cfg.GetProvider()
	cfg.SetProvider("gelbooru")
	t.Cleanup(func() { cfg.SetProvider(prev) })

	// id из диапазона, который не встречается в других тестах пакета: БД
	// общая на весь прогон (TestMain её не изолирует), и запись сюда попадает
	// через UpsertMetaMany — чужой тест с такими же id получил бы их «из
	// локальной БД» и не увидит источник.
	const idA, idB, idC = 7001101, 7001102, 7001103
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", fmt.Sprintf("/posts-by-ids?ids=%d,%d,%d", idA, idB, idC), nil)
	h.GetPostsByIDs(c)

	var resp struct {
		Posts []struct {
			ID int `json:"id"`
		} `json:"posts"`
		Unresolved []int `json:"unresolved"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[int]bool{}
	for _, p := range resp.Posts {
		got[p.ID] = true
	}
	for _, want := range []int{idA, idB, idC} {
		if !got[want] {
			t.Errorf("пост %d не вернулся: ответ posts=%v unresolved=%v",
				want, got, resp.Unresolved)
		}
	}
	if got[fresh] {
		t.Errorf("в ответ попал посторонний пост %d, которого не просили", fresh)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, raw := range seen {
		if strings.Contains(raw, ",") {
			t.Errorf("Gelbooru получил список id=%q: такой запрос он игнорирует", raw)
		}
	}
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

// Спека Hypnohub зафиксирована живыми запросами к API (2026-08):
// анонимный dapi, голый массив, медиа (включая mp4/webm) на своём домене,
// autocomplete.php; списки id через параметр id=1,2,3 работают,
// min_id игнорируется.
func TestHypnohubSpec(t *testing.T) {
	c := NewHypnohubClient()
	if c.Name() != "hypnohub" {
		t.Errorf("Name = %q, want hypnohub", c.Name())
	}
	if c.DisplayName() != "Hypnohub" {
		t.Errorf("DisplayName = %q, want Hypnohub", c.DisplayName())
	}
	if c.MaxQueryLen() != 1900 {
		t.Errorf("MaxQueryLen = %d, want 1900", c.MaxQueryLen())
	}
	if !c.AllowsHost("hypnohub.net") || !c.AllowsHost("cdn.hypnohub.net") {
		t.Errorf("hypnohub должен разрешать свой домен и поддомены")
	}
	if c.AllowsHost("rule34.xxx") || c.AllowsHost("safebooru.org") {
		t.Errorf("hypnohub не должен разрешать чужие домены")
	}
	if !hypnohubSite.batchIDs || !hypnohubSite.idListParam {
		t.Errorf("hypnohub: списки id через id=1,2,3 работают — batchIDs/idListParam должны быть true")
	}
	if hypnohubSite.supportsMinID || hypnohubSite.supportsSort {
		t.Errorf("hypnohub: min_id игнорируется, sort не отправляем — supportsMinID/supportsSort должны быть false")
	}
	if hypnohubSite.requireAuth {
		t.Errorf("hypnohub: dapi отвечает анонимно — requireAuth должен быть false")
	}
}

func TestProviderSingleIDPrefix(t *testing.T) {
	for _, p := range []Provider{NewRule34Client(), NewGelbooruClient(), NewSafebooruClient(), NewHypnohubClient()} {
		if got := providerSingleID(p); got != "id:" {
			t.Errorf("%s: providerSingleID = %q, want id:", p.Name(), got)
		}
	}
}

func TestKnownProvidersIncludeSafebooru(t *testing.T) {
	if !isKnownProvider("safebooru") {
		t.Errorf("safebooru должен быть в knownProviders")
	}
	if !isKnownProvider("hypnohub") {
		t.Errorf("hypnohub должен быть в knownProviders")
	}
	if got := (&Config{Provider: "safebooru"}).GetProvider(); got != "safebooru" {
		t.Errorf("GetProvider(safebooru) = %q, want safebooru", got)
	}
	if got := (&Config{Provider: "hypnohub"}).GetProvider(); got != "hypnohub" {
		t.Errorf("GetProvider(hypnohub) = %q, want hypnohub", got)
	}
}

// Safebooru отдаёт "score":null — должно молча превращаться в 0.
func TestRule34PostNullScore(t *testing.T) {
	var p Rule34Post
	if err := json.Unmarshal([]byte(`{"id":7078709,"score":null,"tags":"a b","rating":"general"}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.ID != 7078709 || p.Score != 0 {
		t.Errorf("id=%d score=%d, want id=7078709 score=0", p.ID, p.Score)
	}
}

// Заглушка из живого ответа safebooru (-rating:метатеги): id есть, но
// image/tags null и file_url мусорный. Реальные посты при этом (score:null
// и т.п.) должны проходить.
func TestParseDapiPostRejectsGhost(t *testing.T) {
	ghost := []byte(`{"preview_url":"https://safebooru.org/thumbnails//thumbnail_.jpg","sample_url":"https://safebooru.org/images//","file_url":"https://safebooru.org/images//","directory":null,"hash":null,"width":null,"height":null,"id":14029915,"image":null,"change":null,"owner":null,"parent_id":0,"rating":null,"sample":false,"sample_height":0,"sample_width":0,"score":null,"tags":"","source":null,"status":"active","has_notes":false,"comment_count":0}`)
	if _, err := parseDapiPost(ghost); err == nil {
		t.Errorf("ghost-запись должна отбраковываться")
	}
	normal := []byte(`{"preview_url":"https://safebooru.org/thumbnails/4172/t.jpg","file_url":"https://safebooru.org/images/4172/x.jpg","directory":4172,"hash":"abc","width":1254,"height":1254,"id":7078709,"image":"x.jpg","rating":"general","score":null,"tags":"1girl solo","status":"active"}`)
	p, err := parseDapiPost(normal)
	if err != nil {
		t.Fatalf("нормальный пост отбракован: %v", err)
	}
	if p.ID != 7078709 || p.Image != "x.jpg" {
		t.Errorf("нормальный пост распарсен неверно: %+v", p)
	}
	// Минимальные записи без image/tags/file_url терпимы (см.
	// TestDapiParsingTypeTolerance) — бракуем только мусорный URL.
	minimal := []byte(`{"id":42,"width":100,"height":50}`)
	if _, err := parseDapiPost(minimal); err != nil {
		t.Errorf("минимальная запись не должна браковаться: %v", err)
	}
}
