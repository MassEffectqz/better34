package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	Phash      string `json:"phash,omitempty"`     // perceptual hash (пусто у видео)
	Blurhash   string `json:"blurhash,omitempty"`  // placeholder-строка BlurHash
	Source     string `json:"source,omitempty"`    // метка источника: "rule34", "gelbooru", хост (для «откуда пост»)
	ParentID   int    `json:"parent_id,omitempty"` // родительский пост (danbooru-style связки)
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
	// stop сигналит фоновой горутине обслуживания WAL завершиться (Close()).
	stop chan struct{}
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
		StartDBAutoBackup(postDB)
		dbReady.Store(true)
		// pHash-backfill старых скачанных картинок: без него дедуп пережатых
		// копий и «похожие» не видят ранние посты. Откладываем 3с, чтобы не
		// мешать старту; идемпотентно (пропускает уже заполненные). Захватываем
		// экземпляр локально: глобальная ссылка тесты подменяют.
		pdb := postDB
		go func() {
			time.Sleep(3 * time.Second)
			pdb.BackfillPHashSeeds()
			pdb.BackfillPHashes()
		}()
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
	md5         TEXT NOT NULL DEFAULT '',
	phash       TEXT NOT NULL DEFAULT '',
	blurhash    TEXT NOT NULL DEFAULT '',
	source      TEXT NOT NULL DEFAULT '',
	parent_id   INTEGER NOT NULL DEFAULT 0
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
CREATE TABLE IF NOT EXISTS view_history (
	post_id   INTEGER PRIMARY KEY,
	viewed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tag_aliases (
	alias      TEXT NOT NULL COLLATE NOCASE PRIMARY KEY,
	target     TEXT NOT NULL COLLATE NOCASE,
	created_at TEXT NOT NULL
) WITHOUT ROWID;
`

// Индексы отдельно от таблиц: перед созданием idx_posts_md5 колонка md5
// должна существовать и в старых БД (её добавляет ALTER ниже).
const postsIndexes = `
CREATE INDEX IF NOT EXISTS idx_posts_downloaded ON posts(downloaded, id);
CREATE INDEX IF NOT EXISTS idx_tags_post ON tags(post_id);
CREATE INDEX IF NOT EXISTS idx_posts_md5 ON posts(md5) WHERE downloaded=1;
CREATE INDEX IF NOT EXISTS idx_comments_post ON comments(post_id, id);
CREATE INDEX IF NOT EXISTS idx_comments_user ON comments(username);
CREATE INDEX IF NOT EXISTS idx_posts_phash_seed ON posts(phash_seed) WHERE downloaded=1 AND phash<>'';
`

func NewPostDB(path string) *PostDB {
	// SQLite сам не создаёт родительские каталоги.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("[db] mkdir %s: %v", dir, err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(1073741824)"
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
	// phash — визуальный отпечаток для «похожих», blurhash — плейсхолдер.
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN phash TEXT NOT NULL DEFAULT ''`); err == nil {
		log.Printf("[db] добавлена колонка phash")
	}
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN blurhash TEXT NOT NULL DEFAULT ''`); err == nil {
		log.Printf("[db] добавлена колонка blurhash")
	}
	// source — метка провайдера/хоста, откуда скачан пост («откуда пост»).
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err == nil {
		log.Printf("[db] добавлена колонка source")
	}
	// parent_id — родитель/дети постов (danbooru-style связки).
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0`); err == nil {
		log.Printf("[db] добавлена колонка parent_id")
	}
	// phash_seed — корзина для быстрого поиска «похожих»(старшие 16 бит pHash).
	if _, err := sqlDB.Exec(`ALTER TABLE posts ADD COLUMN phash_seed INTEGER NOT NULL DEFAULT 0`); err == nil {
		log.Printf("[db] добавлена колонка phash_seed")
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

	// Фоновое обслуживание WAL: checkpoint(TRUNCATE) раз в час держит
	// -wal маленьким, а PRAGMA optimize подкармливает планировщик статистикой.
	// Останавливается Close() через stop-канал: вечная горутина не должна
	// Exec'ать по закрытому пулу после db.Close() (P2-9).
	stop := make(chan struct{})
	go func() {
		delay := 30 * time.Second
		for {
			timer := time.After(delay)
			select {
			case <-stop:
				return
			case <-timer:
			}
			_, _ = sqlDB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			_, _ = sqlDB.Exec(`PRAGMA optimize`)
			delay = time.Hour
		}
	}()

	return &PostDB{db: sqlDB, read: readDB, path: path, stop: stop}
}

