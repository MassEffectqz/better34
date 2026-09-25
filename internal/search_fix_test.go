package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// GetTagCount должен возвращать счётчик именно запрошенного тега:
// fuzzy-автодополнение возвращает и похожие теги, и раньше при отсутствии
// точного совпадения приписывался счётчик первого «похожего».
// ── Точность подсказок ────────────────────────────────────────────────────

// Подсказки по «-brea» должны находиться: служебный минус — часть ввода,
// а не имени тега. Раньше префикс уходил в апстрим как есть, и префиксная
// фильтрация отбрасывала всё.
func TestSuggestTagsStripsOperatorPrefix(t *testing.T) {
	var gotQ string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		w.Write([]byte(`[{"label":"breasts","value":"breasts","count":10}]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	wireTestClient(t, cl, srv)

	got, err := cl.SuggestTags("-brea")
	if err != nil {
		t.Fatalf("SuggestTags: %v", err)
	}
	if gotQ != "brea" {
		t.Errorf("upstream q = %q, want %q (operator prefix must be stripped)", gotQ, "brea")
	}
	if len(got) != 1 || got[0].Value != "breasts" {
		t.Fatalf("unexpected suggestions for -brea: %+v", got)
	}
}

// Fuzzy-апстрим возвращает «похожие» теги — в выдаче должны остаться только
// реально начинающиеся с префикса, а точное совпадение идёт первым.
func TestFilterSuggestionsByPrefixExactFirst(t *testing.T) {
	in := []TagSuggestion{
		{Label: "breasts_large", Value: "breasts_large", Count: 900},
		{Label: "breast", Value: "breast", Count: 5},
		{Label: "huge_breasts", Value: "huge_breasts", Count: 800},
		{Label: "breast", Value: "breast", Count: 5}, // дубль
		{Label: "", Value: "", Count: 1},             // пустое значение
	}
	got := filterSuggestionsByPrefix(in, "breast")
	// huge_breasts больше не отбрасывается: это вхождение внутри имени (tier 3),
	// оно уходит вниз, а не пропадает. Мусор без вхождения префикса — по-прежнему нет.
	if len(got) != 3 {
		t.Fatalf("want 3 matches, got %d: %+v", len(got), got)
	}
	if got[0].Value != "breast" {
		t.Errorf("exact match must be first, got %q", got[0].Value)
	}
	if got[1].Value != "breasts_large" {
		t.Errorf("prefix match must be second, got %+v", got)
	}
	if got[2].Value != "huge_breasts" {
		t.Errorf("mid-word match must be last, got %+v", got)
	}
}

// Тег без вхождения префикса (undefined_fantastic_object по «bocchi»)
// отбрасывается на любом tier — это защита от мусора fuzzy-апстрима.
func TestFilterSuggestionsByPrefixDropsNonMatching(t *testing.T) {
	in := []TagSuggestion{
		{Label: "bocchi_the_rock", Value: "bocchi_the_rock", Count: 31339},
		{Label: "undefined_fantastic_object", Value: "undefined_fantastic_object", Count: 837},
		{Label: "unrelated", Value: "unrelated", Count: 9999},
	}
	got := filterSuggestionsByPrefix(in, "bocchi")
	if len(got) != 1 || got[0].Value != "bocchi_the_rock" {
		t.Fatalf("only real matches must survive, got %+v", got)
	}
}

// Граница слова (tier 2) важнее вхождения в середину имени (tier 3) при
// равной популярности: solo_breasts релевантнее, чем склеенный hugebreasts.
func TestFilterSuggestionsByPrefixWordBoundaryTier(t *testing.T) {
	in := []TagSuggestion{
		{Label: "hugebreasts", Value: "hugebreasts", Count: 500}, // склейка, tier 3
		{Label: "solo_breasts", Value: "solo_breasts", Count: 500},
		{Label: "breasts", Value: "breasts", Count: 1},
	}
	got := filterSuggestionsByPrefix(in, "breast")
	want := []string{"breasts", "solo_breasts", "hugebreasts"}
	for i, w := range want {
		if i >= len(got) || got[i].Value != w {
			t.Fatalf("order = %v, want %v", values(got), want)
		}
	}
}

func TestSuggestionTier(t *testing.T) {
	cases := []struct {
		value, prefix string
		want          int
	}{
		{"breast", "breast", 0},
		{"breasts", "breast", 1},
		{"breasts", "", 0},
		{"solo_breasts", "breast", 2},  // префикс сразу после «_»
		{"huge_breasts", "breast", 2},  // «breasts» — тоже слово с этой буквы
		{"hugebreasts", "breast", 3},   // склейка: вхождение в середине слова
		{"solo_breasts", "breasts", 2}, // не префикс тега, но начало слова
		{"cat", "dog", -1},
	}
	for _, c := range cases {
		if got := suggestionTier(c.value, c.prefix); got != c.want {
			t.Errorf("suggestionTier(%q, %q) = %d, want %d", c.value, c.prefix, got, c.want)
		}
	}
}

func TestSuggestQueryPrefix(t *testing.T) {
	cases := map[string]string{
		"brea":     "brea",
		"-brea":    "brea",
		"~brea":    "brea",
		"+-~brea":  "brea",
		"  -brea ": "brea",
		"-":        "",
		"":         "",
	}
	for in, want := range cases {
		if got := suggestQueryPrefix(in); got != want {
			t.Errorf("suggestQueryPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// Подсказки отдаются по популярности: убывание счётчика, а не порядок
// «как прислал апстрим» (autocomplete.php сортировки не гарантирует).
// Точное совождение с префиксом при этом остаётся первым.
func TestFilterSuggestionsByPrefixSortsByPopularity(t *testing.T) {
	in := []TagSuggestion{
		{Label: "cat_ears", Value: "cat_ears", Count: 50},
		{Label: "cat_girl", Value: "cat_girl", Count: 90000},
		{Label: "catboy", Value: "catboy", Count: 8000},
		{Label: "cat", Value: "cat", Count: 7}, // точное, но самое редкое
	}
	got := filterSuggestionsByPrefix(in, "cat")
	want := []string{"cat", "cat_girl", "catboy", "cat_ears"}
	if len(got) != len(want) {
		t.Fatalf("got %d suggestions, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Value != w {
			t.Errorf("position %d = %q, want %q (order: %+v)", i, got[i].Value, w, got)
		}
	}
}

// Без точного совпадения порядок — строго по убыванию счётчика; при равных
// счётчиках сортировка детерминирована (короче, затем алфавит).
func TestSortSuggestionsByPopularityTieBreak(t *testing.T) {
	in := []TagSuggestion{
		{Label: "bb", Value: "bb", Count: 10},
		{Label: "a", Value: "a", Count: 10},
		{Label: "cccc", Value: "cccc", Count: 10},
		{Label: "z", Value: "z", Count: 99},
	}
	sortSuggestionsByRelevance(in, "q")
	want := []string{"z", "a", "bb", "cccc"}
	for i, w := range want {
		if in[i].Value != w {
			t.Fatalf("order = %v, want %v", values(in), want)
		}
	}
}

func values(s []TagSuggestion) []string {
	out := make([]string, len(s))
	for i, t := range s {
		out[i] = t.Value
	}
	return out
}

// Локальные подсказки не должны подмешивать теги чужого источника: теги
// разных боеру не взаимозаменяемы, поэтому в выдаче активного сайта такая
// подсказка была бы «неточной».
// Граница слова ищется шаблоном с ведущим «%», который не может использовать
// индекс по tags(tag) — запрос деградирует до сканa. Проверяем, что на
// реальном объёме скачанных постов это остаётся быстрым: автодополнение
// вызывается на каждый ввод символа и не должно подвисать.
func BenchmarkSuggestTagsLocalBoundary(b *testing.B) {
	db := NewPostDB(filepath.Join(b.TempDir(), "bench_suggest.db"))
	defer db.Close()
	// ~20k тегов: 2000 постов по 10 тегов.
	tags := make([]string, 0, 10)
	for i := 0; i < 2000; i++ {
		tags = tags[:0]
		for j := 0; j < 10; j++ {
			tags = append(tags, fmt.Sprintf("tag_%d_%d", i, j))
		}
		db.AddOrUpdate(&Post{ID: i + 1, Tags: strings.Join(tags, " "), Downloaded: true})
	}
	// b.Loop() вместо b.N: замеряет только тело цикла и не даёт компилятору
	// выбросить вызов (b.Loop доступен с Go 1.24, модуль на 1.25).
	for b.Loop() {
		db.SuggestTagsLocal("tag_100", 8)
	}
}

func TestSuggestTagsLocalForSource(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "src_suggest.db"))
	defer db.Close()
	db.AddOrUpdate(&Post{ID: 1, Tags: "cat_girl", Downloaded: true, Source: "rule34"})
	db.AddOrUpdate(&Post{ID: 2, Tags: "cat_ears", Downloaded: true, Source: "gelbooru"})

	only := db.SuggestTagsLocalFor("cat", 10, "rule34")
	if len(only) != 1 || only[0].Value != "cat_girl" {
		t.Fatalf("rule34 scope leaked other sources: %+v", only)
	}
	all := db.SuggestTagsLocalFor("cat", 10, "")
	if len(all) != 2 {
		t.Fatalf("empty source must mean all sources, got %+v", all)
	}
	// Обратная совместимость: старая сигнатура — это «все источники».
	if legacy := db.SuggestTagsLocal("cat", 10); len(legacy) != 2 {
		t.Fatalf("SuggestTagsLocal = %+v, want both tags", legacy)
	}
	// Посты, скачанные до появления колонки source, не должны исчезнуть.
	db.AddOrUpdate(&Post{ID: 3, Tags: "cat_legacy", Downloaded: true, Source: ""})
	withLegacy := db.SuggestTagsLocalFor("cat", 10, "rule34")
	if len(withLegacy) != 2 {
		t.Fatalf("legacy (empty source) posts must stay visible: %+v", withLegacy)
	}
}

// Локальные подсказки ищут не только по префиксу, но и по границе слова:
// solo_breasts находится по «breast». Раньше LIKE был «breast%», и такой тег
// в локальной выдаче просто не появлялся.
func TestSuggestTagsLocalWordBoundary(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "sugg_boundary.db"))
	defer db.Close()
	db.AddOrUpdate(&Post{ID: 1, Tags: "solo_breasts breasts", Downloaded: true})

	got := db.SuggestTagsLocal("breast", 10)
	order := values(got)
	// Префиксный breasts (tier 1) выше solo_breasts (tier 2), даже при
	// равной частоте.
	if len(order) != 2 || order[0] != "breasts" || order[1] != "solo_breasts" {
		t.Fatalf("boundary match missing or misordered: %v", order)
	}
}

// Короткий префикс не должен приносить совпадения по границе слова:
// на «uma» это давало doma_umaru и himouto!_umaru-chan — теги, которые
// пользователь не набирал. Совпадения по границе включаются только от
// localBoundaryMinPrefix символов.
func TestSuggestTagsLocalShortPrefixNoBoundaryJunk(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "sugg_short.db"))
	defer db.Close()
	db.AddOrUpdate(&Post{ID: 1, Tags: "doma_umaru uma_girl umamusume", Downloaded: true})

	got := db.SuggestTagsLocal("uma", 10)
	for _, s := range got {
		if s.Value == "doma_umaru" {
			t.Errorf("короткий префикс не должен тянуть boundary-мусор: %+v", got)
		}
	}
	// Настоящие префиксные совпадения остаются.
	names := values(got)
	if len(names) != 2 {
		t.Fatalf("want uma_girl + umamusume, got %v", names)
	}
}

// Регрессия: tagindex запрашивал limit=20, и на коротком префиксе двадцатка
// самых популярных ПРОИЗВОДНЫХ тегов вытесняла базовый — umamusume по «umamu»
// не попадал в выдачу вообще (проверено живым запросом к gelbooru: при
// limit=20 его нет, при limit=100 он первый). Запрос должен уносить достаточно
// записей, чтобы базовый тег находился по начальным буквам.
func TestTagIndexSuggestLimitCoversBaseTag(t *testing.T) {
	var gotLimit, gotPattern string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Читаем параметры только ПРЕФИКСНОГО запроса: при отсутствии
		// базового тега SuggestTags дополнительно шлёт точный name=<tag>.
		if r.URL.Query().Get("name") == "" {
			gotLimit = r.URL.Query().Get("limit")
			gotPattern = r.URL.Query().Get("name_pattern")
		}
		w.Write([]byte(`{"@attributes":{},"tag":[{"name":"umamusume","count":213596}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	if _, err := c.SuggestTags("umamu"); err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if gotPattern != "umamu%" {
		t.Errorf("name_pattern = %q, want %q", gotPattern, "umamu%")
	}
	limit, err := strconv.Atoi(gotLimit)
	if err != nil {
		t.Fatalf("limit = %q, not a number", gotLimit)
	}
	if limit < 100 {
		t.Errorf("tagindex limit = %d: базовый тег вытесняется производными "+
			"(umamusume пропадал по короткому префиксу)", limit)
	}
}

// Регрессия: индекс тегов gelbooru по name_pattern=furry% не возвращает сам
// базовый тег «furry» (200 376 постов) — только производные furry_*. Точный
// запрос name=<tag> его отдаёт, поэтому SuggestTags обязан дособрать его и
// поставить первым. Проверено живыми запросами.
func TestSuggestTagsFetchesBaseTagWhenIndexOmitsIt(t *testing.T) {
	var nameCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("name") == "furry":
			// Точный запрос: базовый тег есть.
			nameCalls++
			w.Write([]byte(`{"@attributes":{},"tag":[{"name":"furry","count":200376}]}`))
		default:
			// Префиксный: только производные, базового нет.
			w.Write([]byte(`{"@attributes":{},"tag":[` +
				`{"name":"furry_breasts","count":4698918},` +
				`{"name":"furry_ears","count":1260528}]}`))
		}
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	sugg, err := c.SuggestTags("furry")
	if err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if nameCalls == 0 {
		t.Error("точный запрос name=furry не был выполнен")
	}
	if len(sugg) == 0 || sugg[0].Value != "furry" {
		t.Fatalf("базовый тег должен быть первым, got %+v", sugg)
	}
	if sugg[0].Count != 200376 {
		t.Errorf("счётчик базового тега = %d, want 200376", sugg[0].Count)
	}
	// Производные не потеряны.
	if len(sugg) != 3 {
		t.Errorf("want 3 suggestions (base + 2 derived), got %d: %+v", len(sugg), sugg)
	}
}

// Если базового тега нет на сайте, список не меняется и не падает.
func TestSuggestTagsNoBaseTagLeavesListIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "" {
			w.Write([]byte(`{"@attributes":{},"tag":[]}`))
			return
		}
		w.Write([]byte(`{"@attributes":{},"tag":[{"name":"furry_breasts","count":4698918}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	sugg, err := c.SuggestTags("furry")
	if err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if len(sugg) != 1 || sugg[0].Value != "furry_breasts" {
		t.Fatalf("несуществующий базовый тег не должен ломать выдачу: %+v", sugg)
	}
}

// Если базовый тег уже есть в выдаче, лишнего точного запроса не делаем.
func TestSuggestTagsSkipsExactLookupWhenPresent(t *testing.T) {
	var nameCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "" {
			nameCalls++
		}
		w.Write([]byte(`{"@attributes":{},"tag":[{"name":"furry","count":200376}]}`))
	}))
	defer srv.Close()

	c := newTestGelbooru(srv.URL)
	if _, err := c.SuggestTags("furry"); err != nil {
		t.Fatalf("SuggestTags failed: %v", err)
	}
	if nameCalls != 0 {
		t.Errorf("точный запрос не нужен, когда тег уже в выдаче (calls=%d)", nameCalls)
	}
}

func TestGetTagCountExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "autocomplete.php" {
			switch r.URL.Query().Get("q") {
			case "mytag":
				w.Write([]byte(`[{"label":"mytag","value":"mytag","count":42},{"label":"mytag2","value":"mytag2","count":999}]`))
			case "other":
				w.Write([]byte(`[{"label":"other_thing","value":"other_thing","count":7}]`))
			default:
				w.Write([]byte(`[]`))
			}
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewRule34Client()
	c.spec.apiURL = srv.URL + "/index.php"
	c.httpClient.Store(&http.Client{})
	c.cache = newBooruCache(filepath.Join(os.TempDir(), "r34_sc_tagcount_test.json"))
	c.suggMu.Lock()
	c.suggM = make(map[string]suggestionCacheEntry)
	c.suggMu.Unlock()
	c.suggBreaker.failures = 0
	c.suggBreaker.openUntil = time.Time{}
	seedTestKeys(c, []APICredential{{Name: "test", APIKey: "testkey", UserID: "1"}})

	n, err := c.GetTagCount("mytag")
	if err != nil {
		t.Fatalf("GetTagCount: %v", err)
	}
	if n != 42 {
		t.Fatalf("GetTagCount(mytag) = %d, want 42", n)
	}

	// Точного совпадения нет — раньше вернулся бы 7 от «other_thing».
	n, err = c.GetTagCount("other")
	if err != nil {
		t.Fatalf("GetTagCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("GetTagCount(other) = %d, want 0 (no exact match)", n)
	}
}
