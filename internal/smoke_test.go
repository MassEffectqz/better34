package internal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSingleflightDedup(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	g := &singleflightGroup{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			g.Do("k", func() ([]Rule34Post, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				time.Sleep(200 * time.Millisecond)
				return []Rule34Post{{ID: 1}}, nil
			})
		}()
	}
	close(start)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("expected 1 upstream call, got %d", calls)
	}
}

func TestCircuitBreakerCoolDown(t *testing.T) {
	b := &circuitBreaker{}
	for i := 0; i < breakerMaxFailures; i++ {
		b.Fail("rule34")
	}
	if b.Allow() {
		t.Fatal("breaker should be open")
	}
	b.openUntil = time.Now().Add(-time.Millisecond)
	if !b.Allow() {
		t.Fatal("breaker should allow after cooldown")
	}
	b.Success()
	if !b.Allow() {
		t.Fatal("breaker should stay closed after success")
	}
}

func TestSearchCacheDiskRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := NewRule34Client()
	c.cache = newBooruCache(filepath.Join(dir, "search_cache.json"))

	c.cache.mu.Lock()
	c.cache.m = map[string]searchCacheEntry{
		"a|1|20|0": {posts: []Rule34Post{{ID: 42, Tags: "test"}}, ts: time.Now()},
	}
	c.cache.mu.Unlock()

	c.cache.saveToDisk()
	if _, err := os.Stat(c.cache.file); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}

	c.cache.mu.Lock()
	c.cache.m = make(map[string]searchCacheEntry)
	c.cache.mu.Unlock()

	c.cache.loadFromDisk()
	posts, ok := c.cache.get("a|1|20|0")
	if !ok || len(posts) != 1 || posts[0].ID != 42 {
		t.Fatalf("round-trip failed: ok=%v posts=%+v", ok, posts)
	}
}

func TestSuggestionCache(t *testing.T) {
	c := NewRule34Client()
	c.suggestionCachePut("rul", []TagSuggestion{{Value: "rule34", Count: 5}})
	items, ok := c.suggestionCacheGet("rul")
	if !ok || len(items) != 1 || items[0].Value != "rule34" {
		t.Fatalf("suggestion cache failed")
	}
}

func kc(key string) APICredential {
	return APICredential{Name: "k_" + key, APIKey: key, UserID: "1"}
}

func TestKeyManagerLRU(t *testing.T) {
	m := &keyManager{}
	keys := []string{"aaa", "bbb", "ccc"}
	creds := []APICredential{kc("aaa"), kc("bbb"), kc("ccc")}
	m.sync(creds)
	counts := map[string]int{}
	for i := 0; i < 9; i++ {
		c, ok := m.pickCred()
		if !ok {
			t.Fatal("no key available")
		}
		counts[c.APIKey]++
	}
	for _, k := range keys {
		if counts[k] != 3 {
			t.Fatalf("expected equal LRU distribution, got %v", counts)
		}
	}
}

func TestKeyManagerQuarantine403(t *testing.T) {
	m := &keyManager{}
	m.sync([]APICredential{kc("aaa"), kc("bbb")})
	picked := map[string]int{}
	for i := 0; i < 6; i++ {
		c, ok := m.pickCred()
		if !ok {
			t.Fatal("no key available")
		}
		picked[c.APIKey]++
		// Ключ "aaa" всегда падает с невалидными кредами (401/403).
		if c.APIKey == "aaa" {
			m.report(c.APIKey, false, "auth")
		} else {
			m.report(c.APIKey, true, "")
		}
	}
	if picked["aaa"] > 1 {
		t.Fatalf("403 key should be quarantined after first failure, got picks=%v", picked)
	}
	if picked["bbb"] < 3 {
		t.Fatalf("healthy key should carry the load, got picks=%v", picked)
	}
	if m.healthyCount() != 1 {
		t.Fatalf("expected 1 healthy key, got %d", m.healthyCount())
	}
}

func TestKeyManagerQuarantineTransient(t *testing.T) {
	m := &keyManager{}
	m.sync([]APICredential{kc("aaa"), kc("bbb")})
	// 3 сетевых ошибки подряд на "aaa" — на карантин.
	for i := 0; i < 3; i++ {
		c, _ := m.pickCred()
		m.report(c.APIKey, false, "network")
	}
	// После карантина "aaa" больше не выбирается.
	for i := 0; i < 4; i++ {
		c, ok := m.pickCred()
		if !ok {
			t.Fatal("no key available")
		}
		if c.APIKey == "aaa" {
			t.Fatalf("quarantined key picked")
		}
		m.report(c.APIKey, true, "")
	}
}

