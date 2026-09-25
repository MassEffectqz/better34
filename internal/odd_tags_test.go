package internal

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Теги нормализуются: пользователь пишет «Gender Bender» / «gender-bender»,
// booru хранит «gender_bender». Без нормализации запись молча не сработает.
// Хендлер обязан отдавать описание каждого тега: клиент рисует его в
// подсказке при наведении. Если бы ушёл голый список тегов, подсветка
// работала бы, но объяснять ничего не объясняла бы.
func TestOddTagsHandlerSendsDescriptions(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"yellow":{"tomboy":"Девушка с мальчишеской внешностью"},"red":{"furry":"Человекоподобное существо"},"note":{"solo":"В кадре один персонаж"}}`
	if err := os.WriteFile(filepath.Join("data", oddTagsFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Сбрасываем кэш: тесты меняют рабочий каталог, а список кэшируется.
	oddTagsCache.Lock()
	oddTagsCache.primed = false
	oddTagsCache.Unlock()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/odd-tags", nil)

	(&Handler{}).OddTags(c)

	if w.Code != 200 {
		t.Fatalf("код = %d, ожидался 200", w.Code)
	}
	var out struct {
		Yellow map[string]string `json:"yellow"`
		Red    map[string]string `json:"red"`
		Note   map[string]string `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("ответ не разобран: %v (%s)", err, w.Body.String())
	}
	if got := out.Red["furry"]; got != "Человекоподобное существо" {
		t.Errorf("описание furry не дошло до клиента: %q", got)
	}
	if got := out.Yellow["tomboy"]; got != "Девушка с мальчишеской внешностью" {
		t.Errorf("описание tomboy не дошло до клиента: %q", got)
	}
	// Третий уровень обязан доехать: это «просто объяснение», без цвета.
	if got := out.Note["solo"]; got != "В кадре один персонаж" {
		t.Errorf("уровень note не дошёл до клиента: %q", got)
	}
}

