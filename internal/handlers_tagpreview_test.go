package internal

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// setupTagPreviewRouter поднимает роутер с изолированной data/ и наполняет
// БД постами с тегами, чтобы проверить счётчик, обложки и смежные теги.
func setupTagPreviewRouter(t *testing.T) (*httptest.Server, *PostDB) {
	return setupTagPreviewRouterProv(t, nil)
}

// setupTagPreviewRouterProv — то же, но с источником: превью тега при пустой
// библиотеке ходит на бор, и без заглушки тест ушёл бы в сеть.
func setupTagPreviewRouterProv(t *testing.T, prov Provider) (*httptest.Server, *PostDB) {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)
	gin.SetMode(gin.TestMode)
	db := NewPostDB("data/posts.db")
	t.Cleanup(func() {
		db.Close()
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})

	// Библиотека из 20 постов: 1girl есть везде (значит, как сосед он и
	// отсеется фильтром «везде», это правильно), blue_hair — в 6 постах с
	// 1girl и ещё в одном БЕЗ него (частота 7, связь слабее), smile — в 5,
	// solo — в 4, long_hair — всего в одном (шум, должен отпасть).
	const n = 20
	tagsFor := func(i int) string {
		base := "1girl"
		switch {
		case i <= 6:
			base += " blue_hair"
		case i == 7:
			base = "blue_hair" // без 1girl — вот и делает его частоту выше связи
		case i <= 12:
			base += " smile"
		case i <= 16:
			base += " solo"
		}
		if i == 3 {
			base += " long_hair"
		}
		return base
	}
	for i := 1; i <= n; i++ {
		db.UpsertMeta(&Post{ID: i, Tags: tagsFor(i), FileURL: "https://x/" + strconv.Itoa(i) + ".jpg"})
	}
	// Часть постов скачана — только у них будут обложки.
	for _, id := range []int{1, 2, 3, 5, 17, 20} {
		db.SetDownloaded(id, "data/save/"+strconv.Itoa(id)+".jpg", "thumbs/"+strconv.Itoa(id)+".jpg")
	}

	h := NewHandler()
	if prov != nil {
		// Кладём провайдера и под дефолтным именем: h.provider() смотрит
		// настройку из конфига, а в тестах важно только, чтобы нашёлся наш.
		h.providers = map[string]Provider{prov.Name(): prov, defaultProviderName: prov}
	}
	r := gin.New()
	api := r.Group("/api")
	api.GET("/tags/:tag/preview", h.TagPreview)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, db
}

func fetchTagPreview(t *testing.T, ts *httptest.Server, path string) (TagPreview, int) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var out TagPreview
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out, resp.StatusCode
}

func TestTagPreviewCountAndCovers(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, code := fetchTagPreview(t, ts, "/api/tags/1girl/preview")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	// 1girl есть у 19 постов из 20 (пост 7 у нас только с blue_hair).
	if got.Count != 19 {
		t.Errorf("count=%d, want 19", got.Count)
	}
	// Обложки только у скачанных: 17, 20, 5, 3, 2, 1 — шесть, по id по убыванию.
	if len(got.Posts) != 6 {
		t.Fatalf("covers=%d, want 6", len(got.Posts))
	}
	if got.Posts[0].ID != 20 {
		t.Errorf("newest first expected, got %d", got.Posts[0].ID)
	}
	if got.Posts[0].Thumb != "/api/thumb/20" {
		t.Errorf("thumb url=%q", got.Posts[0].Thumb)
	}
}

func TestTagPreviewUnknownTagIsEmptyNot404(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, code := fetchTagPreview(t, ts, "/api/tags/nosuchtag/preview")
	if code != http.StatusOK {
		t.Fatalf("unknown tag must be 200, got %d", code)
	}
	if got.Count != 0 || len(got.Posts) != 0 {
		t.Errorf("want empty, got %+v", got)
	}
}

func TestTagPreviewNormalizesTagName(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	// С пробелами и в верхнем регистре, как это бывает в интерфейсе.
	got, code := fetchTagPreview(t, ts, "/api/tags/Blue+Hair/preview")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	// blue_hair есть в постах 1–6 плюс пост 7 (где он без 1girl).
	if got.Count != 7 {
		t.Errorf("normalized count=%d, want 7", got.Count)
	}
	if got.Tag != "blue_hair" {
		t.Errorf("normalized tag=%q", got.Tag)
	}
}

