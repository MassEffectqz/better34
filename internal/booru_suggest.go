package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type suggestionCacheEntry struct {
	items []TagSuggestion
	ts    time.Time
}

const suggestionCacheTTL = 30 * time.Minute
const suggestionCacheMax = 512

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
	// Служебные префиксы (-, ~, …) убираем: для апстрима нужен сам тег.
	query = suggestQueryPrefix(query)
	// По рунам, а не байтам: один кириллический символ — 2 байта.
	if utf8.RuneCountInString(query) < 2 {
		return []TagSuggestion{}, nil
	}

	ckey := strings.ToLower(strings.TrimSpace(query))
	if cached, ok := c.suggestionCacheGet(ckey); ok {
		return cached, nil
	}

	return c.suggSF.Do(ckey, func() ([]TagSuggestion, error) {
		// Двойная проверка кэша: пока мы ждали лидера, он мог
		// уже положить результат.
		if cached, ok := c.suggestionCacheGet(ckey); ok {
			return cached, nil
		}

		if !c.suggBreaker.Allow() {
			return nil, fmt.Errorf("%s автодополнение временно недоступно — слишком много ошибок подряд, пауза %dс", c.spec.name, int(breakerCoolDown.Seconds()))
		}

		c.keys.syncFromConfig(c.spec.name)
		attempts := 2
		if n := len(credentialsForSite(GetConfig().GetAPICredentials(), c.spec.name)); n > attempts {
			attempts = n
		}

		var lastErr error
		for attempt := 1; attempt <= attempts; attempt++ {
			if !c.suggBreaker.Allow() {
				return nil, fmt.Errorf("%s автодополнение временно недоступно (брейкер открыт)", c.spec.name)
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
				// limit=20 НЕ ХВАТАЛО. Индекс сортирует по частоте, поэтому на
				// коротком префиксе двадцатка самых популярных ПРОИЗВОДНЫХ тегов
				// вытесняла базовый: по «umamu» отдавало umamusume:_cinderella_gray
				// и т.п., а сам umamusume не попадал в выдачу.
				// Берём 100, отсекаем лишнее уже у себя после ранжирования.
				v.Set("limit", "100")
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
			// Базовый тег самого префикса в выдаче может отсутствовать целиком:
			// индекс gelbooru по «furry» не отдаёт «furry» (200 376 постов) — его
			// вытесняют производные furry_*, и по «uma» нет «umamusume»
			// (213 597). Точный запрос name=<tag> отдаёт его сразу.
			sugg = c.ensureExactTag(sugg, query, cred)
			c.suggestionCachePut(ckey, sugg)
			return sugg, nil
		}
		return nil, lastErr
	})
}

// errSuggestTransient — 429/5xx: стоит повторить с другим ключом.
var errSuggestTransient = errors.New("tag suggest transient")

// ensureExactTag добавляет базовый тег префикса, если его нет в выдаче.
//
// Зачем: индекс тегов gelbooru с name_pattern=<prefix>% режет выдачу и не
// возвращает сам базовый тег — по «furry» приходит furry_breasts (4 698 918),
// furry_ears (1 260 528) и прочие производные, но не «furry» (200 376 постов),
// и по «uma» нет «umamusume» (213 597). Пользователь набирал префикс, который
// сам является тегом, и не получал его в подсказках.
//
// Точный запрос name=<tag> (без %) отдаёт его сразу — он идёт через общий
// кэш счётчиков, поэтому повторные обращения не бьют по сети. Если точного
// тега на сайте нет, name_pattern даёт пустой ответ и список не меняется.
func (c *booruClient) ensureExactTag(sugg []TagSuggestion, query string, cred APICredential) []TagSuggestion {
	tag := strings.TrimSpace(query)
	if tag == "" {
		return sugg
	}
	for _, s := range sugg {
		if strings.EqualFold(strings.TrimSpace(s.Value), tag) {
			return sugg // уже есть, второй запрос не нужен
		}
	}
	count, err := c.getTagCountDapiExact(tag, cred)
	if err != nil || count <= 0 {
		return sugg // тега нет — нечего показывать
	}
	exact := TagSuggestion{Label: tag, Value: tag, Count: count}
	// Точное совпадение идёт первым: его набрали буквально.
	return append([]TagSuggestion{exact}, sugg...)
}

