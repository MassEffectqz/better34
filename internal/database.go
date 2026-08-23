package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Post struct {
	ID         int    `json:"id"`
	Tags       string `json:"tags"`
	FileURL    string `json:"file_url"`
	PreviewURL string `json:"preview_url"`
	FileType   string `json:"file_type"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FileSize   int    `json:"file_size"`
	Score      int    `json:"score"`
	Rating     string `json:"rating"`
	Downloaded bool   `json:"downloaded"`
	FilePath   string `json:"file_path"`
	ThumbPath  string `json:"thumb_path"`
}

// PostDB — SQLite-хранилище постов (modernc.org/sqlite, без CGO).
// Записи точечные (UPDATE нужных колонок в транзакции), чтения — SQL с
// индексами; теги нормализованы в таблицу tags для поиска/подсказок.
// Публичный API совпадает со старой JSON-реализацией: Save/BumpSave
// стали no-op (запись синхронная), Close закрывает соединение.
type PostDB struct {
	mu   sync.Mutex // сериализация транзакций: modernc/sqlite — один писатель
	db   *sql.DB
	path string
}

var (
	postDB *PostDB
	dbOnce sync.Once
)

func GetDB() *PostDB {
	dbOnce.Do(func() {
		postDB = NewPostDB("data/posts.db")
		postDB.importLegacyJSON("data/db.json")
	})
	return postDB
}

const postsSchema = `
CREATE TABLE IF NOT EXISTS posts (
	id          INTEGER PRIMARY KEY,
	tags        TEXT NOT NULL DEFAULT '',
	file_url    TEXT NOT NULL DEFAULT '',
	preview_url TEXT NOT NULL DEFAULT '',
	file_type   TEXT NOT NULL DEFAULT '',
	width       INTEGER NOT NULL DEFAULT 0,
	height      INTEGER NOT NULL DEFAULT 0,
	file_size   INTEGER NOT NULL DEFAULT 0,
	score       INTEGER NOT NULL DEFAULT 0,
	rating      TEXT NOT NULL DEFAULT '',
	downloaded  INTEGER NOT NULL DEFAULT 0,
	file_path   TEXT NOT NULL DEFAULT '',
	thumb_path  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_posts_downloaded ON posts(downloaded, id);
CREATE TABLE IF NOT EXISTS tags (
	tag     TEXT NOT NULL COLLATE NOCASE,
	post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	PRIMARY KEY (tag, post_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_tags_post ON tags(post_id);
`

func NewPostDB(path string) *PostDB {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Printf("[db] open %s: %v", path, err)
		panic(err)
	}
	// Один коннект: убирает SQLITE_BUSY между нашими же горутинами.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		log.Printf("[db] ping %s: %v", path, err)
		panic(err)
	}
	if _, err := sqlDB.Exec(postsSchema); err != nil {
		log.Printf("[db] schema: %v", err)
		panic(err)
	}
	return &PostDB{db: sqlDB, path: path}
}

// importLegacyJSON разово переносит старый data/db.json в SQLite и
// переименовывает его в .migrated. Вызывается только из GetDB().
func (db *PostDB) importLegacyJSON(jsonPath string) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return
	}
	var posts []*Post
	if err := json.Unmarshal(data, &posts); err != nil {
		log.Printf("[db] legacy %s: %v (файл оставлен на месте)", jsonPath, err)
		return
	}
	var existing int
	_ = db.db.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&existing)
	if existing > 0 {
		log.Printf("[db] sqlite уже содержит %d постов — legacy-импорт пропущен", existing)
		return
	}
	err = db.withTx(func(tx *sql.Tx) error {
		for _, p := range posts {
			if err := upsertPostTx(tx, p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[db] legacy import: %v (будет повторено при следующем старте)", err)
		return
	}
	total := len(posts)
	log.Printf("[db] перенесено из %s: %d постов", jsonPath, total)
	if err := os.Rename(jsonPath, jsonPath+".migrated"); err != nil {
		log.Printf("[db] rename legacy: %v", err)
	}
}

func (db *PostDB) withTx(fn func(*sql.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

const postCols = `id, tags, file_url, preview_url, file_type, width, height, file_size, score, rating, downloaded, file_path, thumb_path`

func scanPost(scan func(...any) error) (*Post, error) {
	p := &Post{}
	var dl int
	err := scan(&p.ID, &p.Tags, &p.FileURL, &p.PreviewURL, &p.FileType,
		&p.Width, &p.Height, &p.FileSize, &p.Score, &p.Rating, &dl, &p.FilePath, &p.ThumbPath)
	if err != nil {
		return nil, err
	}
	p.Downloaded = dl != 0
	return p, nil
}

// upsertPostTx полностью заменяет запись + теги (семантика старого AddOrUpdate).
func upsertPostTx(tx *sql.Tx, p *Post) error {
	_, err := tx.Exec(`INSERT INTO posts (`+postCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET tags=excluded.tags, file_url=excluded.file_url,
		 preview_url=excluded.preview_url, file_type=excluded.file_type, width=excluded.width,
		 height=excluded.height, file_size=excluded.file_size, score=excluded.score,
		 rating=excluded.rating, downloaded=excluded.downloaded, file_path=excluded.file_path,
		 thumb_path=excluded.thumb_path`,
		p.ID, p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, boolToInt(p.Downloaded), p.FilePath, p.ThumbPath)
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

// Save оставлен для совместимости вызовов: записи теперь синхронные.
func (db *PostDB) Save() error { return nil }

// BumpSave оставлен для совместимости вызовов: откладывать нечего.
func (db *PostDB) BumpSave() {}

// Close закрывает соединение (WAL-чейкпоинт выполняется автоматически).
func (db *PostDB) Close() { _ = db.db.Close() }

func (db *PostDB) AddOrUpdate(post *Post) {
	if post == nil {
		return
	}
	_ = db.withTx(func(tx *sql.Tx) error { return upsertPostTx(tx, post) })
}

// UpsertMeta обновляет только метаданные провайдера, не трогая локальное
// состояние (downloaded/file_path/thumb_path). Возвращает true при изменении.
func (db *PostDB) UpsertMeta(p *Post) bool {
	if p == nil {
		return false
	}
	changed := false
	_ = db.withTx(func(tx *sql.Tx) error {
		old, err := getTx(tx, p.ID)
		if err != nil {
			return err
		}
		if old == nil {
			cp := *p
			cp.Downloaded = false
			cp.FilePath = ""
			cp.ThumbPath = ""
			changed = true
			return upsertPostTx(tx, &cp)
		}
		if old.Tags == p.Tags && old.FileURL == p.FileURL && old.PreviewURL == p.PreviewURL &&
			old.FileType == p.FileType && old.Width == p.Width && old.Height == p.Height &&
			old.FileSize == p.FileSize && old.Score == p.Score && old.Rating == p.Rating {
			return nil
		}
		changed = true
		if _, err := tx.Exec(`UPDATE posts SET tags=?, file_url=?, preview_url=?, file_type=?,
			width=?, height=?, file_size=?, score=?, rating=? WHERE id=?`,
			p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
			p.FileSize, p.Score, p.Rating, p.ID); err != nil {
			return err
		}
		return replaceTagsTx(tx, p.ID, p.Tags)
	})
	return changed
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
	row := db.db.QueryRow(`SELECT `+postCols+` FROM posts WHERE id=?`, id)
	p, err := scanPost(row.Scan)
	if err != nil {
		return nil
	}
	return p
}

func (db *PostDB) queryPosts(query string, args ...any) []*Post {
	rows, err := db.db.Query(query, args...)
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
	return result
}

func (db *PostDB) GetDownloaded() []*Post {
	return db.queryPosts(`SELECT ` + postCols + ` FROM posts WHERE downloaded=1 ORDER BY id DESC`)
}

// SearchDownloaded: группы через '|', внутри группы — AND по всем тегам.
// Пост матчится, если подошла хотя бы одна группа.
func (db *PostDB) SearchDownloaded(tags string) []*Post {
	var conds []string
	var args []any
	for _, g := range strings.Split(tags, "|") {
		list := strings.Fields(strings.ToLower(g))
		if len(list) == 0 {
			continue
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(list)), ",")
		conds = append(conds, fmt.Sprintf(
			"(SELECT COUNT(DISTINCT tag) FROM tags WHERE post_id=p.id AND tag IN (%s)) = %d", ph, len(list)))
		for _, t := range list {
			args = append(args, t)
		}
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
	err := db.db.QueryRow(`SELECT 1 FROM posts WHERE id=?`, id).Scan(&one)
	return err == nil
}

func (db *PostDB) SetDownloaded(id int, filePath, thumbPath string) {
	_ = db.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE posts SET downloaded=1, file_path=?, thumb_path=? WHERE id=?`,
			filePath, thumbPath, id)
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
	_, _ = db.db.Exec(`UPDATE posts SET downloaded=0, file_path='' WHERE downloaded=1 AND (file_path=? OR file_path=?)`, path, abs)
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
	_ = db.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(downloaded),0) FROM posts`).Scan(&total, &downloaded)
	return map[string]int{"total": total, "downloaded": downloaded}
}

// TagSuggestion объявлена в rule34.go.

func likePattern(prefix string) string {
	p := strings.ToLower(prefix)
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `%`, `\%`)
	p = strings.ReplaceAll(p, `_`, `\_`)
	return p + "%"
}

func (db *PostDB) SuggestTagsLocal(prefix string, limit int) []TagSuggestion {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.db.Query(`
		SELECT t.tag, COUNT(*) AS c
		FROM tags t JOIN posts p ON p.id = t.post_id AND p.downloaded = 1
		WHERE t.tag LIKE ? ESCAPE '\'
		GROUP BY t.tag
		ORDER BY c DESC, t.tag
		LIMIT ?`, likePattern(prefix), limit)
	if err != nil {
		log.Printf("[db] suggest: %v", err)
		return nil
	}
	defer rows.Close()
	var out []TagSuggestion
	for rows.Next() {
		var s TagSuggestion
		if err := rows.Scan(&s.Label, &s.Count); err == nil {
			s.Value = s.Label
			out = append(out, s)
		}
	}
	return out
}

// AllTagsFreq считает частотность тегов по всем постам — приближение
// «популярности» тега для штрафа в рекомендациях.
func (db *PostDB) AllTagsFreq() map[string]int {
	return db.tagCounts(`SELECT tag, COUNT(*) FROM tags GROUP BY tag`, nil, 0)
}

func (db *PostDB) TagStats(limit int) map[string]int {
	if limit <= 0 {
		limit = 50
	}
	return db.tagCounts(`
		SELECT t.tag, COUNT(*) AS c
		FROM tags t JOIN posts p ON p.id = t.post_id AND p.downloaded = 1
		GROUP BY t.tag
		ORDER BY c DESC, t.tag
		LIMIT ?`, []any{limit}, limit)
}

func (db *PostDB) tagCounts(query string, args []any, limit int) map[string]int {
	rows, err := db.db.Query(query, args...)
	if err != nil {
		log.Printf("[db] tagcounts: %v", err)
		return map[string]int{}
	}
	defer rows.Close()
	freq := make(map[string]int)
	if limit > 0 {
		freq = make(map[string]int, limit)
	}
	for rows.Next() {
		var t string
		var c int
		if err := rows.Scan(&t, &c); err == nil {
			freq[t] = c
		}
	}
	return freq
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (p *Post) String() string {
	maxLen := min(len(p.Tags), 100)
	return fmt.Sprintf("[%d] %s (%.1f MB) - %s", p.ID, p.FileType, float64(p.FileSize)/1024/1024, p.Tags[:maxLen])
}