// importLegacyJSON разово переносит старый data/db.json в SQLite и
// переименовывает его в .migrated. Вызывается только из GetDB().
func (db *PostDB) importLegacyJSON(jsonPath string) {
	// Сначала проверяем, нужен ли вообще импорт: при наличии постов в SQLite
	// не читаем десятки МБ JSON на каждом старте, а сразу убираем файл.
	var existing int
	_ = db.read.QueryRow(`SELECT COUNT(*) FROM posts`).Scan(&existing)
	if existing > 0 {
		log.Printf("[db] sqlite уже содержит %d постов — legacy %s не нужен, удаляю", existing, jsonPath)
		_ = os.Remove(jsonPath)
		return
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return
	}
	var posts []*Post
	if err := json.Unmarshal(data, &posts); err != nil {
		log.Printf("[db] legacy %s: %v (файл оставлен на месте)", jsonPath, err)
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
	// Windows: os.Rename не перезаписывает существующий .migrated — старую
	// копию убираем заранее, иначе исходник останется мёртвым грузом.
	_ = os.Remove(jsonPath + ".migrated")
	if err := os.Rename(jsonPath, jsonPath+".migrated"); err != nil {
		_ = os.Remove(jsonPath)
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

const postCols = `id, tags, file_url, preview_url, file_type, width, height, file_size, score, rating, downloaded, file_path, thumb_path, md5, phash, blurhash, source, parent_id`

func scanPost(scan func(...any) error) (*Post, error) {
	p := &Post{}
	var dl int
	err := scan(&p.ID, &p.Tags, &p.FileURL, &p.PreviewURL, &p.FileType,
		&p.Width, &p.Height, &p.FileSize, &p.Score, &p.Rating, &dl, &p.FilePath, &p.ThumbPath, &p.MD5, &p.Phash, &p.Blurhash, &p.Source, &p.ParentID)
	if err != nil {
		return nil, err
	}
	p.Downloaded = dl != 0
	return p, nil
}

// upsertPostTx полностью заменяет запись + теги (семантика старого AddOrUpdate).
func upsertPostTx(tx *sql.Tx, p *Post) error {
	// phash_seed — корзина старших 16 бит pHash; считается из Phash здесь,
	// чтобы запись всегда была согласована с phash (включая очистку).
	seed := 0
	if h, ok := decodePHash(p.Phash); ok {
		seed = phashSeed(h)
	}
	_, err := tx.Exec(`INSERT INTO posts (`+postCols+`, phash_seed) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET tags=excluded.tags, file_url=excluded.file_url,
		 preview_url=excluded.preview_url, file_type=excluded.file_type, width=excluded.width,
		 height=excluded.height, file_size=excluded.file_size, score=excluded.score,
		 rating=excluded.rating, downloaded=excluded.downloaded, file_path=excluded.file_path,
		 thumb_path=excluded.thumb_path, phash=excluded.phash, blurhash=excluded.blurhash,
		 phash_seed=excluded.phash_seed, parent_id=excluded.parent_id,
		 source=CASE WHEN posts.source='' THEN excluded.source ELSE posts.source END`,
		p.ID, p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, boolToInt(p.Downloaded), p.FilePath, p.ThumbPath, p.MD5,
		p.Phash, p.Blurhash, p.Source, p.ParentID, seed)
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

// Ping проверяет доступность БД (для /api/ready).
func (db *PostDB) Ping() error { return db.read.Ping() }

// Close закрывает соединение (WAL-чейкпоинт выполняется автоматически).
func (db *PostDB) Close() {
	// Сначала останавливаем фоновое обслуживание WAL, потом закрываем пул:
	// иначе горутина продолжит Exec по закрытому соединению (P2-9).
	if db.stop != nil {
		close(db.stop)
	}
	_ = db.db.Close()
	// db.read может указывать на тот же пул, что и db.db — не закрываем дважды.
	if db.read != nil && db.read != db.db {
		_ = db.read.Close()
	}
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
		old.MD5 == p.MD5 {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE posts SET tags=?, file_url=?, preview_url=?, file_type=?,
			width=?, height=?, file_size=?, score=?, rating=?, md5=? WHERE id=?`,
		p.Tags, p.FileURL, p.PreviewURL, p.FileType, p.Width, p.Height,
		p.FileSize, p.Score, p.Rating, p.MD5, p.ID); err != nil {
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
// (расстояние Хэмминга pHash ≤ maxDist). Кандидаты ищутся через бакеты
// phash_seed ±1: при расстоянии ≤ 12 старшие 4 бита не могут разойтись
// больше чем на единицу, остальные бакеты можно не сканировать.
func (db *PostDB) SimilarPHash(phash string, excludeID, limit, maxDist int) []*Post {
	target, ok := decodePHash(phash)
	if !ok {
		return nil
	}
	seed := phashSeed(target)
	rows, err := db.read.Query(`SELECT `+postCols+` FROM posts WHERE downloaded=1 AND phash<>'' AND id<>? AND phash_seed IN (?,?,?)`,
		excludeID, seed-1, seed, seed+1)
	if err != nil {
		return nil
	}
	defer rows.Close()
	type scored struct {
		p    *Post
		dist int
	}
	var candidates []scored
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
			candidates = append(candidates, scored{p, d})
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].dist < candidates[j].dist })
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]*Post, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.p)
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
// 0 — похожих нет. Поиск идёт через бакет phash_seed (±1).
func (db *PostDB) FindDownloadedByPHash(phash string, excludeID, maxDist int) int {
	target, ok := decodePHash(phash)
	if !ok || maxDist < 0 {
		return 0
	}
	seed := phashSeed(target)
	rows, err := db.read.Query(`SELECT id, phash FROM posts WHERE downloaded=1 AND phash<>'' AND id<>? AND phash_seed IN (?,?,?)`,
		excludeID, seed-1, seed, seed+1)
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
		log.Printf("rows err: %v", err)
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
	if !DBReady() {
		return
	}
	posts := db.GetDownloaded()
	filled := 0
	for _, p := range posts {
		if p.Phash != "" || p.FilePath == "" {
			continue
		}
		switch strings.ToLower(filepath.Ext(p.FilePath)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff":
		default:
			continue
		}
		if ph := PerceptualHashFile(p.FilePath); ph != "" {
			db.SetPostPHash(p.ID, ph)
			filled++
		}
	}
	if filled > 0 {
		log.Printf("[db] backfill: посчитаны pHash для %d сохранённых картинок", filled)
	}
}

// ── История просмотров ────────────────────────────────────────────────────
// view_history хранит post_id → viewed_at (upsert). Используется фильтром
// «непросмотренное» в локальной ленте: «как позже, но с пометкой» — пост
// автоматически отмечается просмотренным при открытии во вьюере.

// RecordView отмечает пост просмотренным (новое время перезаписывает старое).
func (db *PostDB) RecordView(postID int) {
	if postID <= 0 {
		return
	}
	_, _ = db.db.Exec(`INSERT INTO view_history(post_id, viewed_at) VALUES (?,?)
		ON CONFLICT(post_id) DO UPDATE SET viewed_at=excluded.viewed_at`,
		postID, time.Now().UTC().Format(time.RFC3339))
}

// RecordViews отмечает несколько постов разом (авто-отметка страницы целиком).
func (db *PostDB) RecordViews(ids []int) {
	if len(ids) == 0 {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_ = db.withTx(func(tx *sql.Tx) error {
		for _, id := range ids {
			if id <= 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO view_history(post_id, viewed_at) VALUES (?,?)
				ON CONFLICT(post_id) DO UPDATE SET viewed_at=excluded.viewed_at`, id, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ForgetView снимает отметку «просмотрено» (откат ошибочной автозаметки).
func (db *PostDB) ForgetView(postID int) {
	if postID <= 0 {
		return
	}
	_, _ = db.db.Exec(`DELETE FROM view_history WHERE post_id=?`, postID)
}

// IsViewed — помечен ли пост как просмотренный.
func (db *PostDB) IsViewed(postID int) bool {
	var one int
	if postID <= 0 {
		return false
	}
	if err := db.read.QueryRow(`SELECT 1 FROM view_history WHERE post_id=?`, postID).Scan(&one); err != nil {
		return false
	}
	return true
}

// ViewedIDs — какие из переданных id помечены просмотренными, одним
// SELECT ... WHERE post_id IN (...) вместо N вызовов IsViewed. Нужен фильтру
// «Новое/Виденное» в онлайн-ленте: там страница выдачи — до 60 постов, и
// поштучная проверка давала бы 60 запросов на каждый лист. Чанкинг по 500
// id — как в GetMany (лимит параметров SQLite).
func (db *PostDB) ViewedIDs(ids []int) map[int]bool {
	out := make(map[int]bool, len(ids))
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
		rows, err := db.read.Query(`SELECT post_id FROM view_history WHERE post_id IN (`+ph+`)`, args...)
		if err != nil {
			log.Printf("[db] ViewedIDs(%d ids): %v", len(ids), err)
			return out
		}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				continue
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			log.Printf("[db] ViewedIDs: rows err: %v", err)
		}
		rows.Close()
	}
	return out
}

// ViewedStats возвращает системную статистику просмотров: всего отметок и
// самых «свежих» — для /api/metrics.
func (db *PostDB) ViewedStats() (total int, latest string) {
	_ = db.read.QueryRow(`SELECT COUNT(*) FROM view_history`).Scan(&total)
	_ = db.read.QueryRow(`SELECT MAX(viewed_at) FROM view_history`).Scan(&latest)
	return total, latest
}

// ── Родители/дети постов (danbooru-style) ────────────────────────────────
// Связка «пост → родитель» образует наборы/сиквенсы поверх глобальной базы:
// children родителя видны во вьюере и в «похожих». Самоссылка обнуляется.

// SetPostParent привязывает пост к родителю (0 — снять привязку).
func (db *PostDB) SetPostParent(id, parentID int) {
	if id <= 0 {
		return
	}
	if parentID == id {
		parentID = 0
	}
	_, _ = db.db.Exec(`UPDATE posts SET parent_id=? WHERE id=?`, parentID, id)
}

// PostParent возвращает id родителя поста (0 — нет родителя).
func (db *PostDB) PostParent(id int) int {
	var parent int
	if id <= 0 {
		return 0
	}
	_ = db.read.QueryRow(`SELECT parent_id FROM posts WHERE id=?`, id).Scan(&parent)
	return parent
}

// PostChildren возвращает скачанных детей поста в порядке id.
func (db *PostDB) PostChildren(id int) []*Post {
	if id <= 0 {
		return nil
	}
	rows, err := db.read.Query(`SELECT `+postCols+` FROM posts WHERE parent_id=? ORDER BY id`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Post
	for rows.Next() {
		p, err := scanPost(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
	}
	return out
}

// ── Алиасы тегов (danbooru-style) ─────────────────────────────────────────
// Таблица tag_aliases переводит синонимы в канонический тег («catgirl» →
// «neko») на этапе построения поискового запроса и в локальном поиске.

// TagAlias — запись синонима.
type TagAlias struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
}

// AddTagAlias сохраняет/перезаписывает алиас. Имя и цель нормализуются в
// нижний регистр; пустые значения и alias==target отклоняются.
func (db *PostDB) AddTagAlias(alias, target string) bool {
	alias = strings.ToLower(strings.TrimSpace(alias))
	target = strings.ToLower(strings.TrimSpace(target))
	if alias == "" || target == "" || alias == target {
		return false
	}
	_, err := db.db.Exec(`INSERT INTO tag_aliases(alias, target, created_at) VALUES (?,?,?)
		ON CONFLICT(alias) DO UPDATE SET target=excluded.target, created_at=excluded.created_at`,
		alias, target, time.Now().UTC().Format(time.RFC3339))
	return err == nil
}

// DeleteTagAlias удаляет алиас. true — был удалён.
func (db *PostDB) DeleteTagAlias(alias string) bool {
	res, err := db.db.Exec(`DELETE FROM tag_aliases WHERE alias=?`, strings.ToLower(strings.TrimSpace(alias)))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// ListTagAliases возвращает все алиасы (для UI и экспорта).
func (db *PostDB) ListTagAliases() []TagAlias {
	rows, err := db.read.Query(`SELECT alias, target FROM tag_aliases ORDER BY alias`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []TagAlias
	for rows.Next() {
		var a, t string
		if err := rows.Scan(&a, &t); err != nil {
			continue
		}
		out = append(out, TagAlias{Alias: a, Target: t})
	}
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
	}
	return out
}

// ResolveTag возвращает канонический тег для алиаса: если alias в таблице,
// отдаётся target, иначе исходное имя. Вызовов на горячем пути немного
// (по одному SELECT на уникальный токен запроса), таблица маленькая.
func (db *PostDB) ResolveTag(alias string) string {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return alias
	}
	var target string
	err := db.read.QueryRow(`SELECT target FROM tag_aliases WHERE alias=?`, alias).Scan(&target)
	if err != nil {
		return alias
	}
	return target
}

// TagSuggestion объявлена в rule34.go.

// likePattern — LIKE-шаблон «тег начинается с префикса» (tier 0–1).
func likePattern(prefix string) string {
	p := escapeLikePrefix(prefix)
	if p == "" {
		return "%"
	}
	return p + "%"
}

// localBoundaryMinPrefix — с какого префикса разрешено искать по границе
// слова. Короткие префиксы дают тонну мусора (всё подряд на «_uma»), а
// реальные совпадения и так находятся обычным префиксным поиском.
const localBoundaryMinPrefix = 4

// likePatternBoundary — LIKE-шаблон «префикс стоит сразу после границы
// слова _» (tier 2): solo_breasts по «breast». Нужен отдельно от
// likePattern, потому что один LIKE не может выразить «начало ИЛИ после _».
func likePatternBoundary(prefix string) string {
	p := escapeLikePrefix(prefix)
	if p == "" {
		return "%"
	}
	return "%\\_" + p + "%"
}

// escapeLikePrefix приводит префикс к виду для LIKE: нижний регистр и
// экранирование спецсимволов. Пустая строка — совпадение со всем.
func escapeLikePrefix(prefix string) string {
	p := strings.ToLower(strings.TrimSpace(prefix))
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `%`, `\%`)
	return strings.ReplaceAll(p, `_`, `\_`)
}

func (db *PostDB) SuggestTagsLocal(prefix string, limit int) []TagSuggestion {
	return db.SuggestTagsLocalFor(prefix, limit, "")
}

// SuggestTagsLocalFor — префиксные подсказки по локальной базе, ограниченные
// источником (пустая строка = по всем). Теги разных боеру не взаимозаменяемы,
// поэтому подсказка, скачанная с чужого сайта, в выдаче активного источника —
// как раз «неточная» подсказка.
//
// Сначала выполняется быстрый запрос по префиксу (LIKE 'x%' берёт индекс по
// tags(tag)). Запрос на границу слова (LIKE '%\_x%') индекс использовать не
// может и сканирует таблицу целиком, поэтому он выполняется только если
// префиксных тегов не хватило — на каждый ввод символа полный скан лишний.
//
// Граница слова подключается только для префиксов от localBoundaryMinPrefix
// символов. На коротких («uma») она давала мусор: doma_umaru и
// himouto!_umaru-chan не начинаются с «uma», но содержат «_uma», и
// пользователь получал в подсказках то, что не набирал.
func (db *PostDB) SuggestTagsLocalFor(prefix string, limit int, source string) []TagSuggestion {
	if limit <= 0 {
		limit = 10
	}
	pl := strings.ToLower(strings.TrimSpace(prefix))

	out := db.suggestLocalQuery(likePattern(prefix), limit, source)
	if len(out) < limit && utf8.RuneCountInString(pl) >= localBoundaryMinPrefix {
		seen := make(map[string]bool, len(out))
		for _, s := range out {
			seen[strings.ToLower(s.Value)] = true
		}
		for _, s := range db.suggestLocalQuery(likePatternBoundary(prefix), limit, source) {
			if !seen[strings.ToLower(s.Value)] {
				out = append(out, s)
			}
		}
	}

	// Ранжирование по релевантности делается в Go уже после выборки: сначала
	// префикс, потом граница слова, внутри — по частоте. Иначе SQL отсортирует
	// строго по частоте и точный префиксный тег уедет вниз.
	sortSuggestionsByRelevance(out, pl)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// suggestLocalQuery — один проход по tags с заданным LIKE-шаблоном.
func (db *PostDB) suggestLocalQuery(pattern string, limit int, source string) []TagSuggestion {
	query := `
		SELECT t.tag, COUNT(*) AS c
		FROM tags t JOIN posts p ON p.id = t.post_id AND p.downloaded = 1
		WHERE t.tag LIKE ? ESCAPE '\'`
	args := []any{pattern}
	if source != "" {
		// Посты, скачанные до появления колонки source, пустой источник не
		// имеют — прячем их только вместе с чужими, иначе старые теги просто
		// исчезли бы из подсказок.
		query += ` AND (p.source = ? OR p.source = '')`
		args = append(args, source)
	}
	// Запас сверх limit: ранжирование по релевантности идёт после выборки,
	// иначе LIMIT срезал бы точные префиксные теги в пользу частых прочих.
	fetchLimit := limit * 3
	if fetchLimit < 30 {
		fetchLimit = 30
	}
	query += `
		GROUP BY t.tag
		ORDER BY c DESC, t.tag
		LIMIT ?`
	args = append(args, fetchLimit)

	rows, err := db.read.Query(query, args...)
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
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
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
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
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
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
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

// DeleteCommentsForPost — каскадное удаление комментариев поста
// (вызывается при удалении поста, чтобы в БД не оставались сироты).
func (db *PostDB) DeleteCommentsForPost(postID int) {
	_, _ = db.db.Exec(`DELETE FROM comments WHERE post_id=?`, postID)
}

// ReplaceCommentsPost переносит комментарии одного поста на другой
// (используется при объединении дубликатов).
func (db *PostDB) ReplaceCommentsPost(from, to int) {
	if from == to {
		return
	}
	_, _ = db.db.Exec(`UPDATE comments SET post_id=? WHERE post_id=?`, to, from)
}

// DeletePostRow удаляет запись поста целиком (теги уходят каскадом).
// Файлы на диске и комментарии (если нужны) обрабатывает вызывающий код.
func (db *PostDB) DeletePostRow(id int) {
	_, _ = db.db.Exec(`DELETE FROM posts WHERE id=?`, id)
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
	if err := rows.Err(); err != nil {
		log.Printf("rows err: %v", err)
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
