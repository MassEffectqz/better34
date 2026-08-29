package internal

import (
	"path/filepath"
	"testing"
)

func searchTestDB(t *testing.T) *PostDB {
	t.Helper()
	db := NewPostDB(filepath.Join(t.TempDir(), "search_test.db"))
	t.Cleanup(func() { db.Close() })
	db.AddOrUpdate(&Post{ID: 1, Tags: "cat_girl touhou", Downloaded: true})
	db.AddOrUpdate(&Post{ID: 2, Tags: "cat_girl 1boy touhou", Downloaded: true})
	db.AddOrUpdate(&Post{ID: 3, Tags: "dog girl outdoor", Downloaded: true})
	db.AddOrUpdate(&Post{ID: 4, Tags: "cat_girl touhou solo", Downloaded: false})
	return db
}

// Минус-тег внутри группы исключает посты, но не ломает всю группу.
func TestSearchDownloadedMinusTag(t *testing.T) {
	db := searchTestDB(t)
	got := db.SearchDownloaded("cat_girl -1boy")
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("want only post 1 (post 2 has 1boy), got %v", ids(got))
	}
}

// Группа только из минус-тегов — фильтр по всему скачанному набору.
func TestSearchDownloadedOnlyMinusTag(t *testing.T) {
	db := searchTestDB(t)
	got := db.SearchDownloaded("-1boy")
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 1 {
		t.Fatalf("want posts 3,1, got %v", ids(got))
	}
}

// Мета-токены (rating:/sort:) игнорируются, а не ищутся как literal-теги.
func TestSearchDownloadedMetaTokens(t *testing.T) {
	db := searchTestDB(t)
	got := db.SearchDownloaded("cat_girl rating:sfw")
	if len(got) != 2 {
		t.Fatalf("want posts 1,2 (meta token ignored), got %v", ids(got))
	}
}

// Поиск нечувствителен к регистру (теги в БД хранятся как пришли от API).
func TestSearchDownloadedCaseInsensitive(t *testing.T) {
	db := searchTestDB(t)
	got := db.SearchDownloaded("CAT_GIRL -1BOY")
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("want only post 1, got %v", ids(got))
	}
}

func ids(posts []*Post) []int {
	out := make([]int, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}