func TestKeyManagerAutoJoin(t *testing.T) {
	m := &keyManager{}
	m.sync([]APICredential{kc("aaa"), kc("bbb")})
	c1, _ := m.pickCred()
	m.report(c1.APIKey, false, "auth")
	c2, _ := m.pickCred()
	m.report(c2.APIKey, false, "auth")
	if m.healthyCount() != 0 {
		t.Fatalf("expected all keys quarantined, got %d", m.healthyCount())
	}
	// В конфиг добавили третий ключ — система должна его сразу подхватить.
	m.sync([]APICredential{kc("aaa"), kc("bbb"), kc("ccc")})
	c3, ok := m.pickCred()
	if !ok {
		t.Fatal("new key should be picked")
	}
	if c3.APIKey != "ccc" {
		t.Fatalf("expected new key ccc to be picked, got %s", c3.APIKey)
	}
}

func TestKeyManagerRecovery(t *testing.T) {
	m := &keyManager{}
	m.sync([]APICredential{kc("aaa")})
	c, ok := m.pickCred()
	if !ok {
		t.Fatal("no key")
	}
	m.report(c.APIKey, false, "network")
	m.report(c.APIKey, false, "network")
	m.report(c.APIKey, false, "network")
	if m.healthyCount() != 0 {
		t.Fatal("key should be quarantined")
	}
	// Карантин истёк — ключ возвращается в строй.
	m.mu.Lock()
	m.keys[0].excludedUntil = time.Now().Add(-time.Second)
	m.mu.Unlock()
	if _, ok := m.pickCred(); !ok {
		t.Fatal("key should recover after quarantine expires")
	}
}

func TestConfigHotReload(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)
	os.MkdirAll("data", 0755)
	os.WriteFile("data/config.json", []byte(`{}`), 0644)

	cfg := &Config{
		ProxyURL:            "",
		DownloadPath:        "data/posts",
		ConcurrentDownloads: 5,
		ThumbSize:           300,
		AutoDownload:        false,
		SavePath:            "data/posts",
	}
	cfg.load()
	if len(cfg.APIKeys) != 0 {
		t.Fatalf("expected 0 keys after load, got %d", len(cfg.APIKeys))
	}
	// Файл меняется (добавили ключ) — maybeReload должен подхватить.
	time.Sleep(1100 * time.Millisecond) // mtime должен уйти в прошлое относительно loadedAt? нет — наоборот: mtime ВПЕРЁД. Sleep нужен, чтобы mtime новой записи > старой с точностью fs
	configCheckAt.Store(0)              // сбрасываем троттлинг, чтобы maybeReload сразу проверил файл
	os.WriteFile("data/config.json", []byte(`{"api_keys":[{"name":"x","api_key":"k1","user_id":"u1"},{"name":"y","api_key":"k2","user_id":"u2"}]}`), 0644)
	time.Sleep(1100 * time.Millisecond)
	cfg.maybeReload()
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("expected 2 keys after hot reload, got %d", len(cfg.APIKeys))
	}
}

func TestLiveSearchPosts(t *testing.T) {
	if os.Getenv("BRIEFLY_LIVE") == "" {
		t.Skip("set BRIEFLY_LIVE=1 to hit the rule34 API")
	}
	if _, err := os.Stat("../data/config.json"); err == nil {
		os.Chdir("..")
	}
	c := NewRule34Client()
	posts, err := c.SearchPosts("blonde", 1, 10, 0)
	if err != nil {
		t.Fatalf("SearchPosts failed: %v", err)
	}
	if len(posts) == 0 {
		t.Fatal("no posts returned")
	}
	if errors.Is(err, errAPI403) {
		t.Fatal("403")
	}
	t.Logf("live: got %d posts, first id=%d\n", len(posts), posts[0].ID)

	sugg, err := c.SuggestTags("rul")
	if err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	t.Logf("live: suggestions=%d first=%+v\n", len(sugg), sugg[0])
}

func TestLiveKeyRotation(t *testing.T) {
	if os.Getenv("BRIEFLY_LIVE") == "" {
		t.Skip("set BRIEFLY_LIVE=1 to hit the rule34 API")
	}
	if _, err := os.Stat("../data/config.json"); err == nil {
		os.Chdir("..")
	}
	c := NewRule34Client()
	queries := []string{"blonde", "blue eyes", "school uniform", "kneesocks", "outdoors"}
	for i, q := range queries {
		posts, err := c.SearchPosts(q, 1, 5, 0)
		if err != nil {
			t.Fatalf("SearchPosts(%q) failed: %v", q, err)
		}
		t.Logf("rotation %d: key ...%s got %d posts\n", i+1, fmt.Sprintf("%s", "?"), len(posts))
	}
}
