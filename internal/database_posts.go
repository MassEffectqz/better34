package internal

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
)

// upsertPostTx полностью заменяет запись + теги (семантика старого AddOrUpdate).
func upsertPostTx(tx *sql.Tx, p *Post) error {
	// phash_seed — корзина старших 16 бит pHash; считается из Phash здесь,
	// чтобы запись всегда была согласована с phash (включая очистку).
	seed := 0
	if h, ok := decodePHash(p.Phash); ok {
		seed = phashSeed(h)
	}
	_, err := tx.Exec(`INSERT INTO posts (`+postCols+`, phash_seed) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET tags=excluded.tags, file_url=excluded.file_url,
		 preview_url=excluded.preview_url, file_type=excluded.file_type, width=excluded.width,
		 height=excluded.height, file_size=excluded.file_size, score=excluded.score,
		 rating=excluded.rating, downloaded=excluded.downloaded, file_path=excluded.file_path,
		 thumb_path=excluded.thumb_path, phash=excluded.phash, blurhash=excluded.blurhash,
		 phash_seed=excluded.phash_seed, parent_id=excluded.parent_id,
		 has_comments=excluded.has_comments, comment_count=excluded.comment_count,
		 source=CASE WHEN posts.source='' THEN excluded.source ELSE posts.source END`,
		p.ID, p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, boolToInt(p.Downloaded), p.FilePath, p.ThumbPath, p.MD5,
		p.Phash, p.Blurhash, p.Source, p.ParentID, boolToInt(p.HasComments), p.CommentCount, seed)
	if err != nil {
		return err
	}
	return replaceTagsTx(tx, p.ID, p.Tags)
}

