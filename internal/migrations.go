package internal

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Migration — отдельный шаг декларативной миграции схемы БД.
type Migration struct {
	Version     int
	Description string
	SQL         string
}

// migrations — упорядоченный список версионированных миграций SQLite.
var migrations = []Migration{
	{
		Version:     1,
		Description: "Initial tables, columns and indexes",
		SQL: `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
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
	phash_seed  INTEGER NOT NULL DEFAULT 0,
	has_comments INTEGER NOT NULL DEFAULT 0,
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
CREATE INDEX IF NOT EXISTS idx_posts_downloaded ON posts(downloaded, id);
CREATE INDEX IF NOT EXISTS idx_tags_post ON tags(post_id);
CREATE INDEX IF NOT EXISTS idx_posts_md5 ON posts(md5) WHERE downloaded=1;
CREATE INDEX IF NOT EXISTS idx_comments_post ON comments(post_id, id);
CREATE INDEX IF NOT EXISTS idx_comments_user ON comments(username);
CREATE INDEX IF NOT EXISTS idx_posts_phash_seed ON posts(phash_seed) WHERE downloaded=1 AND phash<>'';
`,
	},
	{
		Version:     2,
		Description: "FTS5 full-text search index for tags and posts",
		SQL: `
CREATE VIRTUAL TABLE IF NOT EXISTS posts_fts USING fts5(
	tags,
	content='posts',
	content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS trg_posts_ai AFTER INSERT ON posts BEGIN
	INSERT INTO posts_fts(rowid, tags) VALUES (new.id, new.tags);
END;
CREATE TRIGGER IF NOT EXISTS trg_posts_ad AFTER DELETE ON posts BEGIN
	INSERT INTO posts_fts(posts_fts, rowid, tags) VALUES('delete', old.id, old.tags);
END;
CREATE TRIGGER IF NOT EXISTS trg_posts_au AFTER UPDATE OF tags ON posts BEGIN
	INSERT INTO posts_fts(posts_fts, rowid, tags) VALUES('delete', old.id, old.tags);
	INSERT INTO posts_fts(rowid, tags) VALUES (new.id, new.tags);
END;
`,
	},
}

// RunMigrations последовательно применяет все неприменённые декларативные миграции.
func RunMigrations(db *sql.DB) error {
	// Создаем таблицу миграций, если её еще нет
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	);`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := make(map[int]bool)
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err == nil {
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err == nil {
				applied[v] = true
			}
		}
		rows.Close()
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		log.Printf("[db-migration] applying migration #%d: %s", m.Version, m.Description)
		if _, err := db.Exec(m.SQL); err != nil {
			return fmt.Errorf("migration #%d failed: %w", m.Version, err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, m.Version, now); err != nil {
			return fmt.Errorf("record migration #%d: %w", m.Version, err)
		}
		log.Printf("[db-migration] applied migration #%d successfully", m.Version)
	}

	// Пост-синхронизация FTS5: если таблица posts_fts пуста, а в posts уже есть строки
	var ftsCount, postsCount int
	_ = db.QueryRow(`SELECT count(*) FROM posts_fts`).Scan(&ftsCount)
	_ = db.QueryRow(`SELECT count(*) FROM posts`).Scan(&postsCount)
	if ftsCount == 0 && postsCount > 0 {
		log.Printf("[db-migration] backfilling posts_fts from %d posts...", postsCount)
		_, _ = db.Exec(`INSERT INTO posts_fts(rowid, tags) SELECT id, tags FROM posts`)
	}

	return nil
}
