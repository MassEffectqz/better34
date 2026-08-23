package internal

import (
	"encoding/json"
	"testing"
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
	if !rule34Site.batchIDs || !gelbooruSite.batchIDs {
		t.Errorf("rule34/gelbooru должны поддерживать пакетный запрос id")
	}
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
