package internal

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// oddTagsDefault — встроенный список по умолчанию. Лежит в репозитории
// (в отличие от data/), поэтому подсветка работает сразу после сборки и
// служит образцом для правки своего файла.
//
//go:embed odd_tags_default.json
var oddTagsDefault []byte

// Список странных тегов для подсветки в просмотре поста. Источник —
// data/odd_tags.json, файл пользовательский: его правят руками, поэтому
// разбираем максимально терпимо (кривой JSON не должен ронять сервер) и
// перечитываем по TTL, чтобы правки подхватывались без перезапуска.
const (
	oddTagsFile      = "odd_tags.json"
	oddTagsCacheTTL  = 30 * time.Second
	oddTagsMaxPerLvl = 2000
)

// oddTagsData — уровень -> тег -> что этот тег значит. Описание хранится
// рядом с тегом, потому что показывается в подсказке при наведении.
//
// Три уровня:
//
//	yellow — жёлтая подсветка, «фетиш, но нормально»;
//	red    — красная подсветка, «супер странный»;
//	note   — без цвета, просто объяснение тега.
type oddTagsData struct {
	Yellow map[string]string `json:"yellow"`
	Red    map[string]string `json:"red"`
	Note   map[string]string `json:"note"`
}

func (d oddTagsData) total() int { return len(d.Yellow) + len(d.Red) + len(d.Note) }

// levels — все уровни по порядку отображения. Порядок важен при конфликте:
// тег может случайно попасть в два раздела, и тогда побеждает более «строгий».
func (d oddTagsData) levels() []map[string]string {
	return []map[string]string{d.Yellow, d.Red, d.Note}
}

var oddTagsCache = struct {
	sync.Mutex
	data     oddTagsData
	loadedAt time.Time
	primed   bool
}{}

// normalizeOddTag приводит тег к каноничному виду: нижний регистр, пробелы
// и дефисы -> подчёркивания. Пользователь пишет «gender bender», booru хранит
// «gender_bender» — без нормализации половина списка молча не сработает.
func normalizeOddTag(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.ReplaceAll(t, " ", "_")
	t = strings.ReplaceAll(t, "-", "_")
	return t
}

// loadOddTags читает data/odd_tags.json. Если файла нет — берём встроенный
// список и создаём рабочий файл, чтобы пользователь сразу видел, что и где
// править. Кривой JSON не роняет сервер: подсветка просто отключается.
func loadOddTags() oddTagsData {
	var raw map[string]json.RawMessage

	for _, p := range []string{filepath.Join("data", oddTagsFile), oddTagsFile} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, &raw); err != nil {
			log.Printf("[odd-tags] %s: невалидный JSON (%v) — странные теги отключены", p, err)
			return oddTagsData{}
		}
		d := buildOddTags(raw)
		// Старый файл — просто список тегов, без объяснений. Молча оставлять
		// его как есть нельзя: подсветка работала бы, но подсказка объясняла
		// бы не тег, а уровень («все красные теги — вот это»). Дописываем
		// описания из встроенного списка и переписываем файл на диске.
		if oddTagsIsLegacy(raw) {
			d = d.withDefaultDescriptions()
			if err := writeOddTagsFile(p, d); err != nil {
				log.Printf("[odd-tags] не удалось обновить %s: %v", p, err)
			} else {
				log.Printf("[odd-tags] %s обновлён: добавлены описания тегов", p)
			}
		}
		return d
	}

	// Своего файла нет: стартуем со встроенного списка и создаём рабочий файл.
	if err := json.Unmarshal(oddTagsDefault, &raw); err != nil {
		log.Printf("[odd-tags] встроенный список повреждён: %v", err)
		return oddTagsData{}
	}
	out := buildOddTags(raw)
	if err := writeOddTagsFile(filepath.Join("data", oddTagsFile), out); err != nil {
		log.Printf("[odd-tags] не удалось создать рабочий файл: %v", err)
	} else {
		log.Printf("[odd-tags] создан %s (%d тегов) — правьте его под себя", filepath.Join("data", oddTagsFile), out.total())
	}
	return out
}

// writeOddTagsFile сохраняет список в каноничном виде: отсортированные теги
// с описаниями. Так файл после автоправки становится приятным для чтения.
func writeOddTagsFile(path string, d oddTagsData) error {
	body := map[string]any{
		"_comment": "Объяснения к тегам для подсказки при наведении. " +
			"Три уровня: yellow — жёлтая подсветка, red — красная подсветка, " +
			"note — без цвета, просто объяснение. " +
			"Ключ — тег, значение — текст подсказки. " +
			"Файл перечитывается каждые 30 секунд, перезапускать сервер не нужно.",
		"yellow": sortTagMap(d.Yellow),
		"red":    sortTagMap(d.Red),
		"note":   sortTagMap(d.Note),
	}
	bb, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(bb, '\n'), 0o644)
}

func sortTagMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// oddTagsIsLegacy — файл записан в старом виде, уровень это список тегов.
func oddTagsIsLegacy(raw map[string]json.RawMessage) bool {
	for _, name := range []string{"yellow", "red", "note"} {
		msg, ok := raw[name]
		if !ok {
			continue
		}
		var asList []string
		if json.Unmarshal(msg, &asList) == nil {
			return true
		}
	}
	return false
}

