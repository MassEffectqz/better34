package internal

import (
	"path/filepath"
	"testing"
)

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
