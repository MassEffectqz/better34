package internal

import (
	"context"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Мини-игра «выбери лучшее»: турнирная сетка на выбывание.
//
// Пользователь выбирает число раундов (пресет), сервер отдаёт готовую сетку
// из 2^rounds участников. Каждый матч — сравнение 1 vs 1, сетка разыгрывается
// последовательно, пока не останется один победитель.
//
// Ключевое отличие от «покажи случайные посты»: у постов есть score от буры
// (объективный эталон), поэтому после финала можно показать, совпал ли выбор
// пользователя с оценкой сообщества. Именно это делает игру обучающей.

const (
	tournamentMinRounds = 2
	tournamentMaxRounds = 5
	// tournamentMaxParticipants — потолок запроса /api/tournament/posts: ровно
	// размер максимальной сетки. Больше id турнир никогда не спрашивает, а
	// запас сверху превратил бы эндпоинт в «достань мне любые посты по id».
	tournamentMaxParticipants = 1 << tournamentMaxRounds
)

// TournamentPost — участник сетки. Score отдаётся сразу, но фронт показывает
// его ТОЛЬКО на финальном экране: во время матча видны должны быть глаза, а не цифры.
type TournamentPost struct {
	ID         int    `json:"id"`
	Tags       string `json:"tags"`
	Score      int    `json:"score"`
	Rating     string `json:"rating"`
	FileType   string `json:"file_type"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Downloaded bool   `json:"downloaded"`
	// Thumb — готовый URL миниатюры (локальная миниатюра или прокси превью),
	// FileURL — полное изображение для оверлея.
	//
	// PreviewURL/SampleURL/FileSize/Blurhash — СЫРЫЕ данные источника. Клиент
	// обязан отдавать их вьюверу как есть: проксирование — дело сервера. Раньше
	// клиент сам заворачивал адреса в /api/proxy и на «Открыть пост» отправлял
	// «/api/proxy?url=/api/proxy?url=…» — картинка висела на спиннере бесконечно.
	Thumb      string `json:"thumb"`
	FileURL    string `json:"file_url"`
	PreviewURL string `json:"preview_url,omitempty"`
	SampleURL  string `json:"sample_url,omitempty"`
	FileSize   int    `json:"file_size,omitempty"`
	Blurhash   string `json:"blurhash,omitempty"`
	Source     string `json:"source"`
}

// GetTournament — GET /api/tournament?rounds=3&source=offline|online&rating=&tags=...
//
// Отдаёт участников и пустую сетку. Победителей не считает: матчи клиент
// разыгрывает локально, иначе пришлось бы хранить состояние турнира на сервере.
func (h *Handler) GetTournament(c *gin.Context) {
	rounds, ok := parseTournamentRounds(c.Query("rounds"))
	if !ok {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	size := 1 << rounds

	source := c.DefaultQuery("source", "offline")
	if source != "offline" && source != "online" {
		AbortWithError(c, ErrInvalidRequest)
		return
	}

	// Фильтр рейтинга (все/sfw/18+). Мусор — ошибка запроса, а не «все посты»:
	// молча проигнорированный фильтр показал бы пользователю ровно то, что он
	// просил не показывать.
	rating, ok := parseTournamentRating(c.Query("rating"))
	if !ok {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	ratingTerms, ratingExcl := ratingFilter(rating)

	db := GetDB()
	profile := ProfileFor(c)
	profile.mu.RLock()
	hidden := make([]string, 0, len(profile.HiddenTags))
	for tg := range profile.HiddenTags {
		hidden = append(hidden, strings.ToLower(strings.TrimLeft(tg, "+-")))
	}
	profile.mu.RUnlock()

	tags := ResolveAliasesInQuery(c.Query("tags"))

	var posts []TournamentPost
	var available int
	var appErr *AppError
	if source == "offline" {
		posts, available = tournamentOffline(db, hidden, tags, size, ratingExcl)
	} else {
		posts, available, appErr = h.tournamentOnline(tags, size, ratingTerms, ratingExcl)
	}
	if appErr != nil {
		AbortWithError(c, appErr)
		return
	}
	if len(posts) < size {
		// Отдельный код: клиенту нужно сказать пользователю, что турнир такого
		// размера не помещается в библиотеку, а не «что-то сломалось».
		c.JSON(http.StatusOK, gin.H{
			"error": ErrTournamentNotEnough.Code, "message": ErrTournamentNotEnough.Message,
			"source": source, "rating": rating, "size": size, "rounds": rounds,
			"available": available, "posts": []TournamentPost{},
			"bracket": emptyTournamentBracket(rounds),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"size": size, "rounds": rounds, "source": source, "rating": rating,
		"available": available, "posts": posts, "bracket": emptyTournamentBracket(rounds),
	})
}

// GetTournamentPosts — GET /api/tournament/posts?ids=1,2,3&source=offline|online
//
// Отдельное API турнира для «Открыть пост» и галереи участников на финале.
// Сервер (а не клиент) решает, откуда взять файл: из библиотеки, если пост уже
// скачан, иначе — адрес источника, который завёрнуто проксирует /api/proxy.
//
// Почему не /api/posts/:id: пост-участник турнира мог не сохраниться в БД (игра
// берёт посты прямо из выдачи источника), а /api/proxy требует уже готовый
// адрес. Собирать этот адрес на клиенте нельзя — именно так «Открыть пост»
// получал «/api/proxy?url=/api/proxy?url=…» и грузил картинку бесконечно.
//
// Ответ: posts — посты в порядке запрошенных id (клиент рисует галерею в этом
// же порядке), missing — id, которых нет ни в библиотеке, ни у источника.
// missing это не ошибка, а «пост больше недоступен»: такой участник помечается
// в галерее и в просмотрщик не отдаётся (иначе вьювер снова покажет спиннер).
func (h *Handler) GetTournamentPosts(c *gin.Context) {
	ids, ok := parseTournamentIDs(c.Query("ids"))
	if !ok {
		AbortWithError(c, ErrInvalidRequest)
		return
	}
	source := c.DefaultQuery("source", "offline")
	if source != "offline" && source != "online" {
		AbortWithError(c, ErrInvalidRequest)
		return
	}

	posts, missing := tournamentPostsByIDs(c.Request.Context(), GetDB(), h.provider(),
		tournamentIDQueryPlanFor(h.provider()), source, ids)
	c.JSON(http.StatusOK, gin.H{"posts": posts, "missing": missing, "source": source})
}

// parseTournamentIDs разбирает список id участников. Мусор (пустые куски,
// отрицательные и повторные id) отбрасывается молча: клиент склеивает строку из
// своей же модели, и падать из-за лишней запятой серверу незачем. А вот запрос
// без единого пригодного id или длиннее максимальной сетки — ошибка запроса:
// молча усечённый список отдал бы галерею не из тех участников.
func parseTournamentIDs(s string) ([]int, bool) {
	ids := make([]int, 0, 8)
	seen := make(map[int]bool, 8)
	for _, part := range strings.Split(s, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		if len(ids) >= tournamentMaxParticipants {
			return nil, false
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// parseTournamentRating разбирает фильтр рейтинга мини-игры: "" (все),
// "sfw" и "nsfw" (кнопка «18+», значение общее с фильтром ленты). Пустое
// значение — обычный режим по умолчанию; всё остальное — ошибка запроса,
// потому что тихо превратившийся в «все» фильтр показал бы пользователю то,
// что он просил не видеть.
func parseTournamentRating(s string) (string, bool) {
	switch s {
	case "", "sfw", "nsfw":
		return s, true
	}
	return "", false
}

// parseTournamentRounds разбирает пресет раундов: 2..5, по умолчанию 3.
// Вне диапазона — ошибка запроса, а не молчаливое усечение: пользователь должен
// узнать, что выбрал недопустимый размер, а не получить «неожиданно» 8 участников.
func parseTournamentRounds(s string) (int, bool) {
	if s == "" {
		return 3, true
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < tournamentMinRounds || v > tournamentMaxRounds {
		return 0, false
	}
	return v, true
}

// tournamentOffline набирает участников из локальной библиотеки.
//
// Требуем score > 0: без оценки буры финал теряет смысл (нечего показать
// пользователю как «правильный ответ»). Скачанные, но неоценённые посты в игру
// не попадают намеренно.
//
// ratingExcl (sfw/18+) уходит в SQL, а не в режет постов в Go: отфильтрованный
// COUNT едет в поле available, и сообщение «в библиотеке только N постов» должно
// считать именно те посты, что остались после фильтра рейтинга.
func tournamentOffline(db *PostDB, hidden []string, tags string, size int, ratingExcl map[string]bool) ([]TournamentPost, int) {
	f := LocalFilter{Tags: tags, Hidden: hidden, Viewed: -1, MinScore: 1, RatingExcl: ratingExcl}
	// Берём с запасом: limit — это потолок страницы, а не гарантия «ровно size
	// годных постов» (часть отсеется дедупом по id).
	const pool = 64
	all, available := db.SearchDownloadedPaged(f, pool, 0)

	out := make([]TournamentPost, 0, len(all))
	seen := make(map[int]bool, len(all))
	for _, p := range all {
		if p.ID <= 0 || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, tournamentPostFromLocal(p))
	}

	// БД отдаёт посты по убыванию id — то есть «свежие». Для турнира это плохо:
	// второй заход дал бы те же последние 64 поста, и пользователь узнавал бы
	// участников. Перемешиваем и берём size случайных.
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	if len(out) > size {
		out = out[:size]
	}
	return out, available
}

func tournamentPostFromLocal(p *Post) TournamentPost {
	thumb := "/api/thumb/" + strconv.Itoa(p.ID)
	if p.FileType == "video" && p.PreviewURL != "" {
		// У видео локальной миниатюры может быть нет — отдаём превью через прокси.
		thumb = "/api/proxy?url=" + url.QueryEscape(p.PreviewURL) + "&kind=preview"
	}
	return TournamentPost{
		ID: p.ID, Tags: p.Tags, Score: p.Score, Rating: p.Rating,
		FileType: p.FileType, Width: p.Width, Height: p.Height,
		Downloaded: true, Thumb: thumb, FileURL: p.FileURL,
		PreviewURL: p.PreviewURL, FileSize: p.FileSize, Blurhash: p.Blurhash,
		Source: p.Source,
	}
}

// tournamentIDQueryPlan — как спросить у источника конкретные посты: пакетно
// (id:1,2,3 — один запрос на всю галерею) или по одному (id:N). План вынесен в
// отдельную функцию и передаётся аргументом: тесты подставляют свой стаб и
// проходят обе ветки, не поднимая боевой booruClient.
type tournamentIDQueryPlan struct {
	batch  bool
	prefix string
}

func tournamentIDQueryPlanFor(prov Provider) tournamentIDQueryPlan {
	if bc, ok := prov.(*booruClient); ok {
		// Списки id понимают rule34/gelbooru; safebooru их игнорирует и отдаёт
		// свежую выдачу, поэтому спрашиваем у него строго по одному id.
		return tournamentIDQueryPlan{batch: bc.spec.batchIDs, prefix: "id:"}
	}
	return tournamentIDQueryPlan{} // спрашивать нечем
}

// tournamentPostsByIDs собирает посты участников «как для вьювера»: локальная
// копия, если она есть, иначе источник. Возвращает посты в порядке запрошенных
// id и список недоступных id.
//
// Зависимости переданы аргументами (а не взяты из глобалов), чтобы поведение
// можно было проверить юнит-тестом без поднятия HTTP-сервера — так же устроены
// tournamentOffline/tournamentOnline.
func tournamentPostsByIDs(ctx context.Context, db *PostDB, prov Provider, plan tournamentIDQueryPlan, source string, ids []int) ([]gin.H, []int) {
	byID := make(map[int]gin.H, len(ids))
	pending := make([]int, 0, len(ids))
	for _, id := range ids {
		// Запись с одной только миниатюрой бесполезна: вьювер попросит файл,
		// не получит его и повесит спиннер. Такие id идут дальше — к источнику.
		if p := db.Get(id); tournamentPostViewable(p) {
			byID[id] = tournamentViewerPost(p)
			continue
		}
		pending = append(pending, id)
	}

	if len(pending) > 0 && source == "online" && prov != nil {
		for id, p := range tournamentFetchByIDs(ctx, prov, plan, pending) {
			byID[id] = tournamentPostFromRemote(p)
		}
	}

	out := make([]gin.H, 0, len(ids))
	missing := make([]int, 0)
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			out = append(out, p)
			continue
		}
		missing = append(missing, id)
	}
	return out, missing
}

// tournamentPostViewable — пост годится для просмотрщика: файл уже скачан или
// известно, откуда его взять.
func tournamentPostViewable(p *Post) bool {
	return p != nil && (p.Downloaded || p.FileURL != "")
}

// tournamentViewerPost — локальная запись в том же виде, в каком её отдаёт
// /api/posts: downloaded=true отправит вьювер на /api/file/:id, и в сеть он не
// полезет вовсе.
func tournamentViewerPost(p *Post) gin.H {
	return gin.H{
		"id": p.ID, "tags": p.Tags, "file_url": p.FileURL, "preview_url": p.PreviewURL,
		"file_type": p.FileType, "width": p.Width, "height": p.Height,
		"file_size": p.FileSize, "score": p.Score, "rating": p.Rating,
		"downloaded": p.Downloaded, "source": p.Source, "blurhash": p.Blurhash,
		"thumb": "/api/thumb/" + strconv.Itoa(p.ID),
	}
}

// tournamentPostFromRemote — пост источника без проксирования: сырые адреса,
// downloaded=false. Прокси подставит /api/proxy по запросу вьювера.
func tournamentPostFromRemote(p Rule34Post) gin.H {
	return gin.H{
		"id": p.ID, "tags": p.Tags, "file_url": p.FileURL, "preview_url": p.PreviewURL,
		"sample_url": p.SampleURL, "file_type": p.FileType, "width": p.Width,
		"height": p.Height, "file_size": p.FileSize, "score": p.Score,
		"rating": p.Rating, "downloaded": false, "source": p.Source,
	}
}

// tournamentOnline набирает участников с источника ОДНИМ запросом.
//
// Важно: не /api/random — тот берёт ровно один пост за запрос, и сетка на 32
// участника превратилась бы в 32 последовательных запроса (десятки секунд).
// Ограничение offset у gelbooru/danbooru: offset <= 20000. При size=32
// держим безопасный потолок страниц (<= 400), чтобы не получить от источника
// текстовый отказ («Too deep») вместо JSON.
//
// ratingTerms уходят в запрос метатегами (сайт фильтрует сам), а ratingExcl
// повторяет фильтр локально: сайты обрезают хвост запроса по MaxQueryLen и
// могли бы прислать посты не того рейтинга.
func (h *Handler) tournamentOnline(tags string, size int, ratingTerms []string, ratingExcl map[string]bool) ([]TournamentPost, int, *AppError) {
	prov := h.provider()
	if prov == nil {
		return nil, 0, ErrProviderUnavailable
	}
	// Метатеги рейтинга — в начало запроса, как в обычном поиске: они короткие
	// и должны пережить обрезку хвоста.
	if len(ratingTerms) > 0 {
		tags = strings.TrimSpace(strings.Join(ratingTerms, " ") + " " + tags)
	}

	maxPage := 18000 / size
	if tags != "" {
		if maxPage > 10 {
			maxPage = 10
		}
	} else if maxPage > 400 {
		maxPage = 400
	}
	if maxPage < 1 {
		maxPage = 1
	}

	// keep — посты, прошедшие рейтинг. И проверку «меньше size», и сравнение с
	// fallback считаем по ней: иначе страница из одних general-постов выглядела
	// бы полной и подменялась бы вслепую.
	keep := func(in []Rule34Post) []Rule34Post {
		if len(ratingExcl) == 0 {
			return in
		}
		out := in[:0:0] // новый срез: in может прийти из кэша провайдера
		for _, p := range in {
			if !ratingExcl[strings.ToLower(p.Rating)] {
				out = append(out, p)
			}
		}
		return out
	}

	page := 1 + rand.Intn(maxPage)
	raw, err := prov.SearchPosts(tags, page, size, 0)
	raw = keep(raw)
	if (err != nil || len(raw) < size) && page != 1 {
		// Fallback к началу выдачи: если случайная страница оказалась пустой или за краем
		if fallback, ferr := prov.SearchPosts(tags, 1, size, 0); ferr == nil {
			if fb := keep(fallback); len(fb) > len(raw) {
				raw = fb
				err = nil
			}
		}
	}
	if err != nil {
		log.Printf("[tournament] online search failed: tags=%q page=%d size=%d err=%v", tags, page, size, err)
		return nil, 0, ErrProviderUnavailable
	}
	db := GetDB()
	out := make([]TournamentPost, 0, len(raw))
	for _, p := range raw {
		if p.PreviewURL == "" && p.SampleURL == "" && p.FileURL == "" {
			continue // без картинки сравнивать нечего
		}
		// Если пост есть локально — отдаём локальную миниатюру: она не зависит
		// от сети и грузится мгновенно.
		if ex := db.Get(p.ID); ex != nil && ex.ThumbPath != "" {
			out = append(out, tournamentPostFromLocal(ex))
			continue
		}
		thumb := "/api/proxy?url=" + url.QueryEscape(p.PreviewURL) + "&kind=preview"
		if p.PreviewURL == "" {
			thumb = "/api/proxy?url=" + url.QueryEscape(p.SampleURL) + "&kind=preview"
		}
		out = append(out, TournamentPost{
			ID: p.ID, Tags: p.Tags, Score: p.Score, Rating: p.Rating,
			FileType: p.FileType, Width: p.Width, Height: p.Height,
			Downloaded: false, Thumb: thumb, FileURL: p.FileURL,
			PreviewURL: p.PreviewURL, SampleURL: p.SampleURL,
			FileSize: p.FileSize, Source: p.Source,
		})
	}
	return out, len(raw), nil
}

// tournamentFetchByIDs спрашивает у источника конкретные посты.
//
// Пакетным запросом (id:1,2,3) умеют отвечать rule34/gelbooru — это один
// запрос на всю галерею; остальным достаётся одиночный id:N с ограниченным
// параллелизмом, как во вкладках профиля (вкладка «Лайки» — те же сотни id).
//
// Сайт может проигнорировать список id и вернуть свежую выдачу (так ведёт себя
// safebooru). Такой ответ отбрасывается сверкой с запрошенными id: иначе в
// галерею турнира попали бы посты, которых в турнире нет.
func tournamentFetchByIDs(ctx context.Context, prov Provider, plan tournamentIDQueryPlan, ids []int) map[int]Rule34Post {
	want := make(map[int]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	found := make(map[int]Rule34Post, len(ids))

	if plan.prefix == "" {
		// Источнику нечем задать вопрос по id: участники останутся missing, и
		// клиент скажет «пост недоступен» вместо вечной загрузки.
		return found
	}

	ctx, cancel := context.WithTimeout(ctx, idFetchBudget)
	defer cancel()

	if plan.batch {
		tags := make([]string, len(ids))
		for i, id := range ids {
			tags[i] = strconv.Itoa(id)
		}
		posts, err := searchPostsCtx(ctx, prov, plan.prefix+strings.Join(tags, ","), 1, len(ids), 0)
		if err != nil {
			log.Printf("[tournament] выборка по id у %s не удалась: %v", prov.Name(), err)
			return found
		}
		for _, p := range posts {
			if want[p.ID] {
				found[p.ID] = p
			}
		}
		return found
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			posts, err := searchPostsCtx(ctx, prov, plan.prefix+strconv.Itoa(id), 1, 1, 0)
			if err != nil {
				return
			}
			for _, p := range posts {
				if p.ID == id {
					mu.Lock()
					found[id] = p
					mu.Unlock()
					return
				}
			}
		}(id)
	}
	wg.Wait()
	return found
}

// emptyTournamentBracket — пустая сетка: rounds[r] содержит size>>(r+1) пар,
// -1 в паре означает «победитель пока не определён».
//
// Сетка — обычные срезы [][]int по уровням: уровень 0 это первый круг,
// последний уровень — финал. Клиент разыгрывает её локально, сервер хранить
// состояние турнира не обязан.
func emptyTournamentBracket(rounds int) [][][]int {
	bracket := make([][][]int, 0, rounds)
	for r := 0; r < rounds; r++ {
		pairs := 1 << (rounds - r - 1)
		level := make([][]int, pairs)
		for i := range level {
			level[i] = []int{-1, -1}
		}
		bracket = append(bracket, level)
	}
	return bracket
}
