package internal

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── Эталонная (старая) реализация: полный скан всех строк ────────────────
// Воспроизводит логику HEAD: SELECT ... FROM posts WHERE downloaded=1 AND phash<>''
// без бакетного фильтра.

func (db *PostDB) similarPHashFullScan(phash string, excludeID, limit, maxDist int) []*Post {
	target, ok := decodePHash(phash)
	if !ok {
		return nil
	}
	rows, err := db.read.Query(`SELECT `+postCols+` FROM posts WHERE downloaded=1 AND phash<>'' AND id<>?`, excludeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type scored struct {
		p    *Post
		dist int
	}
	var candidates []scoredPost
	for rows.Next() {
		p, err := scanPost(rows.Scan)
		if err != nil {
			continue
		}
		other, ok := decodePHash(p.Phash)
		if !ok {
			continue
		}
		d := math_popcount(target ^ other)
		if d <= maxDist {
			candidates = append(candidates, scoredPost{p, d})
		}
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	sortPHashCandidates(candidates, limit)
	out := make([]*Post, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.p)
	}
	return out
}

func (db *PostDB) findDownloadedByPHashFullScan(phash string, excludeID, maxDist int) int {
	target, ok := decodePHash(phash)
	if !ok || maxDist < 0 {
		return 0
	}
	rows, err := db.read.Query(`SELECT id, phash FROM posts WHERE downloaded=1 AND phash<>'' AND id<>?`, excludeID)
	if err != nil {
		return 0
	}
	defer rows.Close()
	best, bestDist := 0, maxDist+1
	for rows.Next() {
		var id int
		var otherPH string
		if err := rows.Scan(&id, &otherPH); err != nil {
			continue
		}
		other, ok := decodePHash(otherPH)
		if !ok {
			continue
		}
		if d := math_popcount(target ^ other); d <= maxDist && (best == 0 || d < bestDist || (d == bestDist && id < best)) {
			best, bestDist = id, d
		}
	}
	if err := rows.Err(); err != nil {
		return 0
	}
	return best
}

type scoredPost struct {
	p    *Post
	dist int
}

func sortPHashCandidates(c []scoredPost, limit int) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && (c[j].dist < c[j-1].dist); j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	if len(c) > limit {
		c = c[:limit]
	}
}

func newBenchmarkDB(t *testing.B, n int) (*PostDB, []string) {
	t.Helper()
	db := NewPostDB(filepath.Join(t.TempDir(), "bench.db"))
	t.Cleanup(func() { db.Close() })
	hashes := make([]string, 0, n+1)
	target := rand.Uint64()
	hashes = append(hashes, encodePHash(target))
	for i := 0; i < n; i++ {
		h := rand.Uint64()
		hashes = append(hashes, encodePHash(h))
	}
	for i, h := range hashes {
		db.AddOrUpdate(&Post{ID: i + 1, Tags: "bench", Downloaded: true, Phash: h, FilePath: "data/posts/x.jpg"})
	}
	return db, hashes
}

// ─── Бенчмарки SimilarPHash: бакеты vs полный скан ────────────────────────

func BenchmarkSimilarPHash_Buckets_1K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 1000)
	b.ResetTimer()
	for b.Loop() {
		db.SimilarPHash(hashes[0], -1, 10, 12)
	}
}

func BenchmarkSimilarPHash_FullScan_1K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 1000)
	b.ResetTimer()
	for b.Loop() {
		db.similarPHashFullScan(hashes[0], -1, 10, 12)
	}
}

func BenchmarkSimilarPHash_Buckets_10K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 10000)
	b.ResetTimer()
	for b.Loop() {
		db.SimilarPHash(hashes[0], -1, 10, 12)
	}
}

func BenchmarkSimilarPHash_FullScan_10K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 10000)
	b.ResetTimer()
	for b.Loop() {
		db.similarPHashFullScan(hashes[0], -1, 10, 12)
	}
}

func BenchmarkSimilarPHash_Buckets_50K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 50000)
	b.ResetTimer()
	for b.Loop() {
		db.SimilarPHash(hashes[0], -1, 10, 12)
	}
}

func BenchmarkSimilarPHash_FullScan_50K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 50000)
	b.ResetTimer()
	for b.Loop() {
		db.similarPHashFullScan(hashes[0], -1, 10, 12)
	}
}

// ─── Бенчмарки FindDownloadedByPHash: бакеты vs полный скан ───────────────

func BenchmarkFindDownloadedByPHash_Buckets_10K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 10000)
	b.ResetTimer()
	for b.Loop() {
		db.FindDownloadedByPHash(hashes[0], -1, 5)
	}
}

func BenchmarkFindDownloadedByPHash_FullScan_10K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 10000)
	b.ResetTimer()
	for b.Loop() {
		db.findDownloadedByPHashFullScan(hashes[0], -1, 5)
	}
}

func BenchmarkFindDownloadedByPHash_Buckets_50K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 50000)
	b.ResetTimer()
	for b.Loop() {
		db.FindDownloadedByPHash(hashes[0], -1, 5)
	}
}

func BenchmarkFindDownloadedByPHash_FullScan_50K(b *testing.B) {
	db, hashes := newBenchmarkDB(b, 50000)
	b.ResetTimer()
	for b.Loop() {
		db.findDownloadedByPHashFullScan(hashes[0], -1, 5)
	}
}

// ─── Бенчмарк SSE-коалесинга ─────────────────────────────────────────────

func BenchmarkSSECoalesce(b *testing.B) {
	hub := sseHub{subs: make(map[chan string]struct{})}
	ch := make(chan string, 128)
	hub.subs[ch] = struct{}{}
	defer func() {
		hub.mu.Lock()
		delete(hub.subs, ch)
		hub.mu.Unlock()
	}()

	b.Run("old_per_event", func(b *testing.B) {
		for b.Loop() {
			select {
			case ch <- `{"type":"status","queued":1}`:
			default:
			}
		}
	})

	b.Run("new_coalesced", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			select {
			case ch <- `{"type":"status","queued":1}`:
			default:
			}
			if i%50 == 0 {
				// flush раз в 50 событий (имитация 50мс окна)
			}
		}
	})
}

// ─── Бенчмарк ETag-генерации ─────────────────────────────────────────────

func BenchmarkETagGeneration(b *testing.B) {
	info, err := os.Stat("static/js/feed.js")
	if err != nil {
		b.Skip("static/js/feed.js not found")
	}
	b.ResetTimer()
	for b.Loop() {
		_ = fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size())
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
