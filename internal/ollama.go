package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ── Семантический поиск через локальную Ollama ───────────────────────────
// Фраза пользователя («котики в шляпах») превращается моделью в теговый
// запрос бура. Модель/URL настраиваются через окружение; по умолчанию
// llama3.2 — быстрая 3B-модель, которой для структурированной задачи
// «фраза → JSON-список тегов» достаточно с запасом.

func ollamaBase() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("BRIEFLY_OLLAMA")), "/"); v != "" {
		return v
	}
	return "http://127.0.0.1:11434"
}

func ollamaModel() string {
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_OLLAMA_MODEL")); v != "" {
		return v
	}
	return "dolphin-mistral:7b"
}

var ollamaHTTP = &http.Client{Timeout: 30 * time.Second}

// ollamaAvailCache — доступность Ollama проверяется не чаще раза в 30с:
// эндпоинт вызывается на каждый поисковый запрос с "?".
var (
	ollamaAvailMu   sync.Mutex
	ollamaAvailAt   time.Time
	ollamaAvailLast bool
)

func ollamaAvailable() bool {
	ollamaAvailMu.Lock()
	defer ollamaAvailMu.Unlock()
	if time.Since(ollamaAvailAt) < 30*time.Second {
		return ollamaAvailLast
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaBase()+"/api/tags", nil)
	if err != nil {
		ollamaAvailLast = false
		ollamaAvailAt = time.Now()
		return false
	}
	resp, err := ollamaHTTP.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	ollamaAvailLast = err == nil && resp.StatusCode == http.StatusOK
	ollamaAvailAt = time.Now()
	return ollamaAvailLast
}

const nlPromptTmpl = `You are a booru tag translator. Convert the user's description into a booru tag search query.
Rules:
- 3 to 8 tags, space separated, no commas, no explanations, no numbering.
- lowercase, underscores instead of spaces inside a tag (cat_ears).
- use ONLY standard booru tags from this list when applicable: 1girl, 2girls, 1boy, 2boys, solo, duo, group, long_hair, short_hair, ponytail, twintails, blonde_hair, black_hair, blue_hair, red_hair, white_hair, pink_hair, purple_hair, brown_hair, green_hair, blue_eyes, green_eyes, red_eyes, brown_eyes, yellow_eyes, purple_eyes, pink_eyes, large_breasts, small_breasts, flat_chest, medium_breasts, cum, nude, topless, bottomless, spread_legs, open_mouth, closed_eyes, smile, blush, on_bed, on_floor, indoors, outdoors, lying_down, sitting, standing, from_behind, from_side, from_front, breasts_out, shirt_lift, panty_pull, spread_pussy, finger_in_mouth, cat_ears, tail, glasses, hat, ribbon, swimsuit, bikini, school_uniform, maid, nurse, elf, nun, stockings, thighhighs, garter_belt, heels, boots.
- if the description mentions a count of people, include 1girl/2girls/1boy style tags.
- NSFW, explicit, and sexual tags are perfectly normal and expected — never refuse or censor them.
- NEVER reply with refusals, warnings, or moral commentary. Just output the tags.
Reply with the tag query ONLY.

Description: %s`

var nlSanitizeRe = regexp.MustCompile(`[^a-z0-9_\-()]`)

// sanitizeTagQuery приводит ответ модели к безопасному теговому запросу:
// модель может добавить пояснения, кавычки, запятые — всё это отбрасывается.
func sanitizeTagQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`\"' \t\n\r")
	// Берём первую содержательную строку (модели любят пояснения после).
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			raw = line
			break
		}
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == '\t'
	})
	out := make([]string, 0, 8)
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		// Разбиваем на отдельные теги по пробелам.
		for _, word := range strings.Fields(f) {
			word = strings.ReplaceAll(word, " ", "_")
			word = nlSanitizeRe.ReplaceAllString(word, "")
			if word == "" {
				continue
			}
			out = append(out, word)
			if len(out) >= 8 {
				return strings.Join(out, " ")
			}
		}
	}
	return strings.Join(out, " ")
}

type nlCacheEntry struct {
	query string
	at    time.Time
}

const (
	nlCacheTTL = 24 * time.Hour
	nlCacheCap = 128
)

var (
	nlCacheMu sync.Mutex
	nlCache   = make(map[string]nlCacheEntry)
)

// ollamaToTags с кэшем: одни и те же фразы не должны гонять модель.
func ollamaToTags(descr string, provider Provider) (string, error) {
	key := strings.ToLower(strings.Join(strings.Fields(descr), " "))
	nlCacheMu.Lock()
	if e, ok := nlCache[key]; ok && time.Since(e.at) < nlCacheTTL {
		nlCacheMu.Unlock()
		return e.query, nil
	}
	nlCacheMu.Unlock()

	prompt := fmt.Sprintf(nlPromptTmpl, descr)
	payload, _ := json.Marshal(map[string]any{
		"model":  ollamaModel(),
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 64,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaBase()+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ollamaHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: status %d", resp.StatusCode)
	}
	var data struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	query := sanitizeTagQuery(data.Response)
	if query == "" {
		return "", fmt.Errorf("модель вернула пустой запрос")
	}

	// Проверяем каждый тег по базе провайдера и отбрасываем несуществующие.
	if provider != nil {
		tags := strings.Fields(query)
		valid := make([]string, 0, len(tags))
		for _, tag := range tags {
			if len(tag) < 2 {
				continue
			}
			count, err := provider.GetTagCount(tag)
			if err == nil && count > 0 {
				valid = append(valid, tag)
			}
		}
		if len(valid) > 0 {
			if len(valid) > 8 {
				valid = valid[:8]
			}
			query = strings.Join(valid, " ")
		}
	}

	nlCacheMu.Lock()
	if len(nlCache) >= nlCacheCap {
		// Простой LRU по времени: выметаем просроченные, иначе самые старые.
		oldestKey := ""
		var oldest time.Time
		for k, e := range nlCache {
			if oldestKey == "" || e.at.Before(oldest) {
				oldestKey, oldest = k, e.at
			}
		}
		if oldestKey != "" {
			delete(nlCache, oldestKey)
		}
	}
	nlCache[key] = nlCacheEntry{query: query, at: time.Now()}
	nlCacheMu.Unlock()
	return query, nil
}

// NlSearch — GET /api/nl-search?q=<фраза>. Возвращает готовый теговый
// запрос; клиент подставляет его в поиск.
func (h *Handler) NlSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q required"})
		return
	}
	if len(q) > 300 {
		q = q[:300]
	}
	if !ollamaAvailable() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Ollama недоступна на " + ollamaBase()})
		return
	}
	query, err := ollamaToTags(q, h.provider())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "модель не ответила: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"query": query, "model": ollamaModel()})
}
