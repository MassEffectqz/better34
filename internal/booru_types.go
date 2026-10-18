package internal

import (
	"errors"
	"net/http"
	"strings"
	"time"
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
	// HasComments приходит строкой "true"/"false" даже при json=1 — разбирает flexBool.
	HasComments flexBool `json:"has_comments"`
	FileSize    int      `json:"file_size"`
	FileType    string   `json:"file_type"`
}

// SourceComment — комментарий поста на самом источнике (dapi s=comment).
// Локальные комментарии (internal/handlers_comments.go) — другая сущность.
type SourceComment struct {
	ID        int    `json:"id"`
	PostID    int    `json:"post_id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
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
	// SourceComments — комментарии поста на источнике (dapi s=comment).
	// Поддерживают не все сайты: Moebooru-ветка (safebooru) комментариев не
	// имеет вовсе, gelbooru отключил сервис — такие возвращают
	// ErrSourceCommentsUnsupported, это штатный ответ, не ошибка.
	SourceComments(postID int) ([]SourceComment, error)
}

const defaultProviderName = "rule34"

// allProvidersName — псевдо-источник «все сайты сразу»: поиск идёт
// параллельно по всем провайдерам с дедупликацией по md5.
const allProvidersName = "all"

var knownProviders = []struct {
	Name        string `json:"value"`
	DisplayName string `json:"name"`
}{
	{"all", "Все сайты"},
	{"rule34", "rule34.xxx"},
	{"gelbooru", "Gelbooru"},
	{"safebooru", "Safebooru"},
	{"hypnohub", "Hypnohub"},
}

func isKnownProvider(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == allProvidersName {
		return true
	}
	for _, p := range allProviderDescriptors() {
		if p.Name == name {
			return true
		}
	}
	return false
}

// multiProviderOrder — детерминированный порядок опроса сайтов в режиме «all».
var multiProviderOrder = []string{"rule34", "gelbooru", "safebooru", "hypnohub"}

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
	// ВАЖНО (проверено живыми запросами к dapi 2026-09): параметр id у
	// Gelbooru понимает РОВНО ОДИН id. Список id=1,2,3 молча игнорируется —
	// приходит обычная свежая выдача ровно на limit постов, причём limit
	// соблюдается, поэтому такой ответ неотличим от успеха. Одиночный
	// id=14856425 при этом работает, поэтому idListParam остаётся включённым,
	// а batchIDs выключен: пакетные выборки идут путём одиночных обращений
	// (providerSingleID) с ограничением параллелизма. При batchIDs=true
	// вкладки «Лайки»/«Скрытые» получали чужие посты вместо запрошенных —
	// см. TestGelbooruNoIDBatches.
	idListParam: true,
	batchIDs:    false,
	suggestMode: "tagindex",
	cacheFile:   "data/cache/search_cache_gelbooru.json",
	maxQueryLen: 1900,
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

var errAPI403 = errors.New("API returned 403")

// errAPIAuth — обёртка над 401/403: ключ невалиден для этого сайта.
var errAPIAuth = errors.New("API key rejected")

var errAPITransient = errors.New("API rate limited")

// ErrSourceCommentsUnsupported — источник не отдаёт комментарии (сервис
// отключён у сайта или его движок их не поддерживает). Это не сбой: такой
// ответ хендлер отдаёт клиенту штатным кодом «источник не поддерживает».
var ErrSourceCommentsUnsupported = errors.New("source comments unsupported")

// ErrNoCredentials — у сайта включён requireAuth, но нет доступных ключей в настройках (или все на карантине).
var ErrNoCredentials = errors.New("нет доступных API ключей — укажите API key в настройках")

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
