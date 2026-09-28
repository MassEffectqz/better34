package internal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Эталон «как было до оптимизации»: выборка всех скачанных постов, затем
// фильтрация в Go. Сверяем с ним постраничный путь — правка меняет КАК выбираем,
// а не ЧТО выбираем, и это надо доказать на данных со всеми комбинациями
// фильтров, а не только на пустой базе.
func localExpected(t *testing.T, db *PostDB, f LocalFilter) []*Post {
	t.Helper()
	all := db.SearchDownloaded(f.Tags)
	kept := make([]*Post, 0, len(all))
	for _, p := range all {
		skip := false
		for _, h := range f.Hidden {
			for _, t := range splitTags(p.Tags) {
				if t == h {
					skip = true
					break
				}
			}
			if skip {
				break
			}
		}
		if skip {
			continue
		}
		if f.MinID > 0 && p.ID < f.MinID {
			continue
		}
		if f.Viewed >= 0 && db.IsViewed(p.ID) != (f.Viewed == 1) {
			continue
		}
		kept = append(kept, p)
	}
	// Порядок как в эталоне: SearchDownloaded сортирует по id DESC.
	return kept
}

func splitTags(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, lowerASCII(cur))
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, lowerASCII(cur))
	}
	return out
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func idsOf(posts []*Post) []int {
	out := make([]int, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Выборка локальной библиотеки: пагинация, скрытые теги, min_id и история
// просмотров уезжают в SQL. Проверяем, что набор постов на каждой странице и
// total совпадают с эталонной фильтрацией в Go.
func TestSearchDownloadedPagedMatchesFilterSemantics(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()

	// 12 скачанных постов с разными тегами, 3 НЕ скачанных — их в выдаче быть
	// не должно ни при каких фильтрах.
	for i := 1; i <= 12; i++ {
		tags := "common"
		switch i % 4 {
		case 0:
			tags += " blue_hair"
		case 1:
			tags += " solo"
		case 2:
			tags += " blue_hair solo"
		}
		if i%6 == 0 {
			tags += " ugly"
		}
		db.UpsertMeta(&Post{ID: i, Tags: tags, FileURL: fmt.Sprintf("https://x/%d.jpg", i)})
		db.SetDownloaded(i, fmt.Sprintf("data/save/%d.jpg", i), fmt.Sprintf("thumbs/%d.jpg", i))
	}
	for i := 100; i < 103; i++ {
		db.UpsertMeta(&Post{ID: i, Tags: "common blue_hair", FileURL: "https://x/n.jpg"})
	}
	// Просмотрены: 2, 5, 11. Остальные — «новые».
	for _, id := range []int{2, 5, 11} {
		db.RecordView(id)
	}

	cases := []struct {
		name string
		f    LocalFilter
	}{
		{"без фильтров", LocalFilter{Viewed: -1}},
		{"один тег", LocalFilter{Tags: "blue_hair", Viewed: -1}},
		{"два тега (AND)", LocalFilter{Tags: "blue_hair solo", Viewed: -1}},
		{"минус-тег", LocalFilter{Tags: "common -solo", Viewed: -1}},
		{"скрытый тег", LocalFilter{Tags: "", Hidden: []string{"ugly"}, Viewed: -1}},
		{"скрытый вместе с поиском", LocalFilter{Tags: "common", Hidden: []string{"ugly"}, Viewed: -1}},
		{"min_id", LocalFilter{MinID: 8, Viewed: -1}},
		{"только новые", LocalFilter{Viewed: 0}},
		{"только просмотренные", LocalFilter{Viewed: 1}},
		{"новые + тег", LocalFilter{Tags: "blue_hair", Viewed: 0}},
		{"всё вместе", LocalFilter{Tags: "common", Hidden: []string{"ugly"}, MinID: 5, Viewed: 0}},
	}

	for _, tc := range cases {
		want := localExpected(t, db, tc.f)
		// total обязан совпасть с полным набором под фильтром.
		all, total := db.SearchDownloadedPaged(tc.f, 1000, 0)
		if total != len(want) {
			t.Errorf("%s: total=%d, ожидалось %d (получено %v)", tc.name, total, len(want), idsOf(all))
			continue
		}
		if !eqInts(idsOf(all), idsOf(want)) {
			t.Errorf("%s: выборка %v, ожидалось %v", tc.name, idsOf(all), idsOf(want))
		}
	}

	// Постранично: страницы должны идти подряд и без повторов, а вместе —
	// давать ровно весь набор.
	f := LocalFilter{Tags: "common", Viewed: -1}
	want := localExpected(t, db, f)
	const per = 5
	var paged []int
	for off := 0; ; off += per {
		page, total := db.SearchDownloadedPaged(f, per, off)
		if len(page) == 0 {
			break
		}
		paged = append(paged, idsOf(page)...)
		if off+per >= total {
			break
		}
	}
	if !eqInts(paged, idsOf(want)) {
		t.Errorf("постранично %v, ожидалось %v", paged, idsOf(want))
	}

	// За страницей за пределами набора — пусто, а не ошибка.
	beyond, _ := db.SearchDownloadedPaged(f, per, len(want)+10)
	if len(beyond) != 0 {
		t.Errorf("за пределами набора получено %d постов, ожидалось 0", len(beyond))
	}
}

// Нескачанные посты не попадают в локальную ленту ни при каких фильтрах:
// раньше это обеспечивал WHERE downloaded=1 в SearchDownloaded, и при переносе
// пагинации в SQL легко было бы его потерять.
//
// #1 помечен просмотренным: иначе при Viewed:1 его закономерно не было бы, и
// проверка ничего не отличала бы от «нескачанные не просмотрены».
func TestSearchDownloadedPagedExcludesNotDownloaded(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	db.UpsertMeta(&Post{ID: 1, Tags: "cat", FileURL: "https://x/1.jpg"})
	db.SetDownloaded(1, "data/save/1.jpg", "thumbs/1.jpg")
	db.UpsertMeta(&Post{ID: 2, Tags: "cat", FileURL: "https://x/2.jpg"}) // не скачан
	db.RecordView(1)

	for _, f := range []LocalFilter{
		{Viewed: -1}, {Tags: "cat", Viewed: -1}, {Viewed: 1}, {MinID: 1, Viewed: -1},
	} {
		posts, total := db.SearchDownloadedPaged(f, 50, 0)
		if total != 1 || len(posts) != 1 || posts[0].ID != 1 {
			t.Errorf("фильтр %+v: получено %d постов (total=%d), ожидался только скачанный #1",
				f, len(posts), total)
		}
	}
}

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
