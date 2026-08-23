package internal

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// G1: Get/GetDownloaded возвращают живые *Post ПОСЛЕ снятия RLock;
// параллельный SetDownloaded мутирует те же структуры → data race.
// Воспроизведение: go test -race ./internal/ -run TestAdversarialDBPointerRace -v
func TestAdversarialDBPointerRace(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "race_db.json"))
	defer db.Close()
	db.AddOrUpdate(&Post{
		ID: 700001, Tags: "a b c", FileURL: "https://example.com/1.jpg",
		FileType: "jpg", Width: 100, Height: 100, Score: 10, Rating: "safe",
	})
	db.SetDownloaded(700001, "data/posts/700001/original.jpg", "data/thumbs/700001.jpg")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, p := range db.GetDownloaded() {
				_ = p.Downloaded
				_ = p.FilePath
			}
			if p := db.Get(700001); p != nil {
				_ = p.FilePath
			}
		}
	}()
	for i := 0; i < 3000; i++ {
		db.SetDownloaded(700001, fmt.Sprintf("data/posts/700001/new%d.jpg", i%3), "data/thumbs/700001.jpg")
	}
	close(stop)
	wg.Wait()
}

// G2: Config.maybeReload читает/пишет configCheckAt, c.loadedAt и len(c.APIKeys)
// БЕЗ блокировки — гонка с другими вызовами GetConfig и SetAPIKeys.
// Воспроизведение: go test -race ./internal/ -run TestAdversarialConfigReloadRace -v
func TestAdversarialConfigReloadRace(t *testing.T) {
	cfg := GetConfig()
	origKeys := cfg.GetAPICredentials()
	file := filepath.Join("data", "config.json")
	existed := false
	if _, err := os.Stat(file); err == nil {
		existed = true
	} else {
		os.MkdirAll("data", 0o755)
		os.WriteFile(file, []byte(`{"api_keys":[{"name":"t","api_key":"race-key"}]}`), 0o644)
	}
	defer func() {
		cfg.SetAPIKeys(origKeys)
		if !existed {
			os.Remove(file)
			os.Remove("data")
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				GetConfig()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 400; j++ {
			// сбрасываем «check-таймер»: каждый вызов GetConfig снова
			// проходит через синхронизированные чтения loadedAt/APIKeys
			configCheckAt.Store(0)
			cfg.SetAPIKeys([]APICredential{{Name: "a", APIKey: fmt.Sprintf("k%d", j%2)}})
		}
	}()
	wg.Wait()
}

// G6: BatchRename пишет p.FilePath без блокировки (handlers_maintenance.go),
// а Get возвращает живые указатели → data race между хендлером и читателями.
// Воспроизведение: go test -race ./internal/ -run TestAdversarialBatchRenameRace -v
func TestAdversarialBatchRenameRace(t *testing.T) {
	db := GetDB()
	dbFile := filepath.Join("data", "db.json")
	_, dbStatErr := os.Stat(dbFile)
	dbExisted := dbStatErr == nil
	root := filepath.Join(t.TempDir(), "posts")
	const n = 25
	ids := make([]int, 0, n)
	for i := 0; i < n; i++ {
		id := 9920000 + i
		ids = append(ids, id)
		dir := filepath.Join(root, fmt.Sprintf("%d", id))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "original.jpg"), []byte("x"), 0o644)
		db.AddOrUpdate(&Post{
			ID: id, Tags: "t", FileURL: "https://example.com/a.jpg", FileType: "jpg",
			Width: 1, Height: 1, Score: 1, Rating: "safe",
			Downloaded: true, FilePath: filepath.Join(dir, "original.jpg"),
		})
	}
	defer func() {
		for _, id := range ids {
			db.UnsetDownloaded(id)
		}
		db.CleanNonDownloaded()
		db.Save()
		if !dbExisted {
			os.Remove(dbFile)
			os.Remove("data")
		}
	}()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, id := range ids {
				if p := db.Get(id); p != nil {
					_ = p.FilePath
					_ = p.Downloaded
				}
			}
		}
	}()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/rename", bytes.NewBufferString(`{"template":"{id}_new"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h := &Handler{}
	h.BatchRename(c)

	close(stop)
	wg.Wait()
}
