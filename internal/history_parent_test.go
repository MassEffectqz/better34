package internal

import (
	"path/filepath"
	"testing"
)

// TestViewHistory — запись/чтение/откат отметок просмотра.
func TestViewHistory(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "vh.db"))
	t.Cleanup(func() { db.Close() })

	if db.IsViewed(1) {
		t.Fatal("свежая БД не должна считать пост просмотренным")
	}
	db.RecordView(1)
	if !db.IsViewed(1) {
		t.Fatal("RecordView не отметил пост")
	}
	db.RecordViews([]int{2, 3, 4})
	for _, id := range []int{2, 3, 4} {
		if !db.IsViewed(id) {
			t.Fatalf("post #%d не отмечен RecordViews", id)
		}
	}
	db.ForgetView(2)
	if db.IsViewed(2) {
		t.Fatal("ForgetView не снял отметку")
	}
	total, latest := db.ViewedStats()
	if total != 3 {
		t.Fatalf("ожидалось 3 отметки, got %d", total)
	}
	if latest == "" {
		t.Fatal("latest (MAX viewed_at) не заполнен")
	}

	// Повторная запись того же поста — upsert, не дубль строки.
	db.RecordView(1)
	total, _ = db.ViewedStats()
	if total != 3 {
		t.Fatalf("upsert создал дубль: total=%d", total)
	}
}

// TestPostParents — danbooru-style связка родитель/дети.
func TestPostParents(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "parents.db"))
	t.Cleanup(func() { db.Close() })

	for _, p := range []*Post{
		{ID: 501, Tags: "a"},
		{ID: 502, Tags: "b"},
		{ID: 503, Tags: "c"},
	} {
		db.AddOrUpdate(p)
	}

	db.SetPostParent(502, 501)
	if got := db.PostParent(502); got != 501 {
		t.Fatalf("PostParent(502)=%d, ожидалось 501", got)
	}
	children := db.PostChildren(501)
	if len(children) != 1 || children[0].ID != 502 {
		t.Fatalf("у 501 должен быть один ребёнок 502, got %+v", children)
	}

	db.SetPostParent(503, 501)
	if got := db.PostChildren(501); len(got) != 2 {
		t.Fatalf("у 501 должно быть два ребёнка, got %d", len(got))
	}

	// 503 — ребёнок 501; ребёнок ребёнка снимаем (самоссылка → 0).
	db.SetPostParent(502, 503)
	if got := db.PostParent(502); got != 503 {
		t.Fatalf("переназначение родителя: got %d", got)
	}
	db.SetPostParent(502, 502)
	if got := db.PostParent(502); got != 0 {
		t.Fatalf("самоссылка должна обнуляться, got %d", got)
	}

	// Удаление привязки.
	db.SetPostParent(503, 0)
	if got := db.PostParent(503); got != 0 {
		t.Fatalf("сброс родителя не сработал, got %d", got)
	}
	if got := db.PostChildren(501); len(got) != 0 {
		t.Fatalf("у 501 не должно остаться детей, got %d", len(got))
	}

	// parent_id сохраняется в сериализации поста.
	got := db.Get(502)
	if got == nil || got.ParentID != 0 {
		t.Fatalf("Get() не вернул корректный ParentID: %+v", got)
	}
}
