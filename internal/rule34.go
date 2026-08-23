package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// Rule34Post — универсальная модель поста боеру. Поля совпадают с
// Gelbooru-0.2 dapi-форматом; остальные провайдеры маппятся на неё.
type Rule34Post struct {
	ID         int    `json:"id"`
	Tags       string `json:"tags"`
	FileURL    string `json:"file_url"`
	SampleURL  string `json:"sample_url"`
	PreviewURL string `json:"preview_url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Score      int    `json:"score"`
	Rating     string `json:"rating"`
	Image      string `json:"image"`
	Directory  int    `json:"directory"`
	Hash       string `json:"hash"`
	Change     int64  `json:"change"`
	Owner      string `json:"owner"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	HasNotes   bool   `json:"has_notes"`
	CommentCnt int    `json:"comment_count"`
	FileSize   int    `json:"file_size"`
	FileType   string `json:"file_type"`
}

type TagSuggestion struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Provider — источник постов. Все методы должны быть безопасны для
// параллельного вызова.
type Provider interface {
	Name() string
	DisplayName() string
	MaxQueryLen() int
	SearchPosts(tags string, page, limit, minID int) ([]Rule34Post, error)
	SuggestTags(query string) ([]TagSuggestion, error)
	GetTagCount(tag string) (int, error)
	GetPopularTags(limit int) ([]TagSuggestion, error)
	RebuildClient()
	HTTPClient() *http.Client
	RefererURL() string
	AllowsHost(host string) bool
}

const defaultProviderName = "rule34"

var knownProviders = []struct {
	Name        string `json:"value"`
	DisplayName string `json:"name"`
}{
	{"rule34", "rule34.xxx"},
	{"gelbooru", "Gelbooru"},
	{"safebooru", "Safebooru"},
	{"hypnohub", "Hypnohub"},
}

func isKnownProvider(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range knownProviders {
		if p.Name == name {
			return true
		}
	}
	return false
}

// siteSpec описывает Gelbooru-0.2-совместимый сайт: один протокол dapi,
// разные домены и мелкие отличия в параметрах/форме ответа.
type siteSpec struct {
	name        string
	title       string
	apiURL      string   // база dapi (…/index.php)
	wwwURL      string   // сайт для Referer
	mediaHosts  []string // суффиксы хостов CDN для прокси-allowlist
	requireAuth bool     // true — без валидного api_key API не отдаёт контент

	wrappedResp   bool   // ответ обёрнут в {"@attributes":..,"post":[..]}
	supportsMinID bool   // параметр min_id
	supportsSort  bool   // метатег sort:id:desc в tags
	idListParam   bool   // пачка id через отдельный параметр id=1,2,3 (иначе tags=id:…)
	batchIDs      bool   // списковый запрос id:a,b,c реально возвращает запрошенные посты (safebooru игнорирует id-списки — см. GetPostsByIDs)
	suggestMode   string // "autocomplete" | "tagindex"
	cacheFile     string // персистентный кэш поиска этого сайта
	maxQueryLen   int    // безопасная длина tags для сервера (0 = 3800, как у r34)
}

var rule34Site = siteSpec{
	name:          "rule34",
	title:         "rule34.xxx",
	apiURL:        "https://api.rule34.xxx/index.php",
	wwwURL:        "https://rule34.xxx/",
	mediaHosts:    []string{"rule34.xxx"},
	requireAuth:   true,
	wrappedResp:   false,
	supportsMinID: true,
	supportsSort:  true,
	idListParam:   false,
	batchIDs:      true,
	suggestMode:   "autocomplete",
	cacheFile:     "data/cache/search_cache_rule34.json",
}

var gelbooruSite = siteSpec{
	// requireAuth=true: с 2025 dapi без валидного api_key отвечает
	// count=0 для всего контента (включая 18+), так что анонимный
	// режим бесполезен — требуем ключ и честно сообщаем об ошибке.
	name:          "gelbooru",
	title:         "Gelbooru",
	apiURL:        "https://gelbooru.com/index.php",
	wwwURL:        "https://gelbooru.com/",
	mediaHosts:    []string{"gelbooru.com"},
	requireAuth:   true,
	wrappedResp:   true,
	supportsMinID: false,
	supportsSort:  false,
	idListParam:   true,
	batchIDs:      true,
	suggestMode:   "tagindex",
	cacheFile:     "data/cache/search_cache_gelbooru.json",
	maxQueryLen:   1900,
}

// safebooruSite — SFW-клон Gelbooru 0.2 (safebooru.org). Проверено живыми
// запросами 2026-08: dapi отвечает анонимно (requireAuth=false), JSON —
// голый массив; медиа на том же домене; autocomplete.php отдаёт счётчики.
// ВАЖНО: списки id НЕ поддерживаются ни через id=1,2,3, ни через tags=id:a,b
// (параметр молча игнорируется и возвращаются свежие посты!), min_id и
// sort:id:desc тоже игнорируются. Одиночный tags=id:N работает.
var safebooruSite = siteSpec{
	name:          "safebooru",
	title:         "Safebooru",
	apiURL:        "https://safebooru.org/index.php",
	wwwURL:        "https://safebooru.org/",
	mediaHosts:    []string{"safebooru.org"},
	requireAuth:   false,
	wrappedResp:   false,
	supportsMinID: false,
	supportsSort:  false,
	idListParam:   false,
	batchIDs:      false,
	suggestMode:   "autocomplete",
	cacheFile:     "data/cache/search_cache_safebooru.json",
	// Тот же софт, что у gelbooru — консервативный лимит его кластера.
	maxQueryLen: 1900,
}

