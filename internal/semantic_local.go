package internal

import (
	"regexp"
	"strings"
	"unicode"
)

// semanticKeywordMap связывает естественные фразы (русский/английский) с тегами booru.
var semanticPhrases = []struct {
	phrase string
	tags   []string
}{
	// Сложные многословные конструкции
	{"кошка в шляпе", []string{"cat", "hat"}},
	{"котики в шляпах", []string{"cat", "hat"}},
	{"кот в шляпе", []string{"cat", "hat"}},
	{"рыжие волосы", []string{"red_hair"}},
	{"голубые глаза", []string{"blue_eyes"}},
	{"зеленые глаза", []string{"green_eyes"}},
	{"красные глаза", []string{"red_eyes"}},
	{"карие глаза", []string{"brown_eyes"}},
	{"желтые глаза", []string{"yellow_eyes"}},
	{"длинные волосы", []string{"long_hair"}},
	{"короткие волосы", []string{"short_hair"}},
	{"белые волосы", []string{"white_hair"}},
	{"черные волосы", []string{"black_hair"}},
	{"светлые волосы", []string{"blonde_hair"}},
	{"школьная форма", []string{"school_uniform"}},
	{"под дождем", []string{"rain", "outdoors"}},
	{"на пляже", []string{"beach", "outdoors"}},
	{"две девушки", []string{"2girls"}},
	{"две кошки", []string{"cat", "2girls"}},
	{"два парня", []string{"2boys"}},
	{"в очках", []string{"glasses"}},
	{"в шляпе", []string{"hat"}},
	{"в купальнике", []string{"swimsuit"}},
	{"в бикини", []string{"bikini"}},
	{"на кровати", []string{"on_bed", "indoors"}},
	{"на полу", []string{"on_floor"}},
	{"на улице", []string{"outdoors"}},
	{"в комнате", []string{"indoors"}},
	{"кошачьи ушки", []string{"cat_ears"}},
	{"лисьи ушки", []string{"fox_ears"}},
	{"кроличьи ушки", []string{"bunny_ears"}},
}

var semanticWordMap = map[string][]string{
	// Персонажи и сущности
	"девушка":      {"1girl"},
	"девочка":      {"1girl"},
	"тян":          {"1girl"},
	"парень":       {"1boy"},
	"мальчик":      {"1boy"},
	"кун":          {"1boy"},
	"кошка":        {"cat"},
	"кот":          {"cat"},
	"котик":        {"cat"},
	"котики":       {"cat"},
	"кошкодевочка": {"cat_ears", "1girl"},
	"нэко":         {"cat_ears", "1girl"},
	"неко":         {"cat_ears", "1girl"},
	"кицунэ":       {"fox_ears", "1girl"},
	"горничная":    {"maid"},
	"служанка":     {"maid"},
	"мейд":         {"maid"},
	"медсестра":    {"nurse"},
	"эльф":         {"elf"},
	"ангел":        {"angel", "wings"},
	"демон":        {"demon", "horns"},

	// Одежда и аксессуары
	"купальник": {"swimsuit"},
	"бикини":    {"bikini"},
	"шляпа":     {"hat"},
	"очки":      {"glasses"},
	"чулки":     {"stockings"},
	"гетры":     {"thighhighs"},
	"кимоно":    {"kimono"},
	"юката":     {"yukata"},
	"лента":     {"ribbon"},
	"бант":      {"ribbon"},
	"хвост":     {"tail"},
	"крылья":    {"wings"},

	// Эмоции и действия
	"улыбка":    {"smile"},
	"улыбается": {"smile"},
	"смущение":  {"blush"},
	"румянец":   {"blush"},
	"плачет":    {"crying"},
	"спит":      {"sleeping"},
	"сидит":     {"sitting"},
	"стоит":     {"standing"},
	"лежит":     {"lying_down"},

	// Окружение
	"пляж":    {"beach", "outdoors"},
	"море":    {"ocean", "outdoors"},
	"океан":   {"ocean"},
	"дождь":   {"rain"},
	"зонт":    {"umbrella"},
	"зонтик":  {"umbrella"},
	"ночь":    {"night"},
	"луна":    {"moon"},
	"солнце":  {"sunlight"},
	"закат":   {"sunset"},
	"лес":     {"forest"},
	"комната": {"indoors"},
	"кровать": {"bed"},
	"небо":    {"sky"},
	"облака":  {"clouds"},

	// Английские базовые слова
	"1girl":      {"1girl"},
	"2girls":     {"2girls"},
	"1boy":       {"1boy"},
	"2boys":      {"2boys"},
	"solo":       {"solo"},
	"girl":       {"1girl"},
	"boy":        {"1boy"},
	"cat":        {"cat"},
	"cats":       {"cat"},
	"catgirl":    {"cat_ears", "1girl"},
	"neko":       {"cat_ears", "1girl"},
	"maid":       {"maid"},
	"beach":      {"beach"},
	"bikini":     {"bikini"},
	"swimsuit":   {"swimsuit"},
	"glasses":    {"glasses"},
	"hat":        {"hat"},
	"rain":       {"rain"},
	"umbrella":   {"umbrella"},
	"smile":      {"smile"},
	"blush":      {"blush"},
	"thighhighs": {"thighhighs"},
	"stockings":  {"stockings"},
	"wings":      {"wings"},
	"angel":      {"angel"},
}

var nonAlphanumericRe = regexp.MustCompile(`[^\p{L}\p{N}\s_]+`)

// LocalSemanticTranslate выполняет быстрый локальный разбор естественно-языковой фразы
// на соответствующие booru-теги без обращения к внешним нейросетям (Ollama).
func LocalSemanticTranslate(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	lower = nonAlphanumericRe.ReplaceAllString(lower, " ")

	tagSet := make(map[string]bool)
	var orderedTags []string
	addTag := func(t string) {
		t = strings.TrimSpace(t)
		if t != "" && !tagSet[t] {
			tagSet[t] = true
			orderedTags = append(orderedTags, t)
		}
	}

	// 1. Поиск составных фраз
	remaining := lower
	for _, sp := range semanticPhrases {
		if strings.Contains(remaining, sp.phrase) {
			for _, t := range sp.tags {
				addTag(t)
			}
			remaining = strings.ReplaceAll(remaining, sp.phrase, " ")
		}
	}

	// 2. Поиск отдельных слов
	words := strings.Fields(remaining)
	for _, w := range words {
		// Очистка от знаков препинания
		w = strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if w == "" {
			continue
		}
		if tags, ok := semanticWordMap[w]; ok {
			for _, t := range tags {
				addTag(t)
			}
		}
	}

	if len(orderedTags) == 0 {
		return ""
	}

	return strings.Join(orderedTags, " ")
}