// Старый файл (список без описаний) обязан обновляться на лету: иначе
// подсветка работает, но подсказка объясняет не тег, а уровень — ровно тот
// случай, из-за которого правка «метка вместо объяснения» и не помогла.
func TestLoadOddTagsMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("data", oddTagsFile)
	// Старый формат: уровень — просто список тегов.
	if err := os.WriteFile(path, []byte(`{"yellow":["tomboy"],"red":["furry"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	d := loadOddTags()
	if got := d.Red["furry"]; got == "" {
		t.Errorf("после миграции у furry нет описания (%q)", got)
	}
	if got := d.Yellow["tomboy"]; got == "" {
		t.Errorf("после миграции у tomboy нет описания (%q)", got)
	}

	// Файл на диске тоже должен стать новым форматом — иначе правка
	// повторится при каждом перезапуске.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("перезаписанный файл невалиден: %v", err)
	}
	if oddTagsIsLegacy(raw) {
		t.Error("файл на диске остался в старом формате")
	}
	var red map[string]string
	if err := json.Unmarshal(raw["red"], &red); err != nil {
		t.Fatalf("уровень red не стал объектом: %v", err)
	}
	if red["furry"] == "" {
		t.Error("в файле на диске у furry нет описания")
	}
}

// Повторная загрузка не должна считать файл старым и переписывать его снова.
func TestLoadOddTagsDoesNotRewriteModernFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("data", oddTagsFile)
	body := `{"yellow":{"tomboy":"моё описание"},"red":{"furry":"звериная раса"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d := loadOddTags()
	if got := d.Yellow["tomboy"]; got != "моё описание" {
		t.Errorf("описание пользователя затёрто: %q", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("современный файл перезаписан заново — правки пользователя могут потеряться")
	}
}

// Регрессия на недетерминированность: порядок обхода map в Go случаен, и
// теги «Furry»/«furry» нормализуются в один. Без сортировки подпись у тега
// менялась бы от запуска к запуску — тест падал примерно в 1 прогоне из 5.
func TestNormalizeLevelIsDeterministic(t *testing.T) {
	in := map[string]string{
		"Tomboy":   "неканонический ключ",
		"tomboy":   "канонический ключ",
		"FUTANARI": "заглавными",
		"futanari": "строчными",
	}
	first := normalizeLevel(in)
	for i := 0; i < 200; i++ {
		got := normalizeLevel(in)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("прогон %d дал другой результат: %v != %v", i, got, first)
		}
	}
	// Каноническое написание ключа должно выигрывать у неканонического.
	if first["tomboy"] != "канонический ключ" {
		t.Errorf("tomboy = %q, ожидалось описание канонического ключа", first["tomboy"])
	}
	if len(first) != 2 {
		t.Errorf("ожидалось 2 тега после слияния регистров, получено %d: %v", len(first), first)
	}
}

func TestNormalizeOddTag(t *testing.T) {
	cases := map[string]string{
		"gender bender": "gender_bender",
		"gender-bender": "gender_bender",
		"  Furry  ":     "furry",
		"two_boys":      "two_boys",
		"":              "",
	}
	for in, want := range cases {
		if got := normalizeOddTag(in); got != want {
			t.Errorf("normalizeOddTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// Комментарии в JSON (ключи с подчёркиванием) не должны попадать в список
// как теги, иначе появится подсветка тега «_comment».
func TestBuildOddTagsSkipsCommentsAndDuplicates(t *testing.T) {
	d := buildOddTags(map[string]json.RawMessage{
		"yellow": json.RawMessage(`{"tomboy":"мужеподобная девушка","TOMBOY":"дубль"," Gender Bender ":"смена пола","_note":"комментарий","":"пусто"}`),
		"red":    json.RawMessage(`{"furry":"звериная раса","futa":"мужское тело"}`),
	})
	if got := d.Yellow["tomboy"]; got != "мужеподобная девушка" {
		t.Errorf("описание tomboy = %q", got)
	}
	if _, ok := d.Yellow["_note"]; ok {
		t.Errorf("комментарий попал в список тегов: %v", d.Yellow)
	}
	if _, ok := d.Yellow[""]; ok {
		t.Errorf("пустой тег попал в список: %v", d.Yellow)
	}
	if got := d.Yellow["gender_bender"]; got != "смена пола" {
		t.Errorf("пробел в теге не нормализован: %q", got)
	}
	if len(d.Yellow) != 2 {
		t.Errorf("дубликаты/мусор не убраны: %v", sortedOddKeys(d.Yellow))
	}
	if len(d.Red) != 2 {
		t.Errorf("red = %v, ожидалось 2 тега", sortedOddKeys(d.Red))
	}
}

// Старый формат со списком без описаний обязан продолжать работать: иначе
// правка чужого файла тихо погасила бы всю подсветку.
func TestBuildOddTagsAcceptsLegacyList(t *testing.T) {
	d := buildOddTags(map[string]json.RawMessage{
		"yellow": json.RawMessage(`["tomboy","crempay"]`),
		"red":    json.RawMessage(`["furry"]`),
	})
	if len(d.Red) != 1 || d.Red["furry"] != "" {
		t.Errorf("старый формат не разобран: %v", d.Red)
	}
	if len(d.Yellow) != 2 {
		t.Errorf("старый формат yellow не разобран: %v", d.Yellow)
	}
}

// Каждый тег во встроенном списке обязан иметь описание: подсказка без него
// бесполезна — это была причина правки «метка, а не объяснение».
func TestOddTagsDefaultEveryTagHasDescription(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(oddTagsDefault, &raw); err != nil {
		t.Fatalf("встроенный список невалидный JSON: %v", err)
	}
	d := buildOddTags(raw)
	all := append(sortedOddKeys(d.Yellow), sortedOddKeys(d.Red)...)
	all = append(all, sortedOddKeys(d.Note)...)
	if len(all) == 0 {
		t.Fatal("встроенный список пуст")
	}
	// Проверяем только наличие текста, а не длину: пользователь дописывает
	// описания сам, и короткие вроде «Моча» или «Роды» вполне годятся.
	for lvl, m := range map[string]map[string]string{"yellow": d.Yellow, "red": d.Red, "note": d.Note} {
		for tag, desc := range m {
			if strings.TrimSpace(desc) == "" {
				t.Errorf("тег %q (%s) без описания", tag, lvl)
			}
		}
	}
}

// Список читается из data/odd_tags.json, и правки подхватываются.
func TestLoadOddTagsFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"yellow":{"tomboy":"мужеподобная девушка"},"red":{"furry":"звериная раса","futa":"мужское тело"}}`
	if err := os.WriteFile(filepath.Join("data", oddTagsFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d := loadOddTags()
	if got := d.Yellow["tomboy"]; got != "мужеподобная девушка" {
		t.Errorf("описание из файла не прочитано: %q (%v)", got, d.Yellow)
	}
	if len(d.Red) != 2 {
		t.Errorf("red = %v, ожидалось 2 тега", sortedOddKeys(d.Red))
	}
}

// Кривой JSON не должен ронять сервер: подсветка просто отключается.
func TestLoadOddTagsBrokenJSONDegrades(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", oddTagsFile), []byte(`{это не json`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := loadOddTags()
	if len(d.Yellow) != 0 || len(d.Red) != 0 || len(d.Note) != 0 {
		t.Errorf("битый файл должен давать пустой список, получено %+v", d)
	}
}

// Если файла нет, он создаётся из встроенного списка — иначе подсветка не
// работала бы «из коробки» и пользователь не понял бы, что список вообще
// нужен и где его править.
func TestLoadOddTagsCreatesFileFromDefault(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	d := loadOddTags()
	if len(d.Yellow) == 0 || len(d.Red) == 0 || len(d.Note) == 0 {
		t.Fatalf("встроенный список не загрузился: %+v", d)
	}
	if _, err := os.Stat(filepath.Join("data", oddTagsFile)); err != nil {
		t.Errorf("рабочий файл не создан: %v", err)
	}
}

// Встроенный список обязан быть валидным JSON и содержать оба уровня с
// непустым списком — это то, с чего пользователь начинает правку.
func TestOddTagsDefaultIsValid(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(oddTagsDefault, &raw); err != nil {
		t.Fatalf("встроенный список невалидный JSON: %v", err)
	}
	d := buildOddTags(raw)
	if len(d.Yellow) == 0 {
		t.Error("в списке пустой уровень yellow")
	}
	if len(d.Red) == 0 {
		t.Error("в списке пустой уровень red")
	}
	if len(d.Note) == 0 {
		t.Error("в списке пустой уровень note (простое объяснение)")
	}
	// Базовые теги из исходного списка пользователя должны быть на месте.
	has := func(m map[string]string, s string) bool {
		_, ok := m[s]
		return ok
	}
	for _, want := range []string{"tomboy", "crempay", "femboy", "pregnant"} {
		if !has(d.Yellow, want) {
			t.Errorf("в списке нет жёлтого тега %q", want)
		}
	}
	for _, want := range []string{"furry", "futanari", "inflation", "gynomorph", "yaoi", "trap"} {
		if !has(d.Red, want) {
			t.Errorf("в списке нет красного тега %q", want)
		}
	}
}