func (c *booruClient) fetchSuggest(reqURL string, _ APICredential, query string) ([]TagSuggestion, error) {
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Accept", "application/json")

	c.limiter.Wait()
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		c.suggBreaker.Fail(c.spec.name)
		return nil, fmt.Errorf("tag suggest failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("%w: status 401", errAPIAuth)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("%w: tag suggest 403", errAPI403)
	}
	if resp.StatusCode == 429 {
		// Рейт-лимит — наша вина (всплеск), а не отказ API: брейкер
		// открывать нельзя, иначе автодополнение блокирует весь сайт.
		return nil, fmt.Errorf("%w: status 429", errSuggestTransient)
	}
	if resp.StatusCode >= 500 {
		c.suggBreaker.Fail(c.spec.name)
		return nil, fmt.Errorf("%w: status %d", errSuggestTransient, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	c.suggBreaker.Success()

	sugg := c.parseSuggestions(body)

	return filterSuggestionsByPrefix(sugg, query), nil
}

// suggestQueryPrefix нормализует ввод автодополнения: обрезает пробелы и
// служебные префиксы booru-синтаксиса (-tag, +tag, ~tag, ^tag). Без этого
// подсказки по «-brea» не находятся: ни один тег не начинается с минуса.
func suggestQueryPrefix(query string) string {
	q := strings.TrimSpace(query)
	for len(q) > 0 && strings.IndexByte("-+~^", q[0]) >= 0 {
		q = strings.TrimSpace(q[1:])
	}
	return q
}

// filterSuggestionsByPrefix оставляет теги, содержащие префикс, и
// ранжирует их по релевантности: точное совпадение, префиксное, граница
// слова, прочее вхождение (см. suggestionTier); внутри ступени — по
// убыванию счётчика, затем короче имя и алфавит для стабильного порядка.
//
// Раньше отсечение шло строго по HasPrefix, из-за чего теги вроде
// solo_breasts выпадали из выдачи целиком: набрав «breast», пользователь не
// видел ничего, кроме breast*. Теперь такие теги не теряются, а просто идут
// ниже. Обратная сторона fuzzy-апстрима (r34) и мусорных тегов AI-заливок
// по-прежнему отсекается: в tier попадает только то, где префикс реально
// встречается в имени.
func filterSuggestionsByPrefix(sugg []TagSuggestion, query string) []TagSuggestion {
	ql := strings.ToLower(suggestQueryPrefix(query))
	filtered := make([]TagSuggestion, 0, len(sugg))
	seen := make(map[string]bool, len(sugg))
	for _, s := range sugg {
		v := strings.ToLower(strings.TrimSpace(s.Value))
		if v == "" || seen[v] {
			continue
		}
		if suggestionTier(v, ql) < 0 {
			continue
		}
		seen[v] = true
		filtered = append(filtered, s)
	}
	sortSuggestionsByRelevance(filtered, ql)
	return filtered
}

// suggestionTier — «релевантность» тега к набранному префиксу. Чем меньше,
// тем лучше. Именно tier, а не длина имени, должен решать порядок выдачи:
// 0 — точное совпадение, 1 — префиксное (cat_girl по «cat»), 2 — совпадение
// на границе слова после «_» (solo_breasts по «breast»), 3 — прочее вхождение
// внутрь имени (huge_breasts по «breast»). Пустой префикс: все равны (tier 0).
func suggestionTier(value, prefix string) int {
	if prefix == "" {
		return 0
	}
	v := strings.ToLower(strings.TrimSpace(value))
	if v == prefix {
		return 0
	}
	if strings.HasPrefix(v, prefix) {
		return 1
	}
	// Совпадение не в начале: ищем его и смотрим, начинает ли оно слово.
	if i := strings.Index(v, prefix); i > 0 {
		if v[i-1] == '_' {
			return 2
		}
		return 3
	}
	return -1 // префикса в теге нет вовсе, такой тег отбрасываем
}

// sortSuggestionsByPopularity сортирует подсказки: точное совпадение с
// префиксом, затем по убыванию счётчика, затем короче/алфавит.
func sortSuggestionsByRelevance(sugg []TagSuggestion, prefix string) {
	ql := strings.ToLower(prefix)
	sort.SliceStable(sugg, func(i, j int) bool {
		a, b := sugg[i], sugg[j]
		av, bv := strings.ToLower(strings.TrimSpace(a.Value)), strings.ToLower(strings.TrimSpace(b.Value))
		if at, bt := suggestionTier(av, ql), suggestionTier(bv, ql); at != bt {
			return at < bt
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if len(av) != len(bv) {
			return len(av) < len(bv)
		}
		return av < bv
	})
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

// getTagCountDapiExact — точный счётчик тега через dapi s=tag&q=index&name=<tag>.
// Параметр name= ищет ТОЧНОЕ совпадение (в отличие от префиксного
// name_pattern=…%), поэтому работает для составных тегов со скобками и
// подчёркиваниями, которых нет в prefix-автодополнении autocomplete.php.
func (c *booruClient) getTagCountDapiExact(tag string, cred APICredential) (int, error) {
	v := url.Values{}
	v.Set("page", "dapi")
	v.Set("s", "tag")
	v.Set("q", "index")
	v.Set("json", "1")
	v.Set("name", tag)
	if cred.APIKey != "" {
		v.Set("api_key", cred.APIKey)
	}
	if cred.UserID != "" {
		v.Set("user_id", cred.UserID)
	}
	req, err := http.NewRequest("GET", fmt.Sprintf("%s?%s", c.spec.apiURL, v.Encode()), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Briefly/1.0")
	req.Header.Set("Accept", "application/json")

	c.limiter.Wait()
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("tag count dapi failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return 0, fmt.Errorf("%w: tag count 401", errAPIAuth)
	}
	if resp.StatusCode == 403 {
		return 0, fmt.Errorf("%w: tag count 403", errAPI403)
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return 0, fmt.Errorf("%w: tag count %d", errSuggestTransient, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	for _, s := range c.parseSuggestions(body) {
		if strings.EqualFold(s.Value, tag) {
			return s.Count, nil
		}
	}
	return 0, nil
}

func (c *booruClient) GetTagCount(tag string) (int, error) {
	if tag == "" || len(tag) < 2 {
		return 0, fmt.Errorf("tag too short")
	}
	suggestions, err := c.SuggestTags(tag)
	if err != nil {
		return 0, err
	}
	// Точное совпадение без учёта регистра: fuzzy-автодополнение возвращает
	// и похожие теги, счётчик «первого похожего» показывал неверные числа
	// в панелях счётчиков (кнопка «тег» во вьювере, профиль).
	want := strings.ToLower(tag)
	for _, s := range suggestions {
		if strings.ToLower(s.Value) == want {
			return s.Count, nil
		}
	}
	// Fallback: точный lookup по индексу тегов. Автодополнение ищет только
	// по префиксу и часто не знает составных тегов (скобки, подчёркивания) —
	// без fallback такие теги кэшировались как 0 и во вьювере «большинство
	// тегов показывалось как 0».
	if !c.suggBreaker.Allow() {
		return 0, nil
	}
	c.keys.syncFromConfig(c.spec.name)
	cred, ok := c.keys.pickCred()
	if !ok && c.spec.requireAuth {
		return 0, fmt.Errorf("нет доступных API ключей — все на карантине")
	}
	cnt, err := c.getTagCountDapiExact(tag, cred)
	if err != nil {
		if errors.Is(err, errAPIAuth) || errors.Is(err, errAPI403) {
			c.keys.report(cred.APIKey, false, "auth")
			log.Printf("API key ...%s rejected in tag count, reported (tag %q)", keyTail(cred.APIKey), tag)
		}
		return 0, err
	}
	c.keys.report(cred.APIKey, true, "")
	return cnt, nil
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
