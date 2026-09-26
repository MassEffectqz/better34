package internal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// setupCollectionsTestRouter — роутер с /api/collections в изолированной data/
// (та же схема, что и setupProfileTestRouter).
func setupCollectionsTestRouter(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(oldWd) })
	os.MkdirAll("data", 0o755)
	resetGlobalTestState(t)
	// БД этим тестом не трогается, но глобальный синглтон мог остаться от
	// предыдущего теста: закрываем и сбрасываем, как в setupBatchTestRouter.
	t.Cleanup(func() {
		if postDB != nil {
			postDB.Close()
			postDB = nil
		}
		dbOnce = sync.Once{}
	})

	gin.SetMode(gin.TestMode)
	h := NewHandler()
	r := gin.New()
	api := r.Group("/api")
	{
		api.GET("/collections", h.ListCollections)
		api.POST("/collections/reorder", h.ReorderCollections)
	}
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func getCollections(t *testing.T, ts *httptest.Server) []struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
	IDs    []int  `json:"ids"`
	Covers []int  `json:"covers"`
} {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/collections")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var out struct {
		Collections []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Count  int    `json:"count"`
			IDs    []int  `json:"ids"`
			Covers []int  `json:"covers"`
		} `json:"collections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Collections
}

// TestListCollectionsCoversAndIDs — список коллекций отдаёт ids всех постов
// (для ZIP-экспорта) и covers до 4 id (для мозаики обложек).
func TestListCollectionsCoversAndIDs(t *testing.T) {
	ts := setupCollectionsTestRouter(t)

	p := GetProfile()
	p.mu.Lock()
	p.Collections = []Collection{
		{ID: "c1", Name: "Пять", Posts: []int{1, 2, 3, 4, 5}},
		{ID: "c2", Name: "Пустая", Posts: nil},
	}
	p.mu.Unlock()

	cols := getCollections(t, ts)
	if len(cols) != 2 {
		t.Fatalf("collections: got %d, want 2", len(cols))
	}
	if len(cols[0].IDs) != 5 {
		t.Fatalf("ids: got %v", cols[0].IDs)
	}
	if len(cols[0].Covers) != 4 || cols[0].Covers[0] != 1 {
		t.Fatalf("covers должны быть первыми 4 id: got %v", cols[0].Covers)
	}
	if cols[1].Count != 0 || len(cols[1].Covers) != 0 {
		t.Fatalf("пустая коллекция: count=%d covers=%v", cols[1].Count, cols[1].Covers)
	}
}

// TestReorderCollections — порядок коллекций сохраняется после перестановки,
// неизвестные id игнорируются, а перестановка переживает перезагрузку профиля.
func TestReorderCollections(t *testing.T) {
	ts := setupCollectionsTestRouter(t)

	p := GetProfile()
	p.mu.Lock()
	p.Collections = []Collection{
		{ID: "a", Name: "A", Posts: []int{1}},
		{ID: "b", Name: "B", Posts: []int{2}},
		{ID: "c", Name: "C", Posts: []int{3}},
	}
	p.mu.Unlock()

	body, _ := json.Marshal(map[string][]string{"ids": {"c", "a", "b", "nope"}})
	resp, err := http.Post(ts.URL+"/api/collections/reorder", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	cols := getCollections(t, ts)
	got := []string{cols[0].ID, cols[1].ID, cols[2].ID}
	want := []string{"c", "a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("порядок: got %v, want %v", got, want)
	}
	// Данные коллекций не потерялись при перестановке.
	if cols[0].Name != "C" || len(cols[0].IDs) != 1 || cols[0].IDs[0] != 3 {
		t.Fatalf("коллекция C повреждена: %+v", cols[0])
	}

	// Порядок сохранён в profile.json — переживает перезапуск.
	resetGlobalTestState(t)
	reloaded := getCollections(t, ts)
	got2 := []string{reloaded[0].ID, reloaded[1].ID, reloaded[2].ID}
	if strings.Join(got2, ",") != strings.Join(want, ",") {
		t.Fatalf("после перезагрузки профиля: got %v, want %v", got2, want)
	}
}

// TestReorderCollectionsUnknownIDs — запрос только с неизвестными id даёт 404,
// чтобы клиент знал: ничего не переставилось.
func TestReorderCollectionsUnknownIDs(t *testing.T) {
	ts := setupCollectionsTestRouter(t)

	p := GetProfile()
	p.mu.Lock()
	p.Collections = []Collection{{ID: "a", Name: "A", Posts: []int{1}}}
	p.mu.Unlock()

	body, _ := json.Marshal(map[string][]string{"ids": {"zzz"}})
	resp, err := http.Post(ts.URL+"/api/collections/reorder", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
	if cols := getCollections(t, ts); len(cols) != 1 || cols[0].ID != "a" {
		t.Fatalf("порядок изменился при ошибке: %+v", cols)
	}
}
