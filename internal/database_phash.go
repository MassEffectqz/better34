package internal

import (
	"log"
	"path/filepath"
	"sort"
	"strings"
)

// SetPostMD5 запоминает хэш содержимого поста (после успешного скачивания).
func (db *PostDB) SetPostMD5(id int, md5sum string) {
	if _, err := db.db.Exec(`UPDATE posts SET md5=? WHERE id=?`, md5sum, id); err != nil {
		// Молчаливая потеря md5 ломала бы дедуп постов — фиксируем в лог (P2-8).
		log.Printf("[db] SetPostMD5(%d): %v", id, err)
	}
}

// SetPostPHash сохраняет perceptual hash локальной картинки.
func (db *PostDB) SetPostPHash(id int, phash string) {
	if phash == "" {
		return
	}
	if h, ok := decodePHash(phash); ok {
		if _, err := db.db.Exec(`UPDATE posts SET phash=?, phash_seed=? WHERE id=?`, phash, phashSeed(h), id); err != nil {
			log.Printf("[db] SetPostPHash(%d): %v", id, err)
		}
	} else {
		if _, err := db.db.Exec(`UPDATE posts SET phash=? WHERE id=?`, phash, id); err != nil {
			log.Printf("[db] SetPostPHash(%d): %v", id, err)
		}
	}
}

// SetPostBlurhash сохраняет placeholder-строку для мгновенной отрисовки.
func (db *PostDB) SetPostBlurhash(id int, bh string) {
	if bh == "" {
		return
	}
	if _, err := db.db.Exec(`UPDATE posts SET blurhash=? WHERE id=?`, bh, id); err != nil {
		log.Printf("[db] SetPostBlurhash(%d): %v", id, err)
	}
}

// SimilarPHash возвращает скачанные посты, визуально похожие на данный
// (расстояние Хэмминга pHash ≤ maxDist). Сканирует легковесные пары (id, phash),
// гарантируя 100% точность поиска без ложных пропусков при изменении старших бит.
func (db *PostDB) SimilarPHash(phash string, excludeID, limit, maxDist int) []*Post {
	target, ok := decodePHash(phash)
	if !ok {
		return nil
	}
	rows, err := db.read.Query(`SELECT id, phash FROM posts WHERE downloaded=1 AND phash<>'' AND id<>?`, excludeID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type scored struct {
		id   int
		dist int
	}
	var candidates []scored
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
		d := math_popcount(target ^ other)
		if d <= maxDist {
			candidates = append(candidates, scored{id, d})
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("SimilarPHash rows err: %v", err)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist != candidates[j].dist {
			return candidates[i].dist < candidates[j].dist
		}
		return candidates[i].id < candidates[j].id
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]*Post, 0, len(candidates))
	for _, c := range candidates {
		if p := db.Get(c.id); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// FindDownloadedByMd5 возвращает id уже скачанного поста с таким же хэшем
// (кроме excludeID). 0 — дубликата нет.
func (db *PostDB) FindDownloadedByMd5(md5sum string, excludeID int) int {
	if md5sum == "" {
		return 0
	}
	var id int
	err := db.read.QueryRow(
		`SELECT id FROM posts WHERE downloaded=1 AND md5=? AND id<>? LIMIT 1`, md5sum, excludeID).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

// FindDownloadedByPHash ищет уже скачанный пост, визуально похожий на переданный
// pHash (расстояние Хэмминга ≤ maxDist) — дедуп пережатых/обрезных копий.
// При нескольких кандидатах выбирается ближайший, при равных — меньший id.
// 0 — похожих нет.
func (db *PostDB) FindDownloadedByPHash(phash string, excludeID, maxDist int) int {
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
		log.Printf("FindDownloadedByPHash rows err: %v", err)
	}
	return best
}

// SetPostSource запоминает метку источника поста (провайдер или хост).
func (db *PostDB) SetPostSource(id int, source string) {
	if source == "" {
		return
	}
	_, _ = db.db.Exec(`UPDATE posts SET source=? WHERE id=?`, source, id)
}

// BackfillPHashSeeds — разовый бэкфилл phash_seed для строк, посчитанных
// до введения бакетов: seed=0 при ненулевом phash «отравил» бы корзину 0
// и попутно скрыл её содержимое от поиска похожих. Идемпотентно: строки с
// честным нулевым хэшем остаются в корзине 0.
func (db *PostDB) BackfillPHashSeeds() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[db] BackfillPHashSeeds panic: %v", r)
		}
	}()
	if db == nil || !DBReady() {
		return
	}
	select {
	case <-db.stop:
		return
	default:
	}
	rows, err := db.read.Query(`SELECT id, phash FROM posts WHERE downloaded=1 AND phash<>'' AND phash_seed=0`)
	if err != nil {
		return
	}
	type seedRow struct {
		id   int
		hash uint64
	}
	var pending []seedRow
	for rows.Next() {
		select {
		case <-db.stop:
			return
		default:
		}
		var id int
		var ph string
		if rows.Scan(&id, &ph) != nil {
			continue
		}
		if h, ok := decodePHash(ph); ok && h != 0 {
			pending = append(pending, seedRow{id, h})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return
	}
	for _, r := range pending {
		select {
		case <-db.stop:
			return
		default:
		}
		_, _ = db.db.Exec(`UPDATE posts SET phash_seed=? WHERE id=?`, phashSeed(r.hash), r.id)
	}
	if len(pending) > 0 {
		log.Printf("[db] backfill: обновлены phash_seed для %d постов", len(pending))
	}
}

// BackfillPHashes считает pHash для старых скачанных картинок, у которых
// отпечаток ещё не посчитан (дедуп пережатых копий и «похожие» по ним).
// Best effort в фоне после старта; видео пропускаются по расширению.
func (db *PostDB) BackfillPHashes() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[db] BackfillPHashes panic: %v", r)
		}
	}()
	if db == nil || !DBReady() {
		return
	}
	select {
	case <-db.stop:
		return
	default:
	}
	posts := db.GetDownloaded()
	filled := 0
	for _, p := range posts {
		select {
		case <-db.stop:
			return
		default:
		}
		if p.Phash != "" || p.FilePath == "" {
			continue
		}
		switch strings.ToLower(filepath.Ext(p.FilePath)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff":
		default:
			continue
		}
		if ph := PerceptualHashFile(p.FilePath); ph != "" {
			select {
			case <-db.stop:
				return
			default:
			}
			db.SetPostPHash(p.ID, ph)
			filled++
		}
	}
	if filled > 0 {
		log.Printf("[db] backfill: посчитаны pHash для %d сохранённых картинок", filled)
	}
}