// Смежные теги: важно отсечь «популярное, но не связанное». long_hair
// встречается в одном посте — попасть не должен, blue_hair в трёх — должен.
// Смежные теги: отсекаем «везде» (1girl — во всех 20 постах, как сосед он
// бесполезен) и шум из одного поста (long_hair). Остальное — по числу
// совместных постов: blue_hair(6), smile(5), solo(4).
func TestTagPreviewRelatedTagsRanking(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, _ := fetchTagPreview(t, ts, "/api/tags/1girl/preview?related=10")
	names := make([]string, 0, len(got.Related))
	for _, r := range got.Related {
		names = append(names, r.Tag)
	}
	if len(names) == 0 {
		t.Fatal("expected related tags for 1girl")
	}
	for _, n := range names {
		if n == "long_hair" {
			t.Errorf("single-post match must be dropped: %v", names)
		}
		if n == "1girl" {
			t.Errorf("the tag itself must not be a neighbour: %v", names)
		}
	}
	want := []string{"blue_hair", "smile", "solo"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("related=%v, want %v", names, want)
	}
}

// ── Превью с источника, когда в библиотеке по тегу пусто ─────────────────────

// stubTagProvider — источник с заранее заданной выдачей и счётчиком запросов.
type stubTagProvider struct {
	Provider
	name  string
	posts []Rule34Post
	err   error
	calls int
}

func (s *stubTagProvider) Name() string        { return s.name }
func (s *stubTagProvider) DisplayName() string { return s.name }

func (s *stubTagProvider) SearchPosts(tags string, page, limit, minID int) ([]Rule34Post, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if limit > len(s.posts) {
		limit = len(s.posts)
	}
	return s.posts[:limit], nil
}

func TestTagPreviewFallsBackToSourceWhenLibraryEmpty(t *testing.T) {
	sp := &stubTagProvider{name: "rule34", posts: []Rule34Post{
		{ID: 101, Tags: "cloudy_sky solo", FileURL: "https://i/101.jpg",
			PreviewURL: "https://i/101.jpg", Width: 800, Height: 600, FileType: "image/jpeg"},
		// Без превью и сэмпла: в сетку ему нечего показывать, но в БД строка
		// всё равно полезна — по клику вьюеру нужны теги и файл.
		{ID: 102, Tags: "cloudy_sky", FileURL: "https://i/102.jpg", Width: 600, Height: 800},
		// Только сэмпл: им как обложку показать можно.
		{ID: 103, Tags: "cloudy_sky", FileURL: "https://i/103.jpg",
			SampleURL: "https://i/103s.jpg", Width: 100, Height: 100},
	}}
	ts, db := setupTagPreviewRouterProv(t, sp)
	got, code := fetchTagPreview(t, ts, "/api/tags/cloudy_sky/preview")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if got.Count != 0 || len(got.Posts) != 0 {
		t.Fatalf("библиотека должна остаться пустой, получили %+v", got)
	}
	if got.Source == nil {
		t.Fatal("ожидался блок source с превью с бору")
	}
	if got.Source.Site != "rule34" || got.Source.Query != "cloudy_sky" {
		t.Errorf("site/query=%q/%q", got.Source.Site, got.Source.Query)
	}
	// 102 без превью в сетку не попадает — показывать нечего.
	if len(got.Source.Posts) != 2 || got.Source.Count != 2 {
		t.Fatalf("source covers=%d count=%d, want 2/2", len(got.Source.Posts), got.Source.Count)
	}
	first := got.Source.Posts[0]
	if !strings.HasPrefix(first.Thumb, "/api/proxy?url=") || !strings.Contains(first.Thumb, "kind=preview") {
		t.Errorf("обложка должна идти через прокси, получили %q", first.Thumb)
	}
	if !strings.Contains(first.Thumb, "101.jpg") {
		t.Errorf("в url обложки нет превью поста: %q", first.Thumb)
	}
	// Честная пометка: это не скачанные посты.
	if first.Downloaded || first.Site != "rule34" {
		t.Errorf("обложка помечена как скачанная: %+v", first)
	}
	if sp.calls != 1 {
		t.Errorf("источник опрошен %d раз, want 1", sp.calls)
	}
	// Метаданные в БД: иначе по клику пост не откроется (пост-by-ids смотрит
	// в базу) и вьюер не покажет теги.
	rows := db.GetMany([]int{101, 102, 103})
	if len(rows) != 3 {
		t.Fatalf("в БД попало %d постов из 3", len(rows))
	}
	if rows[101] == nil || rows[101].PreviewURL != "https://i/101.jpg" {
		t.Errorf("пост 101 без превью в БД: %+v", rows[101])
	}
	if rows[101] == nil || !strings.Contains(rows[101].Tags, "cloudy_sky") {
		t.Errorf("теги не сохранились: %+v", rows[101])
	}
}

