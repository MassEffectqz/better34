package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	MD5        string `json:"md5"`
}

// PostDB — SQLite-хранилище постов (modernc.org/sqlite, без CGO).
// Записи точечные (UPDATE нужных колонок в транзакции), чтения — SQL с
// индексами; теги нормализованы в таблицу tags для поиска/подсказок.
// Публичный API совпадает со старой JSON-реализацией: Save/BumpSave
// стали no-op (запись синхронная), Close закрывает соединение.
type PostDB struct {
	mu   sync.Mutex // сериализация транзакций: modernc/sqlite — один писатель
	db   *sql.DB    // запись: одно соединение, убирает SQLITE_BUSY между горутинами
	read *sql.DB    // чтение: пул поверх WAL — читатели не ждут писателя
	path string
}

var (
	postDB  *PostDB
	dbOnce  sync.Once
	dbReady atomic.Bool
)

func GetDB() *PostDB {
	dbOnce.Do(func() {
		postDB = NewPostDB("data/posts.db")
		postDB.importLegacyJSON("data/db.json")
		dbReady.Store(true)
	})
	return postDB
}

// DBReady — истинно после первой инициализации глобальной БД. Фоновые
// процессы (дедуп скачиваний) в тестах/утилитах без БД просто пропускают шаг.
func DBReady() bool { return dbReady.Load() }

const postsTables = `
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
	thumb_path  TEXT NOT NULL DEFAULT '',
	md5         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS tags (
	tag     TEXT NOT NULL COLLATE NOCASE,
	post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	PRIMARY KEY (tag, post_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS comments (
	id         INTEGER PRIMARY KEY,
	post_id    INTEGER NOT NULL,
	username   TEXT NOT NULL,
	text       TEXT NOT NULL,
	created_at TEXT NOT NULL
);
`

// Индексы отдельно от таблиц: перед созданием idx_posts_md5 колонка md5
// должна существовать и в старых БД (её добавляет ALTER ниже).
const postsIndexes = `
CREATE INDEX IF NOT EXISTS idx_posts_downloaded ON posts(downloaded, id);
CREATE INDEX IF NOT EXISTS idx_tags_post ON tags(post_id);
CREATE INDEX IF NOT EXISTS idx_posts_md5 ON posts(md5) WHERE downloaded=1;
CREATE INDEX IF NOT EXISTS idx_comments_post ON comments(post_id, id);
CREATE INDEX IF NOT EXISTS idx_comments_user ON comments(username);
`

func NewPostDB(path string) *PostDB {
	// SQLite сам не создаёт родительские каталоги.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[db] mkdir %s: %v", dir, err)
		}
	}
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
	if _, err := sqlDB.Exec(postsTables); err != nil {
		log.Printf("[db] schema: %v", err)
		panic(err)
	}
	// Миграция существующих БД: колонка md5 для дедупликации скачиваний.
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN md5 TEXT NOT NULL DEFAULT ''`); err == nil {
		log.Printf("[db] добавлена колонка md5")
	}
	if _, err := sqlDB.Exec(postsIndexes); err != nil {
		log.Printf("[db] indexes: %v", err)
		panic(err)
	}

	// Отдельный пул для чтения: в режиме WAL читатели работают
	// параллельно с писателем, поэтому тяжёлые SELECT'ы (статистика
	// профиля, поиск по тегам) не блокируют записи коллекций/комментариев.
	// Схема и миграции уже применены записывающим соединением.
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Printf("[db] open read pool %s: %v", path, err)
		panic(err)
	}
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	readDB.SetMaxOpenConns(n)
	readDB.SetMaxIdleConns(n)

	return &PostDB{db: sqlDB, read: readDB, path: path}
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
	_ = db.read.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&existing)
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

const postCols = `id, tags, file_url, preview_url, file_type, width, height, file_size, score, rating, downloaded, file_path, thumb_path, md5`

func scanPost(scan func(...any) error) (*Post, error) {
	p := &Post{}
	var dl int
	err := scan(&p.ID, &p.Tags, &p.FileURL, &p.PreviewURL, &p.FileType,
		&p.Width, &p.Height, &p.FileSize, &p.Score, &p.Rating, &dl, &p.FilePath, &p.ThumbPath, &p.MD5)
	if err != nil {
		return nil, err
	}
	p.Downloaded = dl != 0
	return p, nil
}

// upsertPostTx полностью заменяет запись + теги (семантика старого AddOrUpdate).
func upsertPostTx(tx *sql.Tx, p *Post) error {
	_, err := tx.Exec(`INSERT INTO posts (`+postCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET tags=excluded.tags, file_url=excluded.file_url,
		 preview_url=excluded.preview_url, file_type=excluded.file_type, width=excluded.width,
		 height=excluded.height, file_size=excluded.file_size, score=excluded.score,
		 rating=excluded.rating, downloaded=excluded.downloaded, file_path=excluded.file_path,
		 thumb_path=excluded.thumb_path`,
		p.ID, p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, boolToInt(p.Downloaded), p.FilePath, p.ThumbPath, p.MD5)
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
func (db *PostDB) Close() {
	_ = db.db.Close()
	if db.read != nil {
		_ = db.read.Close()
	}
}

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
			old.FileSize == p.FileSize && old.Score == p.Score && old.Rating == p.Rating &&
			old.MD5 == p.MD5 {
			return nil
		}
		changed = true
		if _, err := tx.Exec(`UPDATE posts SET tags=?, file_url=?, preview_url=?, file_type=?,
			width=?, height=?, file_size=?, score=?, rating=?, md5=? WHERE id=?`,
			p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
			p.FileSize, p.Score, p.Rating, p.MD5, p.ID); err != nil {
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
	row := db.read.QueryRow(`SELECT `+postCols+` FROM posts WHERE id=?`, id)
	p, err := scanPost(row.Scan)
	if err != nil {
		return nil
	}
	return p
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
	return result
}

