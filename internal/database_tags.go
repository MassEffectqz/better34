package internal

import (
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

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

// TagPreviewPosts отдаёт число постов с тегом и несколько обложек из них.
// Считаем по всем постам в БД (выдача буров тоже попадает в tags), но обложки
// отдаём только для скачанных — у остальных нет ни миниатюры, ни превью.
// Порядок — по id по убыванию: свежие картинки интереснее старых.
func (db *PostDB) TagPreviewPosts(tag string, limit int) (int, []*TagPreviewPost) {
	if tag == "" || limit <= 0 {
		return 0, nil
	}
	var count int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM tags WHERE tag = ?`, tag).Scan(&count); err != nil {
		return 0, nil
	}
	rows, err := db.read.Query(`
		SELECT p.id, p.width, p.height
		FROM tags t JOIN posts p ON p.id = t.post_id
		WHERE t.tag = ? AND p.downloaded = 1
		ORDER BY p.id DESC LIMIT ?`, tag, limit)
	if err != nil {
		return count, nil
	}
	defer rows.Close()
	out := make([]*TagPreviewPost, 0, limit)
	for rows.Next() {
		var id, w, h int
		if err := rows.Scan(&id, &w, &h); err != nil {
			break
		}
		// Все строки уже отфильтрованы по downloaded=1, поэтому обложка — наша
		// миниатюра. Она же кэшируется сервис-воркером, т.е. доступна офлайн.
		out = append(out, &TagPreviewPost{
			ID: id, Thumb: "/api/thumb/" + strconv.Itoa(id), W: w, H: h,
		})
	}
	// Обрыв на середине (SQLite отдаёт Err, а Next просто false) — отдаём
	// что успели, но не молчим: иначе потеря обложек неотличима от «их нет».
	if err := rows.Err(); err != nil {
		log.Printf("tag preview: чтение обложек прервано: %v", err)
	}
	return count, out
}

// RelatedTags — теги, которые чаще всего встречаются вместе с tag.
// Порядок по числу совместных постов: в своей библиотеке «самые частые
// соседи» — это ровно то, что нужно для навигации (для 1girl это solo,
// long_hair, smile…). Частоту соседа используем не для сортировки, а как
// фильтр: тег, встречающийся почти во всех постах (highres, absurdres),
// ничего не говорит о конкретном теге, поэтому его выбрасываем.
func (db *PostDB) RelatedTags(tag string, limit int) []*TagRelated {
	if tag == "" || limit <= 0 {
		return nil
	}
	// Кандидаты: до 200 самых частых соседей (дешевле, чем считать по всем).
	rows, err := db.read.Query(`
		SELECT t2.tag, COUNT(DISTINCT t2.post_id) AS co
		FROM tags t1
		JOIN tags t2 ON t2.post_id = t1.post_id
		WHERE t1.tag = ? AND t2.tag <> ?
		GROUP BY t2.tag
		ORDER BY co DESC, t2.tag
		LIMIT 200`, tag, tag)
	if err != nil {
		return nil
	}
	type cand struct {
		tag string
		co  int
	}
	cands := make([]cand, 0, 64)
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.tag, &c.co); err != nil {
			break
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("related tags: чтение кандидатов прервано: %v", err)
	}
	rows.Close()

	// Знаменатель — посты, у которых вообще есть теги: по нему считаем, насколько
	// сосед «везде». Считаем по той же таблице tags, что и совпадения выше,
	// иначе фильтр сравнивал бы несопоставимые величины.
	var tagged int
	if err := db.read.QueryRow(`SELECT COUNT(DISTINCT post_id) FROM tags`).Scan(&tagged); err != nil || tagged == 0 {
		return nil
	}
	generic := float64(tagged) * relatedGenericRatio

	// Частоты всех кандидатов — одним запросом на пачку. Поштучный COUNT по
	// 200 тегам давал ~150 мс на всплывашку, здесь выходит единицы мс.
	freq := make(map[string]int, len(cands))
	args := make([]any, 0, len(cands))
	for _, c := range cands {
		args = append(args, c.tag)
	}
	if len(args) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
		frows, err := db.read.Query(`SELECT tag, COUNT(*) FROM tags WHERE tag IN (`+ph+`) GROUP BY tag`, args...)
		if err == nil {
			for frows.Next() {
				var tg string
				var n int
				if err := frows.Scan(&tg, &n); err != nil {
					break
				}
				freq[tg] = n
			}
			if err := frows.Err(); err != nil {
				log.Printf("related tags: чтение частот прервано: %v", err)
			}
			frows.Close()
		}
	}

	out := make([]*TagRelated, 0, limit)
	for _, c := range cands {
		// Совпадение хотя бы в 2 постах: иначе это шум из одного поста.
		if c.co < 2 {
			continue
		}
		if f, ok := freq[c.tag]; ok && float64(f) > generic {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, &TagRelated{Tag: c.tag, Count: c.co})
	}
	return out
}

// relatedGenericRatio — доля постов, выше которой тег считается «везде» и
// в смежные не попадает. 0.35 отсекает highres/absurdres, но оставляет
// 1girl/solo, которые действительно полезны как соседи.
const relatedGenericRatio = 0.35

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