// hypnohubSite — Gelbooru 0.2 (hypnohub.net). Проверено живыми запросами
// 2026-08: dapi отвечает анонимно (requireAuth=false), ответ — голый
// массив; медиа (включая mp4/webm до 4K со звуком) на том же домене;
// autocomplete.php отдаёт счётчики в label "tag (N)".
// Списки id РАБОТАЮТ отдельным параметром id=1,2,3 — возвращаются именно
// запрошенные посты (batchIDs=true). min_id молча игнорируется (как у
// safebooru); sort:id:desc не отправляем — выдача и так отсортирована
// по id по убыванию.
var hypnohubSite = siteSpec{
	name:          "hypnohub",
	title:         "Hypnohub",
	apiURL:        "https://hypnohub.net/index.php",
	wwwURL:        "https://hypnohub.net/",
	mediaHosts:    []string{"hypnohub.net"},
	requireAuth:   false,
	wrappedResp:   false,
	supportsMinID: false,
	supportsSort:  false,
	idListParam:   true,
	batchIDs:      true,
	suggestMode:   "autocomplete",
	cacheFile:     "data/cache/search_cache_hypnohub.json",
	// Тот же софт, что у gelbooru — консервативный лимит его кластера.
	maxQueryLen: 1900,
}

type searchCacheEntry struct {
	posts []Rule34Post
	ts    time.Time
}

type persistedSearchEntry struct {
	Posts []Rule34Post `json:"posts"`
	TS    time.Time    `json:"ts"`
}

const searchCacheTTL = 10 * time.Minute
const searchCacheMax = 512

// booruCache — LRU-кэш поиска с отложенной записью на диск.
// У каждого провайдера свой экземпляр и свой файл.
type booruCache struct {
	mu      sync.Mutex
	m       map[string]searchCacheEntry
	file    string
	saveMu  sync.Mutex
	pending atomic.Bool
}

func newBooruCache(file string, legacyFiles ...string) *booruCache {
	c := &booruCache{m: make(map[string]searchCacheEntry), file: file}
	c.loadFromDisk(legacyFiles...)
	return c
}

func (c *booruCache) get(key string) ([]Rule34Post, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.ts) > searchCacheTTL {
		return nil, false
	}
	out := make([]Rule34Post, len(e.posts))
	copy(out, e.posts)
	return out, true
}

func (c *booruCache) put(key string, posts []Rule34Post) {
	c.mu.Lock()
	if _, ok := c.m[key]; !ok && len(c.m) >= searchCacheMax {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.m {
			if oldest.IsZero() || e.ts.Before(oldest) {
				oldestKey, oldest = k, e.ts
			}
		}
		delete(c.m, oldestKey)
	}
	cp := make([]Rule34Post, len(posts))
	copy(cp, posts)
	c.m[key] = searchCacheEntry{posts: cp, ts: time.Now()}
	c.mu.Unlock()
	c.scheduleSave()
}

func (c *booruCache) loadFromDisk(legacyFiles ...string) {
	files := append([]string{c.file}, legacyFiles...)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var entries map[string]persistedSearchEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			continue
		}
		now := time.Now()
		loaded := 0
		c.mu.Lock()
		for k, e := range entries {
			if now.Sub(e.TS) > searchCacheTTL {
				continue
			}
			if len(c.m) >= searchCacheMax {
				break
			}
			c.m[k] = searchCacheEntry{posts: e.Posts, ts: e.TS}
			loaded++
		}
		c.mu.Unlock()
		if loaded > 0 {
			log.Printf("[search-cache:%s] loaded %d entries from %s", c.file, loaded, f)
		}
		return
	}
}

func (c *booruCache) saveToDisk() {
	now := time.Now()
	c.mu.Lock()
	entries := make(map[string]persistedSearchEntry, len(c.m))
	for k, e := range c.m {
		if now.Sub(e.ts) > searchCacheTTL {
			continue
		}
		entries[k] = persistedSearchEntry{Posts: e.posts, TS: e.ts}
	}
	c.mu.Unlock()
	if len(entries) == 0 {
		return
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(c.file), 0755)
	if err := os.WriteFile(c.file, data, 0644); err != nil {
		log.Printf("[search-cache] failed to persist %s: %v", c.file, err)
		return
	}
	log.Printf("[search-cache] persisted %d entries to %s", len(entries), c.file)
}

func (c *booruCache) scheduleSave() {
	c.saveMu.Lock()
	if c.pending.Swap(true) {
		c.saveMu.Unlock()
		return
	}
	c.saveMu.Unlock()
	go func() {
		time.Sleep(15 * time.Second)
		c.pending.Store(false)
		c.saveToDisk()
	}()
}

