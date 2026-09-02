package internal

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupNowCreatesSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "posts.db")
	db := NewPostDB(dbPath)
	defer db.Close()
	db.AddOrUpdate(&Post{ID: 1, Tags: "naruto blonde", Downloaded: true})

	if err := db.BackupNow(); err != nil {
		t.Fatalf("BackupNow: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 backup file, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "posts-") || !strings.HasSuffix(entries[0].Name(), ".db") {
		t.Fatalf("unexpected backup name: %s", entries[0].Name())
	}

	// Данные из копии должны читаться напрямую (это самодостаточная БД).
	snap, err := sql.Open("sqlite", filepath.Join(dir, "backups", entries[0].Name()))
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	var n int
	if err := snap.QueryRow(`SELECT COUNT(*) FROM posts WHERE downloaded=1`).Scan(&n); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 downloaded post in snapshot, got %d", n)
	}
}

func TestBackupPrune(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Пять «старых» копий; квота 2 — должно остаться ровно 2 последних.
	for i, stamp := range []string{"20240101-000000", "20240102-000000", "20240103-000000", "20240104-000000", "20240105-000000"} {
		name := "posts-" + stamp + ".db"
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte{byte(i)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(backupDir, 2, 0)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("want 2 kept backups, got %d: %v", len(entries), names)
	}
	if entries[0].Name() != "posts-20240104-000000.db" || entries[1].Name() != "posts-20240105-000000.db" {
		t.Fatalf("prune should keep newest files, got %s, %s", entries[0].Name(), entries[1].Name())
	}
}

func TestDBBackupQuotaEnv(t *testing.T) {
	t.Setenv("BRIEFLY_DB_BACKUPS", "3")
	if got := dbBackupQuota(); got != 3 {
		t.Fatalf("want 3, got %d", got)
	}
	t.Setenv("BRIEFLY_DB_BACKUPS", "0")
	if got := dbBackupQuota(); got != 0 {
		t.Fatalf("want 0 (off), got %d", got)
	}
	t.Setenv("BRIEFLY_DB_BACKUPS", "")
	if got := dbBackupQuota(); got != 7 {
		t.Fatalf("default should be 7, got %d", got)
	}
}

func TestPostDB(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test_db.json")

	db := NewPostDB(tmpFile)
	defer db.Close()

	p := &Post{ID: 1, Tags: "naruto blonde", FileURL: "https://example.com/1.jpg", FileType: "jpg", Width: 100, Height: 200, Score: 50, Rating: "safe"}
	db.AddOrUpdate(p)

	if got := db.Get(1); got == nil {
		t.Fatal("expected post to exist")
	}

	if got := db.PostExists(1); !got {
		t.Error("PostExists should return true")
	}

	if got := db.PostExists(999); got {
		t.Error("PostExists should return false for missing")
	}

	db.SetDownloaded(1, "data/posts/1/original.jpg", "data/thumbs/1.jpg")

	downloaded := db.GetDownloaded()
	if len(downloaded) != 1 {
		t.Fatalf("expected 1 downloaded post, got %d", len(downloaded))
	}

	stats := db.Stats()
	if stats["downloaded"] != 1 || stats["total"] != 1 {
		t.Errorf("unexpected stats: %v", stats)
	}

	suggestions := db.SuggestTagsLocal("nar", 5)
	if len(suggestions) != 1 || suggestions[0].Value != "naruto" || suggestions[0].Count != 1 {
		t.Errorf("unexpected suggestions: %v", suggestions)
	}

	tagStats := db.TagStats(10)
	if tagStats["naruto"] != 1 || tagStats["blonde"] != 1 {
		t.Errorf("unexpected tag stats: %v", tagStats)
	}

	db.AddOrUpdate(&Post{ID: 2, Tags: "bleach", FileURL: "https://example.com/2.jpg"})
	if cleaned := db.CleanNonDownloaded(); cleaned != 1 {
		t.Errorf("expected 1 cleaned, got %d", cleaned)
	}
	if db.PostExists(2) {
		t.Error("post 2 should have been cleaned")
	}

	search := db.SearchDownloaded("naruto")
	if len(search) != 1 {
		t.Errorf("expected 1 search result, got %d", len(search))
	}

	pipe := db.SearchDownloaded("naruto | bleach")
	if len(pipe) != 1 || pipe[0].ID != 1 {
		t.Errorf("expected pipe OR search to match naruto post, got %v", pipe)
	}

	andMiss := db.SearchDownloaded("blonde | bleach")
	if len(andMiss) != 1 || andMiss[0].ID != 1 {
		t.Errorf("expected OR group to match naruto blonde post, got %v", andMiss)
	}
}
