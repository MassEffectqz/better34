package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestProfileSetLiked — массовый лайк: идемпотентность, снятие лайка
// снимает скрытие (то же правило, что у одиночного ToggleLike).
func TestProfileSetLiked(t *testing.T) {
	p := NewProfile(filepath.Join(t.TempDir(), "p.json"))

	if n := p.SetLiked([]int{1, 2, 3}, true); n != 3 {
		t.Fatalf("первый заход: ожидалось 3 изменения, got %d", n)
	}
	if n := p.SetLiked([]int{1, 2, 3}, true); n != 0 {
		t.Fatalf("повторный заход: ожидалось 0 изменений, got %d", n)
	}
	for _, id := range []int{1, 2, 3} {
		if !p.LikedPosts[id] {
			t.Fatalf("пост #%d не лайкнут", id)
		}
	}
	// Скрытие «чужих» постов не отменяется массовым лайком.
	if p.HiddenPosts[1] {
		t.Fatal("скрытий до теста быть не должно")
	}
	// Лайк снимает скрытие для того же поста.
	_ = p.SetHidden([]int{3}, true)
	if n := p.SetLiked([]int{3}, true); n != 1 {
		t.Fatalf("снятие скрытия уже лайкнутого поста — 1 изменение, got %d", n)
	}
	if p.HiddenPosts[3] {
		t.Fatal("массовый лайк не снял скрытие с того же поста")
	}
	// Снятие лайка.
	if n := p.SetLiked([]int{1, 2, 3}, false); n != 3 {
		t.Fatalf("снятие лайка: ожидалось 3 изменения, got %d", n)
	}
	if t0 := p.LikedAt[1]; t0 != 0 {
		t.Fatalf("LikedAt не очищен: %d", t0)
	}
}

// TestProfileCollectionAddMany — массовое добавление в коллекцию пропускает
// дубликаты и не задевает другие коллекции.
func TestProfileCollectionAddMany(t *testing.T) {
	p := NewProfile(filepath.Join(t.TempDir(), "p.json"))
	col, ok := p.AddCollection("тест")
	if !ok {
		t.Fatal("не удалось создать коллекцию")
	}
	added, found := p.CollectionAddMany(col.ID, []int{10, 20, 30})
	if !found || added != 3 {
		t.Fatalf("первый заход: added=%d found=%v", added, found)
	}
	added, _ = p.CollectionAddMany(col.ID, []int{10, 20, 40})
	if added != 1 {
		t.Fatalf("повторный заход: должен добавиться только 40, got %d", added)
	}
	got, _ := p.CollectionPosts(col.ID)
	if len(got) != 4 {
		t.Fatalf("в коллекции должно быть 4 поста, got %d", len(got))
	}

	col2, _ := p.AddCollection("другая")
	added, _ = p.CollectionAddMany(col2.ID, []int{10})
	if added != 1 {
		t.Fatalf("коллекции должны быть независимы, got %d", added)
	}
	if _, found := p.CollectionAddMany("nope", []int{1, 2}); found {
		t.Fatal("несуществующая коллекция не должна находиться")
	}
}

// setupBatchTestRouter собирает минимальный роутер с нужными хендлерами и
// изолированной data/ (t.TempDir): глобальный profile.json не затрагивается.
// Профиль один на тест — используем уникальные id постов в каждом тесте.
func setupBatchTestRouter(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	// Сбрасываем глобальные синглтоны (профиль, аккаунты, конфиг): при
	// -count=2 повторный прогон теста иначе получил бы состояние предыдущего
	// (уже лайкнутые посты, созданные коллекции) из старой data/.
	resetGlobalTestState(t)

	gin.SetMode(gin.TestMode)
	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	{
		api.POST("/batch/like", h.BatchLike)
		api.POST("/batch/hide", h.BatchHide)
		api.POST("/collection", h.CreateCollection)
		api.POST("/collection/:id/posts", h.CollectionAddMany)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// TestBatchLikeHandler — POST /api/batch/like одним запросом: профиль меняется,
// ответ содержит changed, идемпотентен, повторный запрос с liked=false — откат.
func TestBatchLikeHandler(t *testing.T) {
	ts := setupBatchTestRouter(t)

	do := func(body map[string]any) (map[string]any, int) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/api/batch/like", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out, resp.StatusCode
	}

	out, code := do(map[string]any{"ids": []int{2111, 2222}, "liked": true})
	if code != http.StatusOK {
		t.Fatalf("status=%d out=%v", code, out)
	}
	if out["changed"] != float64(2) {
		t.Fatalf("ожидалось changed=2, got %v", out["changed"])
	}
	out, _ = do(map[string]any{"ids": []int{2111, 2222}, "liked": true})
	if out["changed"] != float64(0) {
		t.Fatalf("повторный лайк не идемпотентен: %v", out)
	}
	out, _ = do(map[string]any{"ids": []int{2111}, "liked": false})
	if out["changed"] != float64(1) {
		t.Fatalf("откат: ожидалось changed=1, got %v", out)
	}
	_, code = do(map[string]any{"ids": []int{}, "liked": true})
	if code != http.StatusBadRequest {
		t.Fatalf("пустой ids должен отклоняться, status=%d", code)
	}
}

// TestBatchHideHandler — массовое скрытие профилем и откат.
func TestBatchHideHandler(t *testing.T) {
	ts := setupBatchTestRouter(t)

	do := func(body map[string]any) (map[string]any, int) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/api/batch/hide", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out, resp.StatusCode
	}

	out, code := do(map[string]any{"ids": []int{3301, 3302}, "hidden": true})
	if code != http.StatusOK || out["changed"] != float64(2) {
		t.Fatalf("скрытие: status=%d out=%v", code, out)
	}
	out, _ = do(map[string]any{"ids": []int{3301}, "hidden": false})
	if out["changed"] != float64(1) {
		t.Fatalf("откат скрытия: %v", out)
	}
}

// TestCollectionAddManyHandler — POST /api/collection/:id/posts.
func TestCollectionAddManyHandler(t *testing.T) {
	ts := setupBatchTestRouter(t)

	raw, _ := json.Marshal(map[string]string{"name": "batch"})
	creq, _ := http.NewRequest("POST", ts.URL+"/api/collection", bytes.NewReader(raw))
	creq.Header.Set("Content-Type", "application/json")
	cr, err := ts.Client().Do(creq)
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Body.Close()
	var colResp struct {
		Collection struct {
			ID string `json:"id"`
		} `json:"collection"`
	}
	json.NewDecoder(cr.Body).Decode(&colResp)
	if colResp.Collection.ID == "" {
		t.Fatal("не создалась коллекция")
	}

	raw, _ = json.Marshal(map[string][]int{"ids": {7, 8, 9}})
	req, _ := http.NewRequest("POST", ts.URL+"/api/collection/"+colResp.Collection.ID+"/posts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if out["added"] != float64(3) {
		t.Fatalf("ожидалось added=3, got %v", out)
	}

	req2, _ := http.NewRequest("POST", ts.URL+"/api/collection/"+colResp.Collection.ID+"/posts", bytes.NewReader(raw))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer resp2.Body.Close()
	json.NewDecoder(resp2.Body).Decode(&out)
	if out["added"] != float64(0) {
		t.Fatalf("повторное добавление не идемпотентно: %v", out)
	}
}