// singleflightGroup объединяет параллельные одинаковые запросы в один.
type singleflightGroup struct {
	mu sync.Mutex
	m  map[string]*sfCall
}

type sfCall struct {
	done  chan struct{}
	posts []Rule34Post
	err   error
}

func (g *singleflightGroup) Do(key string, fn func() ([]Rule34Post, error)) ([]Rule34Post, error) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.posts, c.err
	}
	c := &sfCall{done: make(chan struct{})}
	if g.m == nil {
		g.m = make(map[string]*sfCall)
	}
	g.m[key] = c
	g.mu.Unlock()
	c.posts, c.err = fn()
	g.mu.Lock()
	delete(g.m, key)
	close(c.done)
	g.mu.Unlock()
	return c.posts, c.err
}

// circuitBreaker останавливает запросы к API после серии ошибок.
type circuitBreaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const breakerMaxFailures = 5
const breakerCoolDown = 30 * time.Second

func (b *circuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().After(b.openUntil)
}

func (b *circuitBreaker) Fail(site string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.openUntil) {
		return
	}
	b.failures++
	if b.failures >= breakerMaxFailures {
		b.openUntil = time.Now().Add(breakerCoolDown)
		b.failures = 0
		log.Printf("[breaker] %s API открыт на %dс — слишком много ошибок подряд", site, int(breakerCoolDown.Seconds()))
	}
}

func (b *circuitBreaker) Success() {
	b.mu.Lock()
	b.failures = 0
	b.mu.Unlock()
}

func keyTail(key string) string {
	if len(key) > 6 {
		return key[len(key)-6:]
	}
	return key
}

// keyManager адаптивно распределяет нагрузку между API-ключами:
// следит за здоровьем каждого, сажает сбоящие ключи на карантин,
// автоматически подхватывает новые ключи из конфига и отдаёт
// больным ключам меньше запросов (EWMA ошибок + LRU по использованию).
const (
	keyExclude403       = 30 * time.Minute
	keyExcludeTransient = 10 * time.Minute
	keyMaxConsecFails   = 3
	keyEWMADecay        = 0.9
	keyEWMAFailShare    = 0.3
)

type keyState struct {
	cred          APICredential
	fails         int
	ewma          float64
	lastSeq       uint64
	excludedUntil time.Time
	excludedWhy   string
}

type keyManager struct {
	mu      sync.Mutex
	keys    []keyState
	lastGen uint64
	seq     uint64
}

func credentialsForSite(creds []APICredential, site string) []APICredential {
	var out []APICredential
	for _, c := range creds {
		p := strings.ToLower(strings.TrimSpace(c.Provider))
		if p == "" || p == site {
			out = append(out, c)
		}
	}
	return out
}

// syncFromConfig подхватывает изменения конфига (новые/удалённые ключи)
// без рестарта: пересчитывает список только если конфиг реально
// перечитался (изменилось поколение). У каждого провайдера свой
// экземпляр keyManager, поэтому сайт здесь не отслеживается.
// Ключи фильтруются по сайту: cred.Provider пустой → подходят всем,
// иначе ключ отдаётся только своему провайдеру.
func (m *keyManager) syncFromConfig(site string) {
	gen := configGen.Load()
	// lastGen проверяется и мутируется под тем же локом, что и keys:
	// параллельные вызовы из fan-out запросов иначе дают DATA RACE.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastGen == gen {
		return
	}
	m.lastGen = gen
	m.syncLocked(credentialsForSite(GetConfig().GetAPICredentials(), site))
}

func (m *keyManager) sync(creds []APICredential) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncLocked(creds)
}

// syncLocked вызывает под уже взятым m.mu.
func (m *keyManager) syncLocked(creds []APICredential) {
	byKey := make(map[string]*keyState, len(m.keys))
	for i := range m.keys {
		byKey[m.keys[i].cred.APIKey] = &m.keys[i]
	}
	newList := make([]keyState, 0, len(creds))
	for _, cred := range creds {
		if cred.APIKey == "" {
			continue
		}
		if st, ok := byKey[cred.APIKey]; ok {
			newList = append(newList, *st)
			continue
		}
		newList = append(newList, keyState{cred: cred})
		log.Printf("[key-manager] новый ключ ...%s подключён к ротации (всего %d)", keyTail(cred.APIKey), len(newList))
	}
	m.keys = newList
}

// pickCred выбирает лучший доступный ключ: здоровые получают нагрузку
// по очереди (LRU), сбоящие — реже или вообще не получают.
func (m *keyManager) pickCred() (APICredential, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var best *keyState
	for i := range m.keys {
		k := &m.keys[i]
		if now.Before(k.excludedUntil) {
			continue
		}
		if best == nil ||
			k.ewma < best.ewma ||
			(k.ewma == best.ewma && k.lastSeq < best.lastSeq) {
			best = k
		}
	}
	if best == nil {
		return APICredential{}, false
	}
	m.seq++
	best.lastSeq = m.seq
	return best.cred, true
}

