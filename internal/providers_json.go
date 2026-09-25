package internal

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// ── Плагинные провайдеры без кода ────────────────────────────────────────
// Любой Gelbooru-0.2-совместимый сайт добавляется файлом data/providers.json
// (по образцу data/providers.json.example). Файл перечитывается при старте
// и, если появился/изменился, — в фоне (не чаще раза в 15с): новые сайты
// появляются в настройках и поиске без перекомпиляции.

type providerJSON struct {
	Name          string   `json:"name"`
	Title         string   `json:"title"`
	APIURL        string   `json:"api_url"`
	WWWURL        string   `json:"www_url"`
	MediaHosts    []string `json:"media_hosts"`
	RequireAuth   bool     `json:"require_auth"`
	WrappedResp   bool     `json:"wrapped_resp"`
	SupportsMinID bool     `json:"supports_min_id"`
	SupportsSort  bool     `json:"supports_sort"`
	IDListParam   bool     `json:"id_list_param"`
	BatchIDs      bool     `json:"batch_ids"`
	SuggestMode   string   `json:"suggest_mode"`
	CacheFile     string   `json:"cache_file"`
	MaxQueryLen   int      `json:"max_query_len"`
}

// var (а не const): тесты подменяют путь на временный каталог.
var providersJSONPath = "data/providers.json"

var (
	dynProvInit  sync.Once // защита однократной загрузки в loadDynamicProviderSpecs
	dynProvSpec  []siteSpec
	dynProvMod   atomic.Int64 // mtime последней загрузки (ноль = ещё не читали)
	dynProvRelMu sync.Mutex
)

// loadDynamicProviderSpecs читает data/providers.json и приводит к siteSpec.
// Дефектные записи пропускаются с логом: один битый сайт не должен рушить старт.
func loadDynamicProviderSpecs() []siteSpec {
	dynProvRelMu.Lock()
	defer dynProvRelMu.Unlock()
	loadDynamicProviderSpecsLocked()
	return dynProvSpec
}

// loadDynamicProviderSpecsLocked — внутренняя реализация под dynProvRelMu.
// maybeReloadProviders сбрасывает dynProvInit и dynProvSpec, поэтому читать
// и писать эти переменные можно только под тем же мьютексом (сброс Once
// параллельно с Do() — гонка по model of Go).
func loadDynamicProviderSpecsLocked() {
	dynProvInit.Do(func() {
		data, err := os.ReadFile(providersJSONPath)
		if err != nil {
			return // файла нет — это нормально, используем встроенных
		}
		var list []providerJSON
		if err := json.Unmarshal(stripJSONComments(data), &list); err != nil {
			log.Printf("[providers] %s: %v", providersJSONPath, err)
			return
		}
		for i, p := range list {
			name := strings.ToLower(strings.TrimSpace(p.Name))
			if name == "" || name == allProvidersName {
				log.Printf("[providers] #%d: пропущен (нужно name, нельзя «%s»)", i+1, allProvidersName)
				continue
			}
			if p.APIURL == "" || p.WWWURL == "" {
				log.Printf("[providers] %s: пропущен (api_url/www_url обязательны)", name)
				continue
			}
			if len(p.MediaHosts) == 0 {
				log.Printf("[providers] %s: media_hosts пуст — прокси медиа не будет работать", name)
			}
			cacheFile := p.CacheFile
			if cacheFile == "" {
				cacheFile = "data/cache/search_cache_" + name + ".json"
			}
			maxQueryLen := p.MaxQueryLen
			if maxQueryLen <= 0 {
				maxQueryLen = 3800
			}
			suggest := p.SuggestMode
			if suggest != "autocomplete" && suggest != "tagindex" {
				suggest = "autocomplete"
			}
			dynProvSpec = append(dynProvSpec, siteSpec{
				name:          name,
				title:         p.Title,
				apiURL:        p.APIURL,
				wwwURL:        p.WWWURL,
				mediaHosts:    p.MediaHosts,
				requireAuth:   p.RequireAuth,
				wrappedResp:   p.WrappedResp,
				supportsMinID: p.SupportsMinID,
				supportsSort:  p.SupportsSort,
				idListParam:   p.IDListParam,
				batchIDs:      p.BatchIDs,
				suggestMode:   suggest,
				cacheFile:     cacheFile,
				maxQueryLen:   maxQueryLen,
			})
		}
	})
}

// maybeReloadProviders перечитывает файл, если он появился/изменился после
// старта (не чаще раза в 15с). Вызывается из часто дёргающихся точек
// (GetSettings / isKnownProvider) — сам по себе дешёвый.
func maybeReloadProviders() {
	st, err := os.Stat(providersJSONPath)
	if err != nil {
		return
	}
	mod := st.ModTime().UnixNano()
	if mod == dynProvMod.Load() {
		return
	}
	dynProvRelMu.Lock()
	defer dynProvRelMu.Unlock()
	if mod == dynProvMod.Load() {
		return
	}
	dynProvMod.Store(mod)
	dynProvInit = sync.Once{}
	dynProvSpec = nil
	loadDynamicProviderSpecsLocked() // уже под dynProvRelMu — публичная версия ждала бы лок заново (дедлок)
	log.Printf("[providers] %s: перечитан, %d сторонних сайтов", providersJSONPath, len(dynProvSpec))
}

type providerDescriptor struct {
	Name        string `json:"value"`
	DisplayName string `json:"name"`
}

// stripJSONComments убирает комментарии (# … и // …) из JSON: так файл
// провайдеров удобно читать и править, не ломая парсинг. «//» внутри
// строки (например в URL) не трогается.
func stripJSONComments(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, trimInlineComment(ln))
	}
	return []byte(strings.Join(out, "\n"))
}

// trimInlineComment удаляет всё от непокрытого кавычками // или #.
func trimInlineComment(line string) string {
	inStr := false
	esc := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && c == '/' && i+1 < len(line) && line[i+1] == '/':
			return line[:i]
		case !inStr && c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// allProviderDescriptors — встроенные + динамические для настроек.
func allProviderDescriptors() []providerDescriptor {
	maybeReloadProviders()
	out := make([]providerDescriptor, 0, len(knownProviders)+4)
	for _, p := range knownProviders {
		out = append(out, providerDescriptor{p.Name, p.DisplayName})
	}
	for _, s := range loadDynamicProviderSpecs() {
		title := s.title
		if title == "" {
			title = s.name
		}
		out = append(out, providerDescriptor{s.name, title})
	}
	return out
}
