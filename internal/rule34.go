package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const UserAgent = "Better34/1.0"

func (c *booruClient) SourceComments(postID int) ([]SourceComment, error) {
	if postID <= 0 {
		return nil, fmt.Errorf("invalid post id")
	}
	if c.srcCommentsOff.Load() {
		return nil, ErrSourceCommentsUnsupported
	}
	c.keys.syncFromConfig(c.spec.name)
	cred, ok := c.keys.pickCred()
	if !ok {
		if c.spec.requireAuth {
			return nil, fmt.Errorf("%w для %s", ErrNoCredentials, c.spec.title)
		}
		cred = APICredential{}
	}

	v := url.Values{}
	v.Set("page", "dapi")
	v.Set("s", "comment")
	v.Set("q", "index")
	v.Set("json", "1")
	v.Set("post_id", strconv.Itoa(postID))
	if cred.APIKey != "" {
		v.Set("api_key", cred.APIKey)
	}
	if cred.UserID != "" {
		v.Set("user_id", cred.UserID)
	}
	reqURL := fmt.Sprintf("%s?%s", c.spec.apiURL, v.Encode())

	c.limiter.Wait()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json, application/xml")

	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("source comments request failed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read source comments: %w", err)
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, errAPIAuth
	case resp.StatusCode == 429:
		return nil, errAPITransient
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("source comments API returned %d", resp.StatusCode)
	}

	list, err := parseDapiComments(body)
	if errors.Is(err, ErrSourceCommentsUnsupported) {
		// Запомним, что сайт не отдаёт комментарии, чтобы следующий открытый
		// пост не дёргал API снова (gelbooru отвечает так на каждый запрос).
		c.srcCommentsOff.Store(true)
	}
	return list, err
}

// booruClient — общий клиент для всех Gelbooru-0.2-совместимых сайтов.
// Вся инфраструктура (кэши, брейкер, ротация ключей, singleflight)
// изолирована по экземпляру, т.е. по сайту.
type booruClient struct {
	spec        siteSpec
	httpClient  atomic.Value
	breaker     circuitBreaker
	suggBreaker circuitBreaker // отдельный: сбои автодополнения не блокируют поиск
	keys        keyManager
	sf          singleflightGroup
	suggSF      suggestFlightGroup
	limiter     *rateLimiter
	// idLimiter — отдельное ведро для выборки постов по id (см. idRatePerSecond).
	// Нужно, чтобы сотня одиночных запросов профиля не отбирала токены у
	// интерактивного поиска ленты.
	idLimiter *rateLimiter
	cache     *booruCache

	suggMu sync.Mutex
	suggM  map[string]suggestionCacheEntry
	// srcCommentsOff — сайт не отдаёт комментарии (gelbooru отключил s=comment).
	// Флаг ставится при первом таком ответе, чтобы не опрашивать вхолостую.
	srcCommentsOff atomic.Bool
}

