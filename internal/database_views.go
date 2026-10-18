package internal

import (
	"database/sql"
	"log"
	"strings"
	"time"
)

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
