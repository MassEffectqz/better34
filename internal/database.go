package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
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
	Phash      string `json:"phash,omitempty"`     // perceptual hash (пусто у видео)
	Blurhash   string `json:"blurhash,omitempty"`  // placeholder-строка BlurHash
	Source     string `json:"source,omitempty"`    // метка источника: "rule34", "gelbooru", хост (для «откуда пост»)
	ParentID   int    `json:"parent_id,omitempty"` // родительский пост (danbooru-style связки)
	// HasComments — источник сообщил, что под постом есть комментарии;
	// CommentCount — сколько их на самом деле (заполняется при загрузке).
	HasComments  bool `json:"has_comments,omitempty"`
	CommentCount int  `json:"comment_count,omitempty"`
}

// PostDB — SQLite-хранилище постов (modernc.org/sqlite, без CGO).
// Записи точечные (UPDATE нужных колонок в транзакции), чтения — SQL с
// индексами; теги нормализованы в таблицу tags для поиска/подсказок.
// Публичный API совпадает со старой JSON-реализацией: Save/BumpSave
// стали no-op (запись синхронная), Close закрывает соединение.
type PostDB struct {
	mu       sync.Mutex // сериализация транзакций: modernc/sqlite — один писатель
	db       *sql.DB    // запись: одно соединение, убирает SQLITE_BUSY между горутинами
	read     *sql.DB    // чтение: пул поверх WAL — читатели не ждут писателя
	path     string
	stopOnce sync.Once
	// stop сигналит фоновой горутине обслуживания WAL и backfill завершиться (Close()).
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
			if pdb.stop != nil {
				select {
				case <-pdb.stop:
					return
				case <-time.After(3 * time.Second):
				}
			} else {
				time.Sleep(3 * time.Second)
			}
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
	parent_id   INTEGER NOT NULL DEFAULT 0,
	has_comments  INTEGER NOT NULL DEFAULT 0,
	comment_count INTEGER NOT NULL DEFAULT 0
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
-- source_comments — кэш комментариев источника (dapi s=comment). Ключ
-- (post_id, site, cid): id поста в боре не глобален (у разных сайтов
-- диапазоны пересекаются), поэтому сайт входит в ключ.
CREATE TABLE IF NOT EXISTS source_comments (
	post_id    INTEGER NOT NULL,
	site       TEXT NOT NULL,
	cid        INTEGER NOT NULL,
	author     TEXT NOT NULL DEFAULT '',
	body       TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	fetched_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (post_id, site, cid)
) WITHOUT ROWID;
-- source_comments_state — «источник уже опрашивали»: нужна, чтобы отличать
-- «комментариев нет» (повторно не ходим) от «ещё не проверяли». Живёт
-- отдельно от source_comments, потому что запись должна существовать и при
-- нуле комментариев.
CREATE TABLE IF NOT EXISTS source_comments_state (
	post_id    INTEGER NOT NULL,
	site       TEXT NOT NULL,
	count      INTEGER NOT NULL DEFAULT 0,
	fetched_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (post_id, site)
) WITHOUT ROWID;
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
	if err := RunMigrations(sqlDB); err != nil {
		log.Printf("[db] migrations: %v", err)
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

const postCols = `id, tags, file_url, preview_url, file_type, width, height, file_size, score, rating, downloaded, file_path, thumb_path, md5, phash, blurhash, source, parent_id, has_comments, comment_count`

func scanPost(scan func(...any) error) (*Post, error) {
	p := &Post{}
	var dl, hc int
	err := scan(&p.ID, &p.Tags, &p.FileURL, &p.PreviewURL, &p.FileType,
		&p.Width, &p.Height, &p.FileSize, &p.Score, &p.Rating, &dl, &p.FilePath, &p.ThumbPath, &p.MD5, &p.Phash, &p.Blurhash, &p.Source, &p.ParentID, &hc, &p.CommentCount)
	if err != nil {
		return nil, err
	}
	p.Downloaded = dl != 0
	p.HasComments = hc != 0
	return p, nil
}

// Save оставлен для совместимости вызовов: записи теперь синхронные.
func (db *PostDB) Save() error { return nil }

// BumpSave оставлен для совместимости вызовов: откладывать нечего.
func (db *PostDB) BumpSave() {}

// Ping проверяет доступность БД (для /api/ready).
func (db *PostDB) Ping() error { return db.read.Ping() }

// Close закрывает соединение (WAL-чейкпоинт выполняется автоматически).
func (db *PostDB) Close() {
	// Сначала останавливаем фоновое обслуживание WAL и backfill, потом закрываем пул:
	// иначе горутина продолжит Exec по закрытому соединению (P2-9).
	// Защита от паники при повторном Close() через sync.Once.
	db.stopOnce.Do(func() {
		if db.stop != nil {
			close(db.stop)
		}
	})
	_ = db.db.Close()
	// db.read может указывать на тот же пул, что и db.db — не закрываем дважды.
	if db.read != nil && db.read != db.db {
		_ = db.read.Close()
	}
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