func newBooruClient(spec siteSpec) *booruClient {
	c := &booruClient{
		spec:      spec,
		suggM:     make(map[string]suggestionCacheEntry),
		limiter:   newRateLimiterAt(apiRatePerSecond, apiRateBurst),
		idLimiter: newRateLimiterAt(idRatePerSecond, idRateBurst),
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

// proxyDeadPause — на столько перестаём пробовать прокси после неудачного
// dial. Мёртвый прокси не «чинится» сам, а проверка его на каждом соединении
// съедала dial-таймаут каждого запроса; прямое соединение при этом работало.
const proxyDeadPause = 5 * time.Minute

func buildTransport() *http.Transport {
	transport := NewResolveTransport()
	raw := GetConfig().GetProxyURL()
	// BRIEFLY_PROXY_URL перекрывает настройку из UI: быстрый способ сменить
	// или подставить рабочий прокси на время запуска, не трогая конфиг.
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_PROXY_URL")); v != "" {
		raw = v
	}
	if raw == "" {
		return transport
	}
	addr := raw
	if !strings.Contains(addr, "://") {
		addr = "socks5://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "socks5" && u.Scheme != "socks5h") {
		log.Printf("[proxy] адрес %q не распознан как socks5://host:port — прокси игнорируется", raw)
		return transport
	}
	dialer, err := proxy.SOCKS5("tcp", u.Host, nil, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		return transport
	}

	origDial := transport.DialContext

	warnMu := new(sync.Mutex)
	lastWarn := new(time.Time)
	// Пауза после неудачного dial: мёртвый прокси иначе проверялся на КАЖДОМ
	// соединении, и каждый запрос сперва честно ждал i/o timeout (3с), а уже
	// потом шёл напрямую. На пачке в сотню одиночных запросов это превращалось
	// в минуты чистого ожидания. Теперь после первой неудачи прокси
	// пропускается на proxyDeadPause — прямое соединение работает, а вернуться
	// можно, просто сохранив настройку заново (или по истечении паузы).
	var deadUntil atomic.Int64
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if time.Now().UnixNano() < deadUntil.Load() {
			return origDial(ctx, network, addr)
		}
		conn, err := dialer.Dial(network, addr)
		if err == nil {
			return conn, nil
		}
		deadUntil.Store(time.Now().Add(proxyDeadPause).UnixNano())
		// Мёртвый прокси раньше молча отбрасывал трафик на прямой канал:
		// в журнале оставались только загадочные обрывы «удалённой стороной»,
		// и непонятно было, что прокси вообще не работает. Логируем откаты
		// не чаще раза в минуту — ретраи идут часто.
		warnMu.Lock()
		loud := time.Since(*lastWarn) > time.Minute
		if loud {
			*lastWarn = time.Now()
		}
		warnMu.Unlock()
		if loud {
			log.Printf("[proxy] socks %s недоступен (%v) — откат на прямое соединение на %s",
				u.Host, err, proxyDeadPause)
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
	return c.SearchPostsCtx(context.Background(), tags, page, limit, minID)
}

// SearchPostsCtx — поиск с отменой по контексту. Выборка постов по id
// (см. GetPostsByIDs) у сайтов вроде gelbooru — это сотня отдельных
// запросов; без отмены клиент, ушедший по своему дедлайну, оставлял сервер
// жечь лимит источника ещё на минуту и блокировать поиск ленты.
//
// isIDLookup выбирает отдельное ведро троттлинга: сотня одиночных запросов
// иначе отбирает все токены сайта у интерактивного поиска.
func (c *booruClient) SearchPostsCtx(ctx context.Context, tags string, page, limit, minID int) ([]Rule34Post, error) {
	pid := page - 1

	if c.spec.supportsSort && !strings.Contains(tags, "sort:") {
		tags = strings.TrimSpace(tags)
		if tags != "" {
			tags += " "
		}
		tags += "sort:id:desc"
	}

	ckey := fmt.Sprintf("%s|%d|%d|%d", tags, page, limit, minID)
	if posts, ts, ok := c.cache.getStale(ckey); ok {
		if time.Since(ts) <= searchCacheTTL {
			log.Printf("[search-cache] hit %s tags=%q page=%d", c.spec.name, tags, page)
			return posts, nil
		}
		if time.Since(ts) <= searchCacheMaxStale {
			// SWR: ответ протух по TTL, но не древний — отдаём мгновенно,
			// свежесть восстанавливаем фоном. Повторный запрос того же
			// листа не ждёт RTT до CDN.
			log.Printf("[search-cache] stale hit %s tags=%q page=%d — фоновое обновление", c.spec.name, tags, page)
			go c.refreshSearch(ckey, tags, pid, limit, minID)
			return posts, nil
		}
	}

	if !c.breaker.Allow() {
		log.Printf("[breaker] %s API закрыт, запрос %q отменён без обращения к сети", c.spec.name, tags)
		return nil, fmt.Errorf("%s API временно недоступен — слишком много ошибок подряд, пауза %dс", c.spec.title, int(breakerCoolDown.Seconds()))
	}

	limiter := c.limiter
	if isIDLookup(tags) {
		limiter = c.idLimiter
	}

	run := func() ([]Rule34Post, error) {
		if posts, ok := c.cache.get(ckey); ok {
			return posts, nil
		}
		posts, err := c.fetchPostsCtx(ctx, ckey, tags, pid, limit, minID, limiter)
		if err != nil {
			if !errors.Is(err, errAPIAuth) && !errors.Is(err, errAPI403) && !errors.Is(err, errAPITransient) && !errors.Is(err, ErrNoCredentials) {
				// Отмена по контексту — не поломка источника: винить его и
				// открывать брейкер из-за ушедшего клиента незачем.
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					c.breaker.Fail(c.spec.name)
				}
			}
		} else {
			c.breaker.Success()
		}
		return posts, err
	}

	posts, err := c.sf.Do(ckey, run)
	// singleflight делит один сетевой запрос на всех ожидающих, и отмену
	// первого ушедшего клиента наследуют остальные. Если наш контекст ещё
	// жив, а ошибка — отмена чужого, повторяем запрос сами.
	if err != nil && ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		posts, err = c.sf.Do(ckey, run)
	}
	return posts, err
}

// isIDLookup — запрос выборки постов по id (tags=id:123). Такой запрос на
// gelbooru уходит отдельным HTTP-вызовом, поэтому у него свой троттлинг.
func isIDLookup(tags string) bool {
	t := strings.TrimSpace(tags)
	if !strings.HasPrefix(t, "id:") {
		return false
	}
	rest := strings.TrimSpace(t[3:])
	// Только цифры (и запятые у пакетной формы) — иначе это обычный поиск,
	// в котором «id:» встретился как часть тега.
	for _, r := range rest {
		if (r < '0' || r > '9') && r != ',' {
			return false
		}
	}
	return rest != ""
}