func replaceTagsTx(tx *sql.Tx, id int, tagsStr string) error {
	if _, err := tx.Exec(`DELETE FROM tags WHERE post_id=?`, id); err != nil {
		return err
	}
	for _, t := range strings.Fields(tagsStr) {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags(tag, post_id) VALUES (?,?)`, t, id); err != nil {
			return err
		}
	}
	return nil
}

func (db *PostDB) AddOrUpdate(post *Post) {
	if post == nil {
		return
	}
	if err := db.withTx(func(tx *sql.Tx) error { return upsertPostTx(tx, post) }); err != nil {
		log.Printf("[db] AddOrUpdate post %d: %v", post.ID, err)
	}
}

// upsertMetaTx применяет метаданные в рамках открытой транзакции; возвращает
// true при изменении. Общий код для UpsertMeta и батча UpsertMetaMany.
func upsertMetaTx(tx *sql.Tx, p *Post) (bool, error) {
	old, err := getTx(tx, p.ID)
	if err != nil {
		return false, err
	}
	if old == nil {
		cp := *p
		cp.Downloaded = false
		cp.FilePath = ""
		cp.ThumbPath = ""
		return true, upsertPostTx(tx, &cp)
	}
	if old.Tags == p.Tags && old.FileURL == p.FileURL && old.PreviewURL == p.PreviewURL &&
		old.FileType == p.FileType && old.Width == p.Width && old.Height == p.Height &&
		old.FileSize == p.FileSize && old.Score == p.Score && old.Rating == p.Rating &&
		old.MD5 == p.MD5 && old.HasComments == p.HasComments {
		return false, nil
	}
	// has_comments обновляем вместе с метаданными; comment_count здесь не
	// трогаем — его ставит загрузка комментариев источника (SetSourceCommentCount).
	if _, err := tx.Exec(`UPDATE posts SET tags=?, file_url=?, preview_url=?, file_type=?,
			width=?, height=?, file_size=?, score=?, rating=?, md5=?, has_comments=? WHERE id=?`,
		p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, p.MD5, boolToInt(p.HasComments), p.ID); err != nil {
		return true, err
	}
	return true, replaceTagsTx(tx, p.ID, p.Tags)
}

// UpsertMeta обновляет только метаданные провайдера, не трогая локальное
// состояние (downloaded/file_path/thumb_path). Возвращает true при изменении.
func (db *PostDB) UpsertMeta(p *Post) bool {
	if p == nil {
		return false
	}
	changed := false
	_ = db.withTx(func(tx *sql.Tx) error {
		c, err := upsertMetaTx(tx, p)
		changed = c
		return err
	})
	return changed
}

// UpsertMetaMany батчит метаданные в транзакции: поисковая выдача из
// десятков постов раньше порождала столько же отдельных Begin/Commit через
// пул с одним писателем — доминирующая нагрузка на запись при автоподгрузке
// ленты (P1-3). Чанкинг по 200 постов страховывает от превышения
// SQLITE_LIMIT_VARIABLE_NUMBER при большом числе bind-параметров.
func (db *PostDB) UpsertMetaMany(posts []*Post) {
	if len(posts) == 0 {
		return
	}
	const chunk = 200
	for start := 0; start < len(posts); start += chunk {
		end := start + chunk
		if end > len(posts) {
			end = len(posts)
		}
		batch := posts[start:end]
		if err := db.withTx(func(tx *sql.Tx) error {
			for _, p := range batch {
				if _, err := upsertMetaTx(tx, p); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			log.Printf("[db] UpsertMetaMany(%d posts, chunk from %d): %v", len(posts), start, err)
			return
		}
	}
}

func getTx(tx *sql.Tx, id int) (*Post, error) {
	row := tx.QueryRow(`SELECT `+postCols+` FROM posts WHERE id=?`, id)
	p, err := scanPost(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// Get возвращает копию поста либо nil.
func (db *PostDB) Get(id int) *Post {
	row := db.read.QueryRow(`SELECT `+postCols+` FROM posts WHERE id=?`, id)
	p, err := scanPost(row.Scan)
	if err != nil {
		return nil
	}
	return p
}

// GetMany возвращает посты по списку id одним запросом: обогащение выдачи
// поиска одним SELECT ... WHERE id IN (...) вместо N одиночных Get (P1-3).
// Чанкинг по 500 id страховывает от SQLITE_LIMIT_VARIABLE_NUMBER (999) при
// большом числе параметров (GetPostsByIDs принимает до 2000).
func (db *PostDB) GetMany(ids []int) map[int]*Post {
	out := make(map[int]*Post, len(ids))
	if len(ids) == 0 {
		return out
	}
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.read.Query(`SELECT `+postCols+` FROM posts WHERE id IN (`+ph+`)`, args...)
		if err != nil {
			log.Printf("[db] GetMany(%d ids): %v", len(ids), err)
			return out
		}
		for rows.Next() {
			p, err := scanPost(rows.Scan)
			if err != nil {
				continue
			}
			out[p.ID] = p
		}
		if err := rows.Err(); err != nil {
			log.Printf("[db] GetMany: rows err: %v", err)
		}
		rows.Close()
	}
	return out
}

func (db *PostDB) queryPosts(query string, args ...any) []*Post {
	rows, err := db.read.Query(query, args...)
	if err != nil {
		log.Printf("[db] query: %v", err)
		return nil
	}
	defer rows.Close()
	var result []*Post
	for rows.Next() {
		p, err := scanPost(rows.Scan)
		if err != nil {
			continue
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
	}
	return result
}

func (db *PostDB) GetDownloaded() []*Post {
	return db.queryPosts(`SELECT ` + postCols + ` FROM posts WHERE downloaded=1 ORDER BY id DESC`)
}

// LocalFilter — условия выборки локальной библиотеки, которые раньше
// применялись в Go уже ПОСЛЕ того, как СУБД отдала все скачанные посты.
//
// Каждое поле — обычное условие WHERE, поэтому фильтрация и пагинация
// остаются в SQL: SQLite режет выборку по индексам и отдаёт ровно одну
// страницу вместо всех строк.
type LocalFilter struct {
	Tags     string   // запрос в синтаксисе SearchDownloaded
	Hidden   []string // скрытые теги профиля (пост с любым из них не показываем)
	MinID    int      // только посты с id >= MinID
	Viewed   int      // -1 любые, 0 только непросмотренные, 1 только просмотренные
	MinScore int      // >0 — только посты с оценкой буры (для мини-игры)
	// RatingExcl — рейтинги, которые вырезаются (ratingFilter: sfw выкидывает
	// explicit/questionable/sensitive, 18+ — general/safe). В WHERE, а не в Go:
	// отфильтрованный COUNT и есть тот «available», который мини-игра показывает
	// пользователю вместе с просьбой уменьшить сетку.
	RatingExcl map[string]bool
}

// SearchDownloadedPaged — страница скачанных постов под фильтры и total
// (сколько всего подошло под фильтры, нужно клиенту для «показать ещё»).
//
// Раньше здесь был SearchDownloaded/GetDownloaded + цикл IsViewed на каждый
// пост + режет в Go. На 6–10 тысячах постов это означало: каждый лист ленты
// читал ВСЮ таблицу posts со всеми колонками (включая tags и blurhash) и
// делал отдельный SELECT на каждый пост для «просмотрено». Теперь всё это
// выражается в WHERE, а база отдаёт только limit строк.
func (db *PostDB) SearchDownloadedPaged(f LocalFilter, limit, offset int) ([]*Post, int) {
	conds, args := db.localFilterConds(f)
	where := "p.downloaded=1"
	if len(conds) > 0 {
		where += " AND (" + strings.Join(conds, " AND ") + ")"
	}

	var total int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM posts p WHERE `+where, args...).Scan(&total); err != nil {
		log.Printf("[db] local count: %v", err)
		return nil, 0
	}
	if limit <= 0 {
		return nil, total
	}
	// LIMIT/OFFSET с теми же аргументами, что и у COUNT: порядок параметров
	// обязан совпасть, иначе отфильтрует не то.
	page := db.queryPosts(`SELECT `+postCols+` FROM posts p WHERE `+where+
		` ORDER BY p.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	return page, total
}

// CountDownloadedStats — сколько всего постов скачано и сколько из них с
// оценкой буры. Мини-игре нужны оба числа: «нет скачанных постов», «посты без
// оценки» и «посты отсеял фильтр» — три разные причины, а из available их не
// различить (он уже посчитан под тегами, скрытыми тегами и рейтингом).
// Считаем одним проходом: второй COUNT по той же таблице был бы лишней работой.
func (db *PostDB) CountDownloadedStats() (all, scored int) {
	const q = `SELECT COUNT(*), COALESCE(SUM(CASE WHEN score>=1 THEN 1 ELSE 0 END), 0)` +
		` FROM posts WHERE downloaded=1`
	err := db.read.QueryRow(q).Scan(&all, &scored)
	if err != nil {
		log.Printf("[db] count downloaded: %v", err)
		return 0, 0
	}
	return all, scored
}

// localFilterConds строит условия WHERE для выборки локальной библиотеки.
// Вынесено отдельно, чтобы SearchDownloaded и постраничный вариант собирали
// одинаковые условия и не разъезжались.
func (db *PostDB) localFilterConds(f LocalFilter) ([]string, []any) {
	var conds []string
	var args []any

	if tags := strings.TrimSpace(f.Tags); tags != "" {
		// Переиспользуем разбор синтаксиса SearchDownloaded, но собираем
		// группы в скобках И, а не ИЛИ: скрытые теги и история просмотров —
		// именно ограничители, а не альтернативные условия поиска.
		for _, g := range strings.Split(tags, "|") {
			var pos, neg []string
			for _, t := range strings.Fields(strings.ToLower(g)) {
				if strings.HasPrefix(t, "-") {
					if t = strings.TrimLeft(t, "-"); t != "" && !strings.Contains(t, ":") {
						neg = append(neg, t)
					}
					continue
				}
				if strings.Contains(t, ":") {
					continue
				}
				pos = append(pos, t)
			}
			if len(pos) == 0 && len(neg) == 0 {
				continue
			}
			// Редкий тег первым: чем реже candidates, тем раньше INTERSECT
			// отсекает всё остальное.
			freq := db.tagFreqFor(pos)
			sort.Slice(pos, func(i, j int) bool { return freq[pos[i]] < freq[pos[j]] })
			var parts []string
			if len(pos) > 0 {
				var sub []string
				for _, t := range pos {
					sub = append(sub, "(SELECT post_id FROM tags WHERE tag=?)")
					args = append(args, t)
				}
				parts = append(parts, "p.id IN ("+strings.Join(sub, " INTERSECT ")+")")
			}
			if len(neg) > 0 {
				ph := strings.TrimSuffix(strings.Repeat("?,", len(neg)), ",")
				parts = append(parts, fmt.Sprintf(
					"NOT EXISTS (SELECT 1 FROM tags WHERE post_id=p.id AND tag IN (%s))", ph))
				for _, t := range neg {
					args = append(args, t)
				}
			}
			conds = append(conds, "("+strings.Join(parts, " AND ")+")")
		}
	}

	// Скрытые теги профиля. Раньше это был цикл по посту с разбором tags в
	// Go; здесь — один NOT EXISTS, и посты с тегом отсекаются до выборки.
	if len(f.Hidden) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(f.Hidden)), ",")
		conds = append(conds, fmt.Sprintf(
			"NOT EXISTS (SELECT 1 FROM tags WHERE post_id=p.id AND tag IN (%s))", ph))
		for _, t := range f.Hidden {
			args = append(args, strings.ToLower(t))
		}
	}

	if f.MinID > 0 {
		conds = append(conds, "p.id >= ?")
		args = append(args, f.MinID)
	}

	// Оценка буры. Для мини-игры это условие обязательное: без неё на финале
	// нечего показать пользователю как эталон.
	if f.MinScore > 0 {
		conds = append(conds, "p.score >= ?")
		args = append(args, f.MinScore)
	}

	// «Новое/Виденное». Раньше — IsViewed на каждый пост (N отдельных
	// запросов на лист). Здесь условие на саму таблицу view_history.
	switch f.Viewed {
	case 0:
		conds = append(conds, "p.id NOT IN (SELECT post_id FROM view_history)")
	case 1:
		conds = append(conds, "p.id IN (SELECT post_id FROM view_history)")
	}

	// Рейтинг (sfw/18+). LOWER с обеих сторон: словари сайтов пишут рейтинг
	// как попало ("Explicit", "s"), и без приведения к одному регистру
	// «18+»-турнир протащил бы в себя general-посты.
	if len(f.RatingExcl) > 0 {
		names := make([]string, 0, len(f.RatingExcl))
		for r := range f.RatingExcl {
			names = append(names, strings.ToLower(r))
		}
		sort.Strings(names) // стабильный порядок параметров — иначе план запроса дрожит
		ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
		conds = append(conds, "LOWER(p.rating) NOT IN ("+ph+")")
		for _, r := range names {
			args = append(args, r)
		}
	}

	return conds, args
}

// tagFreqFor — число постов по каждому тегу для сортировки «редкий
// первым» в INTERSECT-поиске: один индексированный запрос на все теги,
// а не глобальный скан таблицы tags.

func (db *PostDB) tagFreqFor(tags []string) map[string]int {
	freq := make(map[string]int, len(tags))
	if len(tags) == 0 {
		return freq
	}
	unique := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	args := make([]any, len(unique))
	for i, t := range unique {
		args[i] = t
	}
	rows, err := db.read.Query(`SELECT tag, COUNT(*) FROM tags WHERE tag IN (`+ph+`) GROUP BY tag`, args...)
	if err != nil {
		return freq
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		var n int
		if err := rows.Scan(&tag, &n); err == nil {
			freq[tag] = n
		}
	}
	if err := rows.Err(); err != nil {
		return freq
	}
	return freq
}

// SearchDownloaded: группы через '|', внутри группы — AND по всем тегам,
// минус-теги ("-tag") исключают посты, мета-токены ("rating:…", "sort:…")
// игнорируются (локальный поиск их не поддерживает). Пост матчится, если
// подошла хотя бы одна группа. Группа без позитивных тегов ("-1boy") —
// это фильтр по всему скачанному набору.
func (db *PostDB) SearchDownloaded(tags string) []*Post {
	var conds []string
	var args []any
	for _, g := range strings.Split(tags, "|") {
		var pos, neg []string
		for _, t := range strings.Fields(strings.ToLower(g)) {
			if strings.HasPrefix(t, "-") {
				if t = strings.TrimLeft(t, "-"); t != "" && !strings.Contains(t, ":") {
					neg = append(neg, t)
				}
				continue
			}
			if strings.Contains(t, ":") { // rating:…/sort:… и пр. мета-синтаксис
				continue
			}
			pos = append(pos, t)
		}
		if len(pos) == 0 && len(neg) == 0 {
			continue
		}
		var parts []string
		if len(pos) > 0 {
			// AND = INTERSECT of post_id sets (instead of a correlated COUNT
			// per post). Rarest tag first cuts candidates early.

			freq := db.tagFreqFor(pos)
			ordered := make([]string, len(pos))
			copy(ordered, pos)
			sort.Slice(ordered, func(i, j int) bool {
				return freq[ordered[i]] < freq[ordered[j]]
			})
			var sub []string
			for _, t := range ordered {
				sub = append(sub, "(SELECT post_id FROM tags WHERE tag=?)")
				args = append(args, t)
			}
			parts = append(parts, "p.id IN ("+strings.Join(sub, " INTERSECT ")+")")
		}
		if len(neg) > 0 {
			ph := strings.TrimSuffix(strings.Repeat("?,", len(neg)), ",")
			parts = append(parts, fmt.Sprintf(
				"NOT EXISTS (SELECT 1 FROM tags WHERE post_id=p.id AND tag IN (%s))", ph))
			for _, t := range neg {
				args = append(args, t)
			}
		}
		conds = append(conds, "("+strings.Join(parts, " AND ")+")")
	}
	if len(conds) == 0 {
		return db.GetDownloaded()
	}
	query := `SELECT ` + postCols + ` FROM posts p WHERE p.downloaded=1 AND (` +
		strings.Join(conds, " OR ") + `) ORDER BY id DESC`
	return db.queryPosts(query, args...)
}

func (db *PostDB) PostExists(id int) bool {
	var one int
	err := db.read.QueryRow(`SELECT 1 FROM posts WHERE id=?`, id).Scan(&one)
	return err == nil
}

func (db *PostDB) SetDownloaded(id int, filePath, thumbPath string) {
	_ = db.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE posts SET downloaded=1, file_path=?, thumb_path=? WHERE id=?`,
			filePath, thumbPath, id)
		return err
	})
}