func (db *PostDB) GetDownloaded() []*Post {
	return db.queryPosts(`SELECT ` + postCols + ` FROM posts WHERE downloaded=1 ORDER BY id DESC`)
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
			ph := strings.TrimSuffix(strings.Repeat("?,", len(pos)), ",")
			parts = append(parts, fmt.Sprintf(
				"(SELECT COUNT(DISTINCT tag) FROM tags WHERE post_id=p.id AND tag IN (%s)) = %d", ph, len(pos)))
			for _, t := range pos {
				args = append(args, t)
			}
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
	_ = db.read.QueryRow(`SELECT COUNT(*), COALESCE(SUM(downloaded),0) FROM posts`).Scan(&total, &downloaded)
	return map[string]int{"total": total, "downloaded": downloaded}
}

// SetPostMD5 запоминает хэш содержимого поста (после успешного скачивания).
func (db *PostDB) SetPostMD5(id int, md5sum string) {
	_, _ = db.db.Exec(`UPDATE posts SET md5=? WHERE id=?`, md5sum, id)
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
	rows, err := db.read.Query(`
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
	rows, err := db.read.Query(query, args...)
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

// ── Комментарии: локальное обсуждение постов между пользователями ──────

type Comment struct {
	ID        int    `json:"id"`
	PostID    int    `json:"post_id"`
	Username  string `json:"username"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

func (db *PostDB) AddComment(postID int, username, text string) (*Comment, error) {
	createdAt := time.Now().UTC().Format(time.RFC3339)
	res, err := db.db.Exec(
		`INSERT INTO comments(post_id, username, text, created_at) VALUES (?,?,?,?)`,
		postID, username, text, createdAt)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Comment{ID: int(id), PostID: postID, Username: username, Text: text, CreatedAt: createdAt}, nil
}

func (db *PostDB) Comments(postID int) []*Comment {
	rows, err := db.read.Query(
		`SELECT id, post_id, username, text, created_at FROM comments WHERE post_id=? ORDER BY id ASC`, postID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		cm := &Comment{}
		if err := rows.Scan(&cm.ID, &cm.PostID, &cm.Username, &cm.Text, &cm.CreatedAt); err == nil {
			out = append(out, cm)
		}
	}
	return out
}

// DeleteComment удаляет комментарий; возвращает true, если комментарий был.
func (db *PostDB) DeleteComment(id int) bool {
	res, err := db.db.Exec(`DELETE FROM comments WHERE id=?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// CountCommentsByUser — число комментариев автора.
func (db *PostDB) CountCommentsByUser(username string) int {
	var n int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM comments WHERE username=?`, username).Scan(&n); err != nil {
		return 0
	}
	return n
}

// CommentsByUser возвращает все комментарии автора (для бэкапа профиля).
func (db *PostDB) CommentsByUser(username string) []*Comment {
	rows, err := db.read.Query(
		`SELECT id, post_id, username, text, created_at FROM comments WHERE username=? ORDER BY id ASC`, username)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		cm := &Comment{}
		if err := rows.Scan(&cm.ID, &cm.PostID, &cm.Username, &cm.Text, &cm.CreatedAt); err == nil {
			out = append(out, cm)
		}
	}
	return out
}

// ImportComments переносит комментарии из бэкапа, пропуская дубликаты
// (тот же пост + текст + время). Возвращает число добавленных.
func (db *PostDB) ImportComments(username string, in []*Comment) int {
	if len(in) == 0 {
		return 0
	}
	added := 0
	_ = db.withTx(func(tx *sql.Tx) error {
		for _, cm := range in {
			if cm.Text == "" || cm.PostID <= 0 {
				continue
			}
			var one int
			err := tx.QueryRow(
				`SELECT 1 FROM comments WHERE post_id=? AND username=? AND text=? AND created_at=?`,
				cm.PostID, username, cm.Text, cm.CreatedAt).Scan(&one)
			if err == nil {
				continue // уже есть
			}
			createdAt := cm.CreatedAt
			if createdAt == "" {
				createdAt = time.Now().UTC().Format(time.RFC3339)
			}
			if _, err := tx.Exec(
				`INSERT INTO comments(post_id, username, text, created_at) VALUES (?,?,?,?)`,
				cm.PostID, username, cm.Text, createdAt); err != nil {
				continue
			}
			added++
		}
		return nil
	})
	return added
}

func (p *Post) String() string {
	maxLen := min(len(p.Tags), 100)
	return fmt.Sprintf("[%d] %s (%.1f MB) - %s", p.ID, p.FileType, float64(p.FileSize)/1024/1024, p.Tags[:maxLen])
}