// refreshSearch обновляет устаревший кэш фоном (SWR). Ошибки не важны:
// следующий запрос просто ещё раз уйдёт в сеть. Открытый брейкер снаружи —
// фоновое обновление не долбит апстрим во время его проблем.
func (c *booruClient) refreshSearch(ckey, tags string, pid, limit, minID int) {
	if !c.breaker.Allow() {
		return
	}
	c.sf.Do(ckey, func() ([]Rule34Post, error) {
		if posts, ok := c.cache.get(ckey); ok {
			return posts, nil
		}
		posts, err := c.fetchPosts(ckey, tags, pid, limit, minID, c.limiter)
		if err != nil {
			if !errors.Is(err, errAPIAuth) && !errors.Is(err, errAPI403) && !errors.Is(err, errAPITransient) && !errors.Is(err, ErrNoCredentials) {
				c.breaker.Fail(c.spec.name)
			}
		} else {
			c.breaker.Success()
		}
		return posts, err
	})
}

func (c *booruClient) fetchPosts(ckey, tags string, pid, limit, minID int, limiter *rateLimiter) ([]Rule34Post, error) {
	return c.fetchPostsCtx(context.Background(), ckey, tags, pid, limit, minID, limiter)
}

func (c *booruClient) fetchPostsCtx(ctx context.Context, ckey, tags string, pid, limit, minID int, limiter *rateLimiter) ([]Rule34Post, error) {
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
		// Пользовательский синтаксис "+tag" (форс-включение) снимаем только
		// в начале токена: "+" внутри тега (например, "c++") валиден.
		fields := strings.Fields(restTags)
		for i, t := range fields {
			fields[i] = strings.TrimLeft(t, "+")
		}
		baseV.Set("tags", strings.Join(fields, " "))
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
			return nil, fmt.Errorf("%s API недоступен (брейкер открыт)", c.spec.title)
		}

		cred, ok := c.keys.pickCred()
		if !ok {
			if c.spec.requireAuth {
				return nil, fmt.Errorf("%w для %s", ErrNoCredentials, c.spec.title)
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
		if logAPIKeyDebug {
			log.Printf("[api-key-check] %s request tags=%q uses key ...%s user_id=%q (attempt %d/%d)",
				c.spec.name, restTags, keyTail(key), userID, attempt, attempts)
		}

		// Троттлинг и сам HTTP-запрос идут под контекстом: клиент, ушедший по
		// своему дедлайну, не должен заставить сервер дожидаться токена и потом
		// ещё выслать запрос в источник.
		if err := limiter.WaitCtx(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTPClient().Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c.keys.report(key, false, "network")
			lastErr = fmt.Errorf("API request failed: %w", err)
			if attempt < attempts {
				log.Printf("API retry %d/%d for %q: %v", attempt, attempts, restTags, err)
				if !sleepCtx(ctx, jitteredBackoff(attempt)) {
					return nil, ctx.Err()
				}
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			c.keys.report(key, false, "read")
			lastErr = fmt.Errorf("failed to read API response: %w", err)
			if attempt < attempts {
				log.Printf("API retry %d/%d for %q: %v", attempt, attempts, restTags, err)
				if !sleepCtx(ctx, jitteredBackoff(attempt)) {
					return nil, ctx.Err()
				}
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
				if !sleepCtx(ctx, 150*time.Millisecond) {
					return nil, ctx.Err()
				}
			}
			continue
		case resp.StatusCode == 429:
			// Рейт-лимит: уважаем Retry-After, ретраим, но после
			// исчерпания попыток НЕ считаем аварией (errAPITransient).
			c.keys.report(key, false, "transient")
			lastErr = fmt.Errorf("%w: status 429 (body: %s)", errAPITransient, string(body))
			if attempt < attempts {
				delay := jitteredBackoff(attempt)
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
						delay = time.Duration(min(secs, 10)) * time.Second
					}
				}
				log.Printf("API retry %d/%d for %q: status 429 (sleep %s)", attempt, attempts, restTags, delay)
				if !sleepCtx(ctx, delay) {
					return nil, ctx.Err()
				}
			}
			continue
		case resp.StatusCode >= 500:
			c.keys.report(key, false, "transient")
			lastErr = fmt.Errorf("API returned status %d (body: %s)", resp.StatusCode, string(body))
			if attempt < attempts {
				delay := jitteredBackoff(attempt)
				log.Printf("API retry %d/%d for %q: status %d (sleep %s)", attempt, attempts, restTags, resp.StatusCode, delay)
				if !sleepCtx(ctx, delay) {
					return nil, ctx.Err()
				}
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
			if strings.Contains(bodyStr, "Too deep") {
				// Gelbooru выдаёт plain text «Too deep! Pull it back some.» при offset > 20000.
				// Это не авария API, а выход за пределы доступных постов: отдаём пустой результат.
				return []Rule34Post{}, nil
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
