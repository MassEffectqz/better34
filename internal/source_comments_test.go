package internal

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// Реальный ответ hypnohub s=comment (json=1 игнорируется, отдаётся XML).
const hypoCommentsXML = `<?xml version="1.0" encoding="UTF-8"?><comments type="array">` +
	`<comment created_at="2026-09-27 00:08" post_id="100" body="100th" creator="HOLYROCKER" id="229848" creator_id="22418"/>` +
	`<comment created_at="2026-09-27 00:09" post_id="100" body="long text here" creator="Gooning" id="594264" creator_id="1"/>` +
	`</comments>`

func TestParseDapiCommentsXML(t *testing.T) {
	list, err := parseDapiComments([]byte(hypoCommentsXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 comments, got %d", len(list))
	}
	if list[0].ID != 229848 || list[0].PostID != 100 {
		t.Errorf("bad ids: %+v", list[0])
	}
	if list[0].Author != "HOLYROCKER" || list[0].Body != "100th" {
		t.Errorf("bad author/body: %+v", list[0])
	}
	if list[1].CreatedAt != "2026-09-27 00:09" {
		t.Errorf("created_at lost: %q", list[1].CreatedAt)
	}
}

func TestParseDapiCommentsJSONVariants(t *testing.T) {
	cases := map[string]string{
		"array":    `[{"id":5,"post_id":7,"body":"hi","creator":"bob","created_at":"2026-01-01 00:00"}]`,
		"wrapper":  `{"@attributes":{"count":1},"comment":[{"id":5,"post_id":7,"text":"hi","author":"bob"}]}`,
		"comments": `{"comments":[{"id":5,"post_id":7,"comment":"hi","username":"bob"}]}`,
	}
	for name, body := range cases {
		list, err := parseDapiComments([]byte(body))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if len(list) != 1 {
			t.Fatalf("%s: want 1, got %d", name, len(list))
		}
		if list[0].Body != "hi" || list[0].Author != "bob" {
			t.Errorf("%s: got %+v", name, list[0])
		}
	}
}

func TestParseDapiCommentsEmpty(t *testing.T) {
	for _, body := range []string{"", "   ", "[]", `<comments type="array"></comments>`} {
		list, err := parseDapiComments([]byte(body))
		if err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(list) != 0 {
			t.Errorf("body %q: want empty, got %d", body, len(list))
		}
	}
}

// Записи без текста не должны попадать в список (иначе пустые строки в UI).
func TestParseDapiCommentsSkipsEmptyBody(t *testing.T) {
	list, err := parseDapiComments([]byte(
		`<comments type="array"><comment id="1" post_id="2" body="" creator="x"/></comments>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %d", len(list))
	}
}

func TestSourceCommentsSkipsRequestWhenDisabled(t *testing.T) {
	c := newBooruClient(gelbooruSite)
	c.srcCommentsOff.Store(true)
	if _, err := c.SourceComments(3000000); !errors.Is(err, ErrSourceCommentsUnsupported) {
		t.Fatalf("want unsupported without HTTP call, got %v", err)
	}
}

func TestFlexBoolAndHasCommentsParsing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"id":1,"file_url":"https://x/y.jpg","has_comments":"true"}`, true},
		{`{"id":1,"file_url":"https://x/y.jpg","has_comments":"false"}`, false},
		{`{"id":1,"file_url":"https://x/y.jpg","has_comments":true}`, true},
		{`{"id":1,"file_url":"https://x/y.jpg"}`, false},
		{`{"id":1,"file_url":"https://x/y.jpg","has_comments":null}`, false},
	} {
		p, err := parseDapiPost([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if bool(p.HasComments) != tc.want {
			t.Errorf("%s: has_comments=%v, want %v", tc.raw, p.HasComments, tc.want)
		}
	}
}

func TestSourceCommentsCacheRoundTrip(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/posts.db")
	defer db.Close()
	db.UpsertMeta(&Post{ID: 42, FileURL: "https://x/42.jpg", HasComments: true})

	if _, _, _, ready := db.SourceCommentState(42, "hypnohub"); ready {
		t.Fatal("post must not be 'ready' before first fetch")
	}
	list := []*SourceComment{
		{ID: 1, PostID: 42, Author: "a", Body: "first", CreatedAt: "2026-09-27 00:08"},
		{ID: 2, PostID: 42, Author: "b", Body: "second", CreatedAt: "2026-09-27 00:09"},
	}
	if err := db.ReplaceSourceComments(42, "hypnohub", list); err != nil {
		t.Fatal(err)
	}
	got, count, _, ready := db.SourceCommentState(42, "hypnohub")
	if !ready || count != 2 || len(got) != 2 {
		t.Fatalf("cache round trip: ready=%v count=%d len=%d", ready, count, len(got))
	}
	if got[0].Body != "first" || got[1].Author != "b" {
		t.Errorf("bad cache content: %+v", got)
	}
	// Повторная запись полностью заменяет кэш (а не дополняет).
	if err := db.ReplaceSourceComments(42, "hypnohub", list[:1]); err != nil {
		t.Fatal(err)
	}
	_, count, _, ready = db.SourceCommentState(42, "hypnohub")
	if !ready || count != 1 {
		t.Fatalf("replace: count=%d ready=%v", count, ready)
	}
	p := db.Get(42)
	if p == nil || p.CommentCount != 1 || !p.HasComments {
		t.Fatalf("post counters: %+v", p)
	}
	// Другой сайт того же поста — отдельная запись (id не глобален).
	if _, _, _, ready := db.SourceCommentState(42, "gelbooru"); ready {
		t.Fatal("other site must not share the cache")
	}
	// Удаление поста чистит кэш.
	db.DeletePostRow(42)
	if _, _, _, ready := db.SourceCommentState(42, "hypnohub"); ready {
		t.Fatal("cache must be dropped with the post")
	}
}

// Пост без комментариев: пустой список от источника — достоверный ответ,
// второй раз источник опрашивать не нужно.
func TestSourceCommentsEmptyIsReady(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/posts.db")
	defer db.Close()
	db.UpsertMeta(&Post{ID: 7, FileURL: "https://x/7.jpg"})
	if err := db.ReplaceSourceComments(7, "hypnohub", nil); err != nil {
		t.Fatal(err)
	}
	_, count, _, ready := db.SourceCommentState(7, "hypnohub")
	if !ready {
		t.Fatal("empty fetch must count as ready")
	}
	if count != 0 {
		t.Fatalf("count=%d, want 0", count)
	}
}

// Комментарии без id (не у всех сайтов он есть) — ключ выводим из содержимого.
func TestSourceCommentsWithoutID(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/posts.db")
	defer db.Close()
	db.UpsertMeta(&Post{ID: 9, FileURL: "https://x/9.jpg"})
	list := []*SourceComment{{PostID: 9, Author: "a", Body: "same text", CreatedAt: "t"}}
	if err := db.ReplaceSourceComments(9, "hypnohub", list); err != nil {
		t.Fatal(err)
	}
	if _, count, _, ready := db.SourceCommentState(9, "hypnohub"); !ready || count != 1 {
		t.Fatalf("id-less comment lost: ready=%v count=%d", ready, count)
	}
	// Повтор той же записи не должен падать по PRIMARY KEY.
	if err := db.ReplaceSourceComments(9, "hypnohub", list); err != nil {
		t.Fatal(err)
	}
}

// Длинный текст с чужого сайта хранится как есть: обрезка — забота клиента.
func TestSourceCommentsKeepsLongBody(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/posts.db")
	defer db.Close()
	db.UpsertMeta(&Post{ID: 11, FileURL: "https://x/11.jpg"})
	long := strings.Repeat("ы", 1200)
	if err := db.ReplaceSourceComments(11, "hypnohub", []*SourceComment{{ID: 1, PostID: 11, Body: long}}); err != nil {
		t.Fatal(err)
	}
	got, _, _, _ := db.SourceCommentState(11, "hypnohub")
	if len(got) != 1 || utf8.RuneCountInString(got[0].Body) != 1200 {
		t.Fatalf("long body truncated: %d runes", utf8.RuneCountInString(got[0].Body))
	}
}
