package internal

import (
	"database/sql"
	"hash/crc32"
	"log"
	"time"
)

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

// SourceCommentsForPost — кэш комментариев источника поста. Второй
// результат true, если запись уже запрашивалась (тогда «нет комментариев» —
// тоже достоверный ответ, и второй раз источник не опрашиваем).
func (db *PostDB) SourceCommentsForPost(postID int, site string) ([]*SourceComment, bool) {
	rows, err := db.read.Query(`SELECT cid, author, body, created_at FROM source_comments
		WHERE post_id=? AND site=? ORDER BY cid ASC`, postID, site)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	out := make([]*SourceComment, 0, 8)
	for rows.Next() {
		c := &SourceComment{PostID: postID}
		if err := rows.Scan(&c.ID, &c.Author, &c.Body, &c.CreatedAt); err != nil {
			return out, true
		}
		out = append(out, c)
	}
	// Обрыв на середине: список может быть неполным, а второй результат
	// (true) кэширует его как достоверный — логируем, чтобы это было видно.
	if err := rows.Err(); err != nil {
		log.Printf("source comments %d/%s: чтение прервано: %v", postID, site, err)
	}
	return out, true
}

// SourceCommentState — кэшированное состояние поста: есть ли комментарии
// (проверено), сколько их и когда запрашивали. ready=false → источник ещё
// не опрашивали, клиент должен сделать первый запрос.
func (db *PostDB) SourceCommentState(postID int, site string) (comments []*SourceComment, count int, fetchedAt string, ready bool) {
	// Состояние привязано к паре (пост, сайт): id в боре не глобален, и
	// «проверяли hypnohub» ничего не говорит про gelbooru.
	var n int
	if err := db.read.QueryRow(`SELECT count, fetched_at FROM source_comments_state
		WHERE post_id=? AND site=?`, postID, site).Scan(&n, &fetchedAt); err != nil {
		// Нет записи — источник ещё не опрашивали.
		comments, _ = db.SourceCommentsForPost(postID, site)
		return comments, 0, "", false
	}
	comments, _ = db.SourceCommentsForPost(postID, site)
	return comments, n, fetchedAt, true
}

// ReplaceSourceComments полностью заменяет кэш комментариев поста и
// фиксирует comment_count (0 — «у поста их нет»).
func (db *PostDB) ReplaceSourceComments(postID int, site string, list []*SourceComment) error {
	return db.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM source_comments WHERE post_id=? AND site=?`, postID, site); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := tx.Prepare(`INSERT INTO source_comments
			(post_id, site, cid, author, body, created_at, fetched_at) VALUES (?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, c := range list {
			if c.Body == "" {
				continue
			}
			cid := c.ID
			if cid <= 0 {
				// У части сайтов id не приходит — ключ всё равно должен быть.
				cid = int(crc32.ChecksumIEEE([]byte(c.Author + "\x00" + c.Body + "\x00" + c.CreatedAt)))
			}
			if _, err := stmt.Exec(postID, site, cid, c.Author, c.Body, c.CreatedAt, now); err != nil {
				return err
			}
		}
		// Запись состояния создаётся даже для пустого списка: «у поста нет
		// комментариев» — тоже достоверный ответ, второй раз не ходим.
		if _, err := tx.Exec(`INSERT INTO source_comments_state (post_id, site, count, fetched_at)
			VALUES (?,?,?,?) ON CONFLICT(post_id, site) DO UPDATE SET
			count=excluded.count, fetched_at=excluded.fetched_at`, postID, site, len(list), now); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE posts SET comment_count=?, has_comments=CASE WHEN ?>0 THEN 1 ELSE has_comments END
			WHERE id=?`, len(list), len(list), postID)
		return err
	})
}

// DeleteSourceCommentsForPost — чистит кэш при удалении поста.
func (db *PostDB) DeleteSourceCommentsForPost(postID int) {
	_, _ = db.db.Exec(`DELETE FROM source_comments WHERE post_id=?`, postID)
	_, _ = db.db.Exec(`DELETE FROM source_comments_state WHERE post_id=?`, postID)
}

// CountCommentsByUser — число комментариев автора.
func (db *PostDB) CountCommentsByUser(username string) int {
	var n int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM comments WHERE username=?`, username).Scan(&n); err != nil {
		return 0
	}
	return n
}

// AddFriendCommentOnce добавляет комментарий, пришедший от друга инстанса, и
// возвращает true, если он действительно новый.
//
// Дедуп по (username, post_id, text): повторная синхронизация каждые пять
// минут не должна плодить копии. Отдельная функция (а не AddComment) нужна
// ещё и потому, что username здесь чужой и с двоеточием — такой «пользователь»
// не должен попадать в отчёт «кто комментировал» и в экспорт профиля.
func (db *PostDB) AddFriendCommentOnce(username string, postID int, text, createdAt string) bool {
	// INSERT ... SELECT ... WHERE NOT EXISTS: проверка и вставка одной
	// командой. Конкурентных писателей нет (MaxOpenConns(1)), гонки не будет.
	res, err := db.db.Exec(
		`INSERT INTO comments(post_id, username, text, created_at)
		 SELECT ?,?,?,? WHERE NOT EXISTS(
		   SELECT 1 FROM comments WHERE username=? AND post_id=? AND text=?
		 )`,
		postID, username, text, createdAt,
		username, postID, text)
	if err != nil {
		log.Printf("friend comment insert: %v", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
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