// SetThumbPathOnly запоминает путь к миниатюре, не трогая downloaded/file_path.
// Нужно для дозагрузки превью по требованию: пост не скачан, но миниатюра
// теперь лежит локально и повторно качать её не нужно.
func (db *PostDB) SetThumbPathOnly(id int, thumbPath string) {
	_ = db.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE posts SET thumb_path=? WHERE id=?`, thumbPath, id)
		return err
	})
}

func (db *PostDB) UnsetDownloaded(id int) {
	_ = db.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE posts SET downloaded=0, file_path='', thumb_path='' WHERE id=?`, id)
		return err
	})
}

func (db *PostDB) UnsetDownloadedByPath(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Старая реализация сравнивала abs-пути с обеих сторон и чистила только
	// FilePath — поведение сохранено.
	if _, err := db.db.Exec(`UPDATE posts SET downloaded=0, file_path='' WHERE downloaded=1 AND (file_path=? OR file_path=?)`, path, abs); err != nil {
		log.Printf("[db] UnsetDownloadedByPath(%s): %v", path, err)
	}
}

func (db *PostDB) CleanNonDownloaded() int {
	res, err := db.db.Exec(`DELETE FROM posts WHERE downloaded=0`)
	if err != nil {
		log.Printf("[db] clean: %v", err)
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

func (db *PostDB) Stats() map[string]int {
	var total, downloaded int
	_ = db.read.QueryRow(`SELECT COUNT(*), COALESCE(SUM(downloaded),0) FROM posts`).Scan(&total, &downloaded)
	return map[string]int{"total": total, "downloaded": downloaded}
}

// DeletePostRow удаляет запись поста целиком (теги уходят каскадом).
// Файлы на диске и комментарии (если нужны) обрабатывает вызывающий код.
// Кэш комментариев источника чистим здесь же — иначе он осиротеет.
func (db *PostDB) DeletePostRow(id int) {
	db.DeleteSourceCommentsForPost(id)
	_, _ = db.db.Exec(`DELETE FROM posts WHERE id=?`, id)
}
