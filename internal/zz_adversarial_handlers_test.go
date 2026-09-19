package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// G5: CleanDuplicates игнорирует заголовок подтверждения X-Confirm-Dupes
// (handlers_extra.go: `_ = c.GetHeader(...)`) — POST /api/dups/clean удаляет
// файлы даже без подтверждения.
func TestAdversarialCleanDuplicatesWithoutConfirm(t *testing.T) {
	root := t.TempDir()
	cfg := GetConfig()
	oldSave := cfg.GetSavePath()
	cfg.SetSavePath(root)
	defer cfg.SetSavePath(oldSave)

	os.MkdirAll(filepath.Join(root, "a"), 0o755)
	os.MkdirAll(filepath.Join(root, "b"), 0o755)
	content := []byte("same-content-bytes")
	os.WriteFile(filepath.Join(root, "a", "file.bin"), content, 0o644)
	os.WriteFile(filepath.Join(root, "b", "file.bin"), content, 0o644)

	h := &Handler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/dups/clean", nil)
	// без заголовка X-Confirm-Dupes
	h.CleanDuplicates(c)

	if _, err := os.Stat(filepath.Join(root, "a", "file.bin")); err != nil {
		t.Errorf("BUG G5: дубликат удалён БЕЗ подтверждения X-Confirm-Dupes (a/file.bin: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b", "file.bin")); err != nil {
		t.Errorf("BUG G5: дубликат удалён БЕЗ подтверждения X-Confirm-Dupes (b/file.bin: %v)", err)
	}
}

// G7: GetPostsByIDs делает по одному HTTP-запросу на каждый id (N+1).
// 60 id → 60 запросов к API вместо одного пакетного.
func TestAdversarialGetPostsByIDsFanOut(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "adversarial-fake-key"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	var ids []string
	for i := 0; i < 60; i++ {
		ids = append(ids, fmt.Sprintf("%d", 8100000+i))
	}
	h := &Handler{providers: map[string]Provider{"rule34": cl}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/posts-by-ids?ids="+strings.Join(ids, ","), nil)
	h.GetPostsByIDs(c)

	if got := calls.Load(); got > 10 {
		t.Errorf("BUG G7: %d HTTP-запросов к API на %d id (N+1; ожидалось пакетно ≤10)", got, len(ids))
	}
}

// G41: GetTagCounts без лимита на число тегов — 40 тегов → 40 запросов.
func TestAdversarialGetTagCountsFanOut(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cl := NewRule34Client()
	cl.spec.apiURL = srv.URL
	cl.httpClient.Store(&http.Client{})
	seedTestKeys(cl, []APICredential{{Name: "t", APIKey: "adversarial-fake-key"}})
	cl.cache = newBooruCache(filepath.Join(t.TempDir(), "sc.json"))
	cl.suggMu.Lock()
	cl.suggM = make(map[string]suggestionCacheEntry)
	cl.suggMu.Unlock()
	cl.breaker.failures = 0
	cl.breaker.openUntil = time.Time{}

	var tags []string
	for i := 0; i < 40; i++ {
		tags = append(tags, fmt.Sprintf("ab%d", i))
	}
	h := &Handler{providers: map[string]Provider{"rule34": cl}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/tag-counts?tags="+strings.Join(tags, ","), nil)
	h.GetTagCounts(c)

	if got := calls.Load(); got > 20 {
		t.Errorf("BUG G41: %d HTTP-запросов на %d тегов (ожидалось ≤20: suggest + dapi exact-lookup на тег)", got, len(tags))
	}
}

// G10: publishSSE шлёт в канал подписчика в неблокирующем режиме;
// при медленном подписчике сообщения молча теряются (буфер 32).
func TestAdversarialSSEDropsSlowSubscriber(t *testing.T) {
	ch, unsubscribe := subscribeSSE()
	defer unsubscribe()

	const published = 40
	for i := 0; i < published; i++ {
		publishSSE(map[string]any{"n": i})
	}

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got == published {
				return // все сообщения доставлены — бага нет
			}
			t.Errorf("BUG G10: подписчик получил %d из %d событий (буфер 32 — остальные молча потеряны)", got, published)
			return
		}
	}
}

// G8: downloader не ограничивает размер скачиваемого файла (в отличие от
// ProxyRemote с лимитом 4MB) — 6MB файл тихо сохраняется на диск.
func TestAdversarialDownloaderNoSizeLimit(t *testing.T) {
	payload := make([]byte, 6<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := GetConfig()
	oldPath := cfg.GetDownloadPath()
	cfg.SetDownloadPath(dir)
	defer cfg.SetDownloadPath(oldPath)

	d := NewDownloader(1, nil)
	d.setQueueFile(filepath.Join(dir, "q.json"))
	defer d.Close()

	done := make(chan struct{})
	go func() {
		for {
			_, a, don := d.Status()
			if a == 0 && don > 0 {
				close(done)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	d.Submit(DownloadJob{PostID: 9940001, FileURL: srv.URL + "/big.bin", FileType: "bin"})
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("download did not finish")
	}

	filePath := filepath.Join(dir, "9940001", "original.bin")
	st, err := os.Stat(filePath)
	if err == nil && st.Size() > 4<<20 {
		t.Errorf("BUG G8: downloader сохранил %d байт (лимита размера нет — ProxyRemote режет на 4MB)", st.Size())
	}
}

// G17: Downloader.Close() не идемпотентен — повторный вызов паникует
// на close(d.stopCh).
func TestAdversarialDownloaderDoubleClose(t *testing.T) {
	d := NewDownloader(1, nil)
	d.setQueueFile(filepath.Join(t.TempDir(), "q.json"))
	d.Close()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG G17: повторный Close() паникует: %v", r)
		}
	}()
	d.Close()
}