func (m *keyManager) report(apiKey string, ok bool, why string) {
	if apiKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.keys {
		k := &m.keys[i]
		if k.cred.APIKey != apiKey {
			continue
		}
		if ok {
			k.fails = 0
			k.ewma *= keyEWMADecay
			k.excludedUntil = time.Time{}
			k.excludedWhy = ""
			return
		}
		k.fails++
		k.ewma = k.ewma*(1-keyEWMAFailShare) + keyEWMAFailShare
		switch {
		case why == "auth":
			k.excludedUntil = time.Now().Add(keyExclude403)
			k.excludedWhy = "ключ невалиден для этого сайта (401/403)"
		case k.fails >= keyMaxConsecFails:
			k.excludedUntil = time.Now().Add(keyExcludeTransient)
			k.excludedWhy = fmt.Sprintf("%d ошибок подряд", k.fails)
		}
		if !k.excludedUntil.IsZero() {
			log.Printf("[key-manager] ключ ...%s уходит на карантин на %s: %s",
				keyTail(apiKey), time.Until(k.excludedUntil).Round(time.Minute), k.excludedWhy)
		}
		return
	}
}

func (m *keyManager) healthyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for i := range m.keys {
		if now.After(m.keys[i].excludedUntil) {
			n++
		}
	}
	return n
}

type suggestionCacheEntry struct {
	items []TagSuggestion
	ts    time.Time
}

const suggestionCacheTTL = 30 * time.Minute
const suggestionCacheMax = 512

func jitteredBackoff(attempt int) time.Duration {
	base := 300 * time.Millisecond * time.Duration(1<<(attempt-1))
	if base > 3*time.Second {
		base = 3 * time.Second
	}
	return base + time.Duration(rand.Intn(150))*time.Millisecond
}

var errAPI403 = errors.New("API returned 403")

// errAPIAuth — обёртка над 401/403: ключ невалиден для этого сайта.
var errAPIAuth = errors.New("API key rejected")

// booruClient — общий клиент для всех Gelbooru-0.2-совместимых сайтов.
// Вся инфраструктура (кэши, брейкер, ротация ключей, singleflight)
// изолирована по экземпляру, т.е. по сайту.
type booruClient struct {
	spec       siteSpec
	httpClient atomic.Value
	breaker    circuitBreaker
	keys       keyManager
	sf         singleflightGroup
	cache      *booruCache

	suggMu sync.Mutex
	suggM  map[string]suggestionCacheEntry
}

func newBooruClient(spec siteSpec) *booruClient {
	c := &booruClient{
		spec:  spec,
		suggM: make(map[string]suggestionCacheEntry),
	}
	var legacy []string
	if spec.name == defaultProviderName {
		legacy = []string{"data/cache/search_cache.json"}
	}
	c.cache = newBooruCache(spec.cacheFile, legacy...)
	c.httpClient.Store(buildAPIHTTPClient())
	return c
}

func NewRule34Client() *booruClient   { return newBooruClient(rule34Site) }
func NewGelbooruClient() *booruClient { return newBooruClient(gelbooruSite) }
func NewSafebooruClient() *booruClient {
	return newBooruClient(safebooruSite)
}
func NewHypnohubClient() *booruClient { return newBooruClient(hypnohubSite) }

// providerSingleID возвращает префикс одиночного запроса поста по id —
// все dapi-совместимые сайты понимают tags=id:N. Пустая строка: провайдер
// не поддерживает поиск по id (для GetPostsByIDs).
func providerSingleID(p Provider) string {
	if _, ok := p.(*booruClient); ok {
		return "id:"
	}
	return ""
}

func buildAPIHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: buildTransport(),
	}
}

func buildTransport() *http.Transport {
	transport := NewResolveTransport("https://cloudflare-dns.com/dns-query")
	raw := GetConfig().GetProxyURL()
	if raw == "" {
		return transport
	}
	addr := raw
	if !strings.Contains(addr, "://") {
		addr = "socks5://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "socks5" && u.Scheme != "socks5h") {
		return transport
	}
	dialer, err := proxy.SOCKS5("tcp", u.Host, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		return transport
	}

	origDial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.Dial(network, addr)
		if err == nil {
			return conn, nil
		}
		return origDial(ctx, network, addr)
	}
	return transport
}

func (c *booruClient) Name() string        { return c.spec.name }
func (c *booruClient) DisplayName() string { return c.spec.title }
func (c *booruClient) RefererURL() string  { return c.spec.wwwURL }

// MaxQueryLen — безопасная длина tags-запроса для сайта. Поисковый кластер
// gelbooru нестабилен на запросах ~3.8К символов с сотнями минус-тегов
// (периодически отдаёт пустой результат): хвост фильтруется локально.
func (c *booruClient) MaxQueryLen() int {
	if c.spec.maxQueryLen > 0 {
		return c.spec.maxQueryLen
	}
	return 3800
}