func TestTagPreviewSkipsSourceWhenLibraryHasCovers(t *testing.T) {
	sp := &stubTagProvider{name: "rule34", posts: []Rule34Post{{ID: 201, PreviewURL: "https://i/201.jpg"}}}
	ts, _ := setupTagPreviewRouterProv(t, sp)
	got, _ := fetchTagPreview(t, ts, "/api/tags/blue_hair/preview")
	// blue_hair есть у скачанных постов 1, 2, 3, 5 — библиотеку показываем.
	if len(got.Posts) == 0 {
		t.Fatal("ожидались локальные обложки")
	}
	if sp.calls != 0 {
		t.Errorf("при непустой библиотеке источник трогать нельзя, вызовов %d", sp.calls)
	}
	if got.Source != nil {
		t.Errorf("source не нужен при непустой библиотеке: %+v", got.Source)
	}
}

func TestTagPreviewSourceDisabled(t *testing.T) {
	sp := &stubTagProvider{name: "rule34", posts: []Rule34Post{{ID: 301, PreviewURL: "https://i/301.jpg"}}}
	ts, _ := setupTagPreviewRouterProv(t, sp)
	got, code := fetchTagPreview(t, ts, "/api/tags/cloudy_sky/preview?source=0")
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if got.Source != nil || sp.calls != 0 {
		t.Errorf("source=0 обязан отключить поиск: source=%+v calls=%d", got.Source, sp.calls)
	}
}

// Сеть/лимит: лучше честное «в библиотеке пусто», чем ложное «на бору ничего
// не нашлось» — пользователь решил бы, что тега не существует.
func TestTagPreviewSourceErrorStaysSilent(t *testing.T) {
	sp := &stubTagProvider{name: "rule34", err: errors.New("timeout")}
	ts, _ := setupTagPreviewRouterProv(t, sp)
	got, code := fetchTagPreview(t, ts, "/api/tags/cloudy_sky/preview")
	if code != http.StatusOK {
		t.Fatalf("сбой источника не должен ломать превью: status=%d", code)
	}
	if got.Source != nil {
		t.Errorf("при ошибке источника блок source показывать нельзя: %+v", got.Source)
	}
	if sp.calls != 1 {
		t.Errorf("вызовов источника %d, want 1", sp.calls)
	}
}

func TestTagPreviewRelatedDisabled(t *testing.T) {
	ts, _ := setupTagPreviewRouter(t)
	got, _ := fetchTagPreview(t, ts, "/api/tags/1girl/preview?related=0")
	if len(got.Related) != 0 {
		t.Errorf("related=0 must return none, got %+v", got.Related)
	}
	if got.Count == 0 {
		t.Error("count must still be returned")
	}
}

// Превью с бору обязано уважать фильтр рейтинга: заглушка-источник запрос
// игнорирует, поэтому фильтрует наша локальная досчистка — ровно как в хендлере
// поиска, когда сайт обрезал хвост запроса.
func TestTagPreviewSourceRespectsRatingFilter(t *testing.T) {
	posts := []Rule34Post{
		{ID: 401, Rating: "explicit", PreviewURL: "https://i/401.jpg"},
		{ID: 402, Rating: "questionable", PreviewURL: "https://i/402.jpg"},
		{ID: 403, Rating: "general", PreviewURL: "https://i/403.jpg"},
	}
	ts, _ := setupTagPreviewRouterProv(t, &stubTagProvider{name: "rule34", posts: posts})
	got, _ := fetchTagPreview(t, ts, "/api/tags/x/preview?rating=sfw")
	if got.Source == nil {
		t.Fatal("ожидался source")
	}
	ids := make([]int, 0, len(got.Source.Posts))
	for _, p := range got.Source.Posts {
		ids = append(ids, p.ID)
	}
	if len(ids) != 1 || ids[0] != 403 {
		t.Errorf("sfw пропустил запрещённое: ids=%v, want [403]", ids)
	}

	// Без фильтра показываем всё, что вернул бор.
	ts2, _ := setupTagPreviewRouterProv(t, &stubTagProvider{name: "rule34", posts: posts})
	got2, _ := fetchTagPreview(t, ts2, "/api/tags/x/preview")
	if got2.Source == nil || len(got2.Source.Posts) != 3 {
		t.Errorf("без фильтра ждём все 3 поста, получили %+v", got2.Source)
	}
}