// defaultDescriptions — тег → описание из встроенного списка.
func defaultDescriptions() map[string]string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(oddTagsDefault, &raw); err != nil {
		return nil
	}
	d := buildOddTags(raw)
	out := make(map[string]string, d.total())
	for _, m := range d.levels() {
		for tag, desc := range m {
			out[tag] = desc
		}
	}
	return out
}

// withDefaultDescriptions дополняет список описаниями из встроенного списка.
// Теги, которых во встроенном списке нет, остаются без описания — клиент
// покажет для них общую подсказку уровня.
func (d oddTagsData) withDefaultDescriptions() oddTagsData {
	def := defaultDescriptions()
	fill := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := make(map[string]string, len(m))
		for tag := range m {
			out[tag] = def[tag]
		}
		return out
	}
	return oddTagsData{
		Yellow: fill(d.Yellow),
		Red:    fill(d.Red),
		Note:   fill(d.Note),
	}
}

// buildOddTags превращает сырой JSON в список. Ключи разбираем через
// RawMessage: кроме yellow/red в файле может быть "_comment" со строкой —
// жёсткая типизация на нём упала бы целиком, из-за чего одна невинная
// правка комментария гасила бы всю подсветку.
//
// Уровень принимается и объектом {"тег": "описание"}, и старым массивом
// ["тег"] — иначе правка чужого файла привела бы к пустой подсветке.
func buildOddTags(raw map[string]json.RawMessage) oddTagsData {
	level := func(name string) map[string]string {
		msg, ok := raw[name]
		if !ok {
			return nil
		}
		var asMap map[string]string
		if err := json.Unmarshal(msg, &asMap); err == nil {
			return normalizeLevel(asMap)
		}
		var asList []string
		if err := json.Unmarshal(msg, &asList); err == nil {
			// Список без описаний: подсветка работает, но подсказка будет
			// общей для всего уровня.
			m := make(map[string]string, len(asList))
			for _, t := range asList {
				m[t] = ""
			}
			return normalizeLevel(m)
		}
		log.Printf("[odd-tags] уровень %q: ожидался объект или список, значение пропущено", name)
		return nil
	}
	return oddTagsData{
		Yellow: level("yellow"),
		Red:    level("red"),
		Note:   level("note"),
	}
}

// normalizeLevel приводит теги к каноничному виду и убирает коллизии.
//
// Порядок обхода map в Go случайный, поэтому приводить ключи «как пойдёт» нельзя:
// в файле встречаются и «Furry», и «furry» — оба нормализуются в «furry», и
// без сортировки подпись у тега менялась бы от запуска к запуску. Поэтому
// сначала сортируем ключи, а при совпадении оставляем написание, которое уже
// каноническое (пользователь не ошибся в регистре).
func normalizeLevel(in map[string]string) map[string]string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(in))
	canonical := make(map[string]bool, len(in)) // каким ключом заняли слот
	for _, t := range keys {
		n := normalizeOddTag(t)
		// Служебные ключи с подчёркиванием — комментарии в JSON, не теги.
		if n == "" || strings.HasPrefix(n, "_") {
			continue
		}
		if len(out) >= oddTagsMaxPerLvl {
			break
		}
		desc := strings.TrimSpace(in[t])
		prev, exists := out[n]
		if !exists {
			out[n] = desc
			canonical[n] = n == t
			continue
		}
		// Коллизия. Заменяем накопленное, если оно неканоническое, а текущее
		// каноническое («Tomboy» не должен вытеснять «tomboy»), либо если
		// накопленное пустое, а текущее с описанием.
		if (!canonical[n] && n == t) || (prev == "" && desc != "") {
			out[n] = desc
			canonical[n] = n == t
		}
	}
	return out
}

// getOddTags отдаёт список с кэшем на TTL: файл маленький, но дёргать его
// на каждый пост незачем.
func getOddTags() oddTagsData {
	oddTagsCache.Lock()
	defer oddTagsCache.Unlock()
	if oddTagsCache.primed && time.Since(oddTagsCache.loadedAt) < oddTagsCacheTTL {
		return oddTagsCache.data
	}
	oddTagsCache.data = loadOddTags()
	oddTagsCache.loadedAt = time.Now()
	oddTagsCache.primed = true
	return oddTagsCache.data
}

// OddTags — объяснения к тегам для клиента, по уровням.
//
// Три уровня: yellow и red получают цветовую подсветку, note — только
// текст подсказки. В подсказке показывается сам текст описания, без
// префиксов вида «Странный —»: пользователь попросил объяснение, а не ярлык.
func (h *Handler) OddTags(c *gin.Context) {
	d := getOddTags()
	empty := func() map[string]string { return map[string]string{} }
	if d.Yellow == nil {
		d.Yellow = empty()
	}
	if d.Red == nil {
		d.Red = empty()
	}
	if d.Note == nil {
		d.Note = empty()
	}
	c.JSON(200, d)
}

// sortedOddKeys — для логов и тестов: ключи map идут случайно, нужен порядок.
func sortedOddKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