func (c *booruClient) AllowsHost(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range c.spec.mediaHosts {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (c *booruClient) RebuildClient() {
	c.httpClient.Store(buildAPIHTTPClient())
}

func (c *booruClient) HTTPClient() *http.Client {
	return c.httpClient.Load().(*http.Client)
}

func (c *booruClient) SearchPosts(tags string, page, limit, minID int) ([]Rule34Post, error) {
	pid := page - 1

	if c.spec.supportsSort && !strings.Contains(tags, "sort:") {
		tags = strings.TrimSpace(tags)
		if tags != "" {
			tags += " "
		}
		tags += "sort:id:desc"
	}

	ckey := fmt.Sprintf("%s|%d|%d|%d", tags, page, limit, minID)
	if posts, ok := c.cache.get(ckey); ok {
		log.Printf("[search-cache] hit %s tags=%q page=%d", c.spec.name, tags, page)
		return posts, nil
	}

	if !c.breaker.Allow() {
		log.Printf("[breaker] %s API закрыт, запрос %q отменён без обращения к сети", c.spec.name, tags)
		return nil, fmt.Errorf("%s API временно недоступен — слишком много ошибок подряд, пауза %dс", c.spec.name, int(breakerCoolDown.Seconds()))
	}

	return c.sf.Do(ckey, func() ([]Rule34Post, error) {
		if posts, ok := c.cache.get(ckey); ok {
			return posts, nil
		}
		posts, err := c.fetchPosts(ckey, tags, pid, limit, minID)
		if err != nil {
			if !errors.Is(err, errAPIAuth) && !errors.Is(err, errAPI403) {
				c.breaker.Fail(c.spec.name)
			}
		} else {
			c.breaker.Success()
		}
		return posts, err
	})
}

func (c *booruClient) fetchPosts(ckey, tags string, pid, limit, minID int) ([]Rule34Post, error) {
	baseV := url.Values{}
	baseV.Set("page", "dapi")
	baseV.Set("s", "post")
	baseV.Set("q", "index")
	baseV.Set("json", "1")

	restTags := tags
	if c.spec.idListParam && strings.HasPrefix(tags, "id:") {
		// Пачка id: у gelbooru это отдельный параметр id=1,2,3.
		baseV.Set("id", strings.TrimPrefix(tags, "id:"))
		restTags = ""
	} else if restTags != "" {
		baseV.Set("tags", strings.ReplaceAll(restTags, "+", ""))
	}
	if c.spec.supportsMinID && minID > 0 {
		baseV.Set("min_id", fmt.Sprintf("%d", minID))
	}
	baseV.Set("limit", fmt.Sprintf("%d", limit))
	baseV.Set("pid", fmt.Sprintf("%d", pid))

	attempts := 3
	if n := len(GetConfig().GetAPICredentials()); n > attempts {
		attempts = n
	}
	c.keys.syncFromConfig(c.spec.name)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if !c.breaker.Allow() {
			return nil, fmt.Errorf("%s API недоступен (брейкер открыт)", c.spec.name)
		}

		cred, ok := c.keys.pickCred()
		if !ok {
			if c.spec.requireAuth {
				return nil, fmt.Errorf("нет доступных API ключей — все на карантине")
			}
			// Анонимный доступ разрешён — работаем без ключа.
			cred = APICredential{}
		}
		key := cred.APIKey
		userID := cred.UserID

		v := url.Values{}
		for k, vals := range baseV {
			for _, val := range vals {
				v.Set(k, val)
			}
		}
		if key != "" {
			v.Set("api_key", key)
		}
		if userID != "" {
			v.Set("user_id", userID)
		}
		reqURL := fmt.Sprintf("%s?%s", c.spec.apiURL, v.Encode())
		log.Printf("[api-key-check] %s request tags=%q uses key ...%s user_id=%q (attempt %d/%d)",
			c.spec.name, restTags, keyTail(key), userID, attempt, attempts)

		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Briefly/1.0")
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTPClient().Do(req)
		if err != nil {
			c.keys.report(key, false, "network")
			lastErr = fmt.Errorf("API request failed: %w", err)
			if attempt < attempts {
				log.Printf("API retry %d/%d for %q: %v", attempt, attempts, restTags, err)
				time.Sleep(jitteredBackoff(attempt))
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			c.keys.report(key, false, "read")
			lastErr = fmt.Errorf("failed to read API response: %w", err)
			if attempt < attempts {
				log.Printf("API retry %d/%d for %q: %v", attempt, attempts, restTags, err)
				time.Sleep(jitteredBackoff(attempt))
			}
			continue
		}

		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			// Ключ невалиден/запрещён (gelbooru отдаёт 401 на чужой ключ,
			// rule34 — 403) — сажаем его на карантин и пробуем следующий.
			c.keys.report(key, false, "auth")
			lastErr = fmt.Errorf("%w: %s", errAPIAuth, string(body))
			if attempt < attempts {
				log.Printf("API key ...%s rejected (%d), switching key (attempt %d/%d)",
					keyTail(key), resp.StatusCode, attempt+1, attempts)
				time.Sleep(150 * time.Millisecond)
			}
			continue
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			c.keys.report(key, false, "transient")
			lastErr = fmt.Errorf("API returned status %d (body: %s)", resp.StatusCode, string(body))
			if attempt < attempts {
				delay := jitteredBackoff(attempt)
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
						delay = time.Duration(min(secs, 10)) * time.Second
					}
				}
				log.Printf("API retry %d/%d for %q: status %d (sleep %s)", attempt, attempts, restTags, resp.StatusCode, delay)
				time.Sleep(delay)
			}
			continue
		case resp.StatusCode != 200:
			return nil, fmt.Errorf("API returned status %d (body: %s)", resp.StatusCode, string(body))
		}

		c.keys.report(key, true, "")

		// Пустой ответ НЕ кэшируем: у gelbooru пустота бывает и при
		// сбое поискового узла на запросах, где результаты есть.
		// Легитимные «ничего не найдено» просто повторят запрос.

		posts, perr := parseDapiPosts(body)
		if perr != nil {
			bodyStr := strings.TrimSpace(string(body))
			if len(bodyStr) > 200 {
				bodyStr = bodyStr[:200]
			}
			if strings.Contains(bodyStr, "Missing authentication") || strings.Contains(bodyStr, "error") {
				return nil, fmt.Errorf("API error: %s", bodyStr)
			}
			return nil, fmt.Errorf("failed to parse API response: %w (body: %s)", perr, bodyStr)
		}

		if len(posts) > 0 {
			c.cache.put(ckey, posts)
		}
		return posts, nil
	}
	return nil, lastErr
}

// parseDapiPosts понимает обе формы ответа dapi: голый массив постов
// (rule34) и объект-обёртку {"@attributes":…,"post":[…]} (gelbooru).
// Разбор идёт через промежуточную структуру с гибкими числами и по одному
// посту: расхождение типов в одном поле (у gelbooru directory — строка)
// или битый пост не должны убивать весь ответ.
func parseDapiPosts(body []byte) ([]Rule34Post, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return []Rule34Post{}, nil
	}

	var raws []json.RawMessage
	if trimmed[0] == '{' {
		var wrap struct {
			Post []json.RawMessage `json:"post"`
		}
		if err := json.Unmarshal(trimmed, &wrap); err != nil {
			return nil, err
		}
		raws = wrap.Post
	} else {
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, err
		}
	}

	out := make([]Rule34Post, 0, len(raws))
	for _, raw := range raws {
		p, err := parseDapiPost(raw)
		if err != nil {
			log.Printf("[dapi] пропущен битый пост: %v", err)
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func parseDapiPost(raw []byte) (Rule34Post, error) {
	var w struct {
		ID         flexInt `json:"id"`
		Tags       string  `json:"tags"`
		FileURL    string  `json:"file_url"`
		SampleURL  string  `json:"sample_url"`
		PreviewURL string  `json:"preview_url"`
		Width      flexInt `json:"width"`
		Height     flexInt `json:"height"`
		Score      flexInt `json:"score"`
		Rating     string  `json:"rating"`
		Image      string  `json:"image"`
		Hash       string  `json:"hash"`
		Owner      string  `json:"owner"`
		Source     string  `json:"source"`
		Status     string  `json:"status"`
		FileSize   flexInt `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Rule34Post{}, err
	}
	if w.ID <= 0 {
		return Rule34Post{}, fmt.Errorf("post without valid id")
	}
	// Ghost-записи (safebooru при -rating:-метатегах): id есть, но
	// directory/image null и file_url собран мусорным — "…/images//".
	// Минимальные записи без image/tags проект сознательно терпит
	// (см. TestDapiParsingTypeTolerance), поэтому бракуем только URL,
	// заканчивающийся слэшем: у настоящего файла после него всегда имя.
	if strings.HasSuffix(w.FileURL, "/") {
		return Rule34Post{}, fmt.Errorf("post %d with malformed file_url", int(w.ID))
	}
	return Rule34Post{
		ID:         int(w.ID),
		Tags:       w.Tags,
		FileURL:    w.FileURL,
		SampleURL:  w.SampleURL,
		PreviewURL: w.PreviewURL,
		Width:      int(w.Width),
		Height:     int(w.Height),
		Score:      int(w.Score),
		Rating:     w.Rating,
		Image:      w.Image,
		Hash:       w.Hash,
		Owner:      w.Owner,
		Source:     w.Source,
		Status:     w.Status,
		FileSize:   int(w.FileSize),
		FileType:   detectFileType(w.FileURL),
	}, nil
}

// flexInt принимает и число, и строку с числом ("123"), и мусор вроде
// "42\/9b" (в этом случае 0): сайты семейства непоследовательны в типах.
type flexInt int

func (fi *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		n, _ := strconv.ParseInt(strings.Trim(strings.TrimSpace(s), `"\`), 10, 64)
		*fi = flexInt(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return nil
	}
	*fi = flexInt(n)
	return nil
}

func (c *booruClient) suggestionCacheGet(key string) ([]TagSuggestion, bool) {
	c.suggMu.Lock()
	defer c.suggMu.Unlock()
	e, ok := c.suggM[key]
	if !ok || time.Since(e.ts) > suggestionCacheTTL {
		return nil, false
	}
	return e.items, true
}

func (c *booruClient) suggestionCachePut(key string, items []TagSuggestion) {
	c.suggMu.Lock()
	defer c.suggMu.Unlock()
	if _, ok := c.suggM[key]; !ok && len(c.suggM) >= suggestionCacheMax {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.suggM {
			if oldest.IsZero() || e.ts.Before(oldest) {
				oldestKey, oldest = k, e.ts
			}
		}
		delete(c.suggM, oldestKey)
	}
	c.suggM[key] = suggestionCacheEntry{items: items, ts: time.Now()}
}

func (c *booruClient) SuggestTags(query string) ([]TagSuggestion, error) {
	if len(query) < 2 {
		return []TagSuggestion{}, nil
	}

	ckey := strings.ToLower(strings.TrimSpace(query))
	if cached, ok := c.suggestionCacheGet(ckey); ok {
		return cached, nil
	}

	if !c.breaker.Allow() {
		return nil, fmt.Errorf("%s API временно недоступен — слишком много ошибок подряд, пауза %dс", c.spec.name, int(breakerCoolDown.Seconds()))
	}

	c.keys.syncFromConfig(c.spec.name)
	attempts := 2
	if n := len(credentialsForSite(GetConfig().GetAPICredentials(), c.spec.name)); n > attempts {
		attempts = n
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if !c.breaker.Allow() {
			return nil, fmt.Errorf("%s API временно недоступен (брейкер открыт)", c.spec.name)
		}
		cred, ok := c.keys.pickCred()
		if !ok {
			if c.spec.requireAuth {
				return nil, fmt.Errorf("нет доступных API ключей — все на карантине")
			}
			cred = APICredential{}
		}

		v := url.Values{}
		switch c.spec.suggestMode {
		case "tagindex":
			// Префиксный поиск по индексу тегов (MySQL LIKE): name_pattern=cat%
			v.Set("page", "dapi")
			v.Set("s", "tag")
			v.Set("q", "index")
			v.Set("json", "1")
			v.Set("name_pattern", query+"%")
			v.Set("limit", "20")
		default:
			apiBase := strings.TrimSuffix(strings.Replace(c.spec.apiURL, "/index.php", "", 1), "/")
			v = url.Values{"q": {query}}
			req0 := fmt.Sprintf("%s/autocomplete.php?%s", apiBase, v.Encode())
			sugg, err := c.fetchSuggest(req0, cred, query)
			if err != nil {
				lastErr = err
				if errors.Is(err, errAPI403) || errors.Is(err, errAPIAuth) {
					c.keys.report(cred.APIKey, false, "auth")
					continue
				}
				return nil, err
			}
			c.keys.report(cred.APIKey, true, "")
			c.suggestionCachePut(ckey, sugg)
			return sugg, nil
		}
		if cred.APIKey != "" {
			v.Set("api_key", cred.APIKey)
		}
		if cred.UserID != "" {
			v.Set("user_id", cred.UserID)
		}
		reqURL := fmt.Sprintf("%s?%s", c.spec.apiURL, v.Encode())

		sugg, err := c.fetchSuggest(reqURL, cred, query)
		if err != nil {
			lastErr = err
			if errors.Is(err, errAPIAuth) || errors.Is(err, errAPI403) {
				c.keys.report(cred.APIKey, false, "auth")
				log.Printf("API key ...%s rejected in tag suggest, switching key (attempt %d/%d)",
					keyTail(cred.APIKey), attempt+1, attempts)
				continue
			}
			if !errors.Is(err, errSuggestTransient) {
				return nil, err
			}
			continue
		}
		c.keys.report(cred.APIKey, true, "")
		c.suggestionCachePut(ckey, sugg)
		return sugg, nil
	}
	return nil, lastErr
}

// errSuggestTransient — 429/5xx: стоит повторить с другим ключом.
var errSuggestTransient = errors.New("tag suggest transient")

func (c *booruClient) fetchSuggest(reqURL string, cred APICredential, query string) ([]TagSuggestion, error) {
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		c.breaker.Fail(c.spec.name)
		return nil, fmt.Errorf("tag suggest failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("%w: status 401", errAPIAuth)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: tag suggest 403", errAPI403)
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		c.breaker.Fail(c.spec.name)
		return nil, fmt.Errorf("%w: status %d", errSuggestTransient, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.breaker.Success()

	sugg := c.parseSuggestions(body)

	// Страховка от «творческих» апстримов (fuzzy-автодополнение r34,
	// мусорные теги AI-заливок): оставляем только теги, реально
	// начинающиеся с введённого префикса; без пустых значений и дублей.
	if ql := strings.ToLower(strings.TrimSpace(query)); ql != "" {
		filtered := make([]TagSuggestion, 0, len(sugg))
		seen := make(map[string]bool, len(sugg))
		for _, s := range sugg {
			v := strings.ToLower(strings.TrimSpace(s.Value))
			if v == "" || seen[v] || !strings.HasPrefix(v, ql) {
				continue
			}
			seen[v] = true
			filtered = append(filtered, s)
		}
		sugg = filtered
	}

	return sugg, nil
}

// parseSuggestions разбирает оба формата автодополнения:
// autocomplete.php → [{"label":..,"value":..,"count":N}],
// tag index (dapi s=tag) → {"@attributes":..,"tag":[{"name":..,"count":N,"type":T}]}.
func (c *booruClient) parseSuggestions(body []byte) []TagSuggestion {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return []TagSuggestion{}
	}
	if trimmed[0] == '{' {
		var wrap struct {
			Tag []struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			} `json:"tag"`
		}
		if err := json.Unmarshal(trimmed, &wrap); err == nil && wrap.Tag != nil {
			out := make([]TagSuggestion, 0, len(wrap.Tag))
			for _, t := range wrap.Tag {
				if t.Name == "" || t.Count <= 0 {
					continue
				}
				out = append(out, TagSuggestion{Label: t.Name, Value: t.Name, Count: t.Count})
			}
			sort.Slice(out, func(i, j int) bool {
				if out[i].Count != out[j].Count {
					return out[i].Count > out[j].Count
				}
				return out[i].Value < out[j].Value
			})
			return out
		}
		return []TagSuggestion{}
	}

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return []TagSuggestion{}
	}
	suggestions := make([]TagSuggestion, 0, len(raw))
	for _, r := range raw {
		var s TagSuggestion
		if v, ok := r["value"]; ok {
			json.Unmarshal(v, &s.Value)
		}
		if v, ok := r["label"]; ok {
			json.Unmarshal(v, &s.Label)
			if s.Label == "" {
				s.Label = s.Value
			}
		}
		if cnt, ok := r["count"]; ok {
			var cntInt int
			if err := json.Unmarshal(cnt, &cntInt); err == nil {
				s.Count = cntInt
			}
		}
		if s.Count == 0 && s.Label != "" && s.Label != s.Value {
			if idx := strings.LastIndex(s.Label, " ("); idx > 0 {
				if end := strings.Index(s.Label[idx:], ")"); end > 0 {
					if cnt, err := strconv.Atoi(s.Label[idx+2 : idx+end]); err == nil {
						s.Count = cnt
						s.Label = s.Label[:idx]
					}
				}
			}
		}
		suggestions = append(suggestions, s)
	}
	return suggestions
}

func (c *booruClient) GetTagCount(tag string) (int, error) {
	if tag == "" || len(tag) < 2 {
		return 0, fmt.Errorf("tag too short")
	}
	suggestions, err := c.SuggestTags(tag)
	if err != nil {
		return 0, err
	}
	if len(suggestions) == 0 {
		return 0, nil
	}

	for _, s := range suggestions {
		if s.Value == tag {
			return s.Count, nil
		}
	}

	return suggestions[0].Count, nil
}

// GetPopularTags возвращает самые частотные теги из свежей выборки постов
// активного источника (тот же сайт, что и /api/posts).
func (c *booruClient) GetPopularTags(limit int) ([]TagSuggestion, error) {
	if limit <= 0 || limit > 60 {
		limit = 40
	}
	posts, err := c.SearchPosts("", 1, 100, 0)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, p := range posts {
		for _, t := range strings.Fields(p.Tags) {
			name := strings.TrimSpace(strings.ToLower(t))
			if name == "" || strings.Contains(name, ":") || len(name) > 24 {
				continue
			}
			counts[name]++
		}
	}

	out := make([]TagSuggestion, 0, len(counts))
	for name, n := range counts {
		out = append(out, TagSuggestion{Label: name, Value: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ratingFilter возвращает метатеги для серверного запроса и набор
// рейтингов, вырезаемых локально по полю Rating. Словари рейтингов у
// сайтов разошлись: gelbooru/safebooru и rule34 с 2026 используют
// danbooru-стиль general/sensitive/questionable/explicit, старый rule34 —
// safe/explicit (проверено живыми запросами 2026-08: rating="safe" больше
// не встречается, поэтому прежний фильтр 18+ стал no-op). Исключаем оба
// словаря сразу: лишний метатег не совпадает ни с чем, а локальный фильтр
// гарантирует результат даже если сайт обрезал хвост запроса.
func ratingFilter(mode string) ([]string, map[string]bool) {
	switch mode {
	case "sfw":
		// SFW = только general (плюс legacy-safe).
		return []string{"-rating:explicit", "-rating:questionable", "-rating:sensitive"},
			map[string]bool{"explicit": true, "questionable": true, "sensitive": true}
	case "nsfw":
		return []string{"-rating:general", "-rating:safe"},
			map[string]bool{"general": true, "safe": true}
	}
	return nil, nil
}

func detectFileType(fileURL string) string {
	lower := strings.ToLower(fileURL)
	if strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".webm") {
		return "video"
	}
	if strings.HasSuffix(lower, ".gif") {
		return "gif"
	}
	if strings.HasSuffix(lower, ".png") {
		return "png"
	}
	if strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".jpg") {
		return "jpg"
	}
	if strings.HasSuffix(lower, ".webp") {
		return "webp"
	}
	return "unknown"
}
