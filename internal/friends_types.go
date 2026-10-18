package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	friendsDir        = "data/accounts/friends"
	friendsLegacyFile = "data/friends.json"
	// friendCodePrefix — версия формата кода друга. Меняем при несовместимых
	// изменениях, чтобы старый код не распарсился молча в мусор.
	friendCodePrefix = "briefly-friend-v1"
	// Капы на входящие данные: друг — недоверенный источник, и без ограничений
	// один запрос может вогнать в память мегабайты.
	maxFriends       = 64
	maxFriendLikes   = 5000
	maxFriendCmts    = 500
	maxFriendColls   = 200
	maxFriendCollsPC = 2000
	maxFriendText    = 500
	maxFriendName    = 64
)

var (
	ErrFriendCodeInvalid = errors.New("код друга не распознан")
	ErrFriendSelf        = errors.New("нельзя добавить самого себя")
	ErrFriendExists      = errors.New("этот друг уже добавлен")
	ErrFriendNotFound    = errors.New("друг не найден")
	ErrFriendAuth        = errors.New("неверный ключ друга")
	ErrFriendBody        = errors.New("некорректные данные от друга")
)

// Friend — запись в списке друзей: куда ходить и каким ключом.
type Friend struct {
	// URL — адрес инстанса друга, включая схему ("https://26.4.5.6:3000").
	URL string `json:"url"`
	// Key — ключ ДРУГА, выданный им в его friend-коде. Им мы авторизуемся у
	// друга в обе стороны: и чтобы забрать его данные, и чтобы отдать свои.
	// Отдельного «моего ключа для друга» не нужно: симметрия и так работает
	// (у друга в его записи лежит наш ключ, он приходит с его запросом).
	Key      string `json:"key"`
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Username string `json:"username,omitempty"`
	AddedAt  string `json:"added_at"`
	LastSync string `json:"last_sync,omitempty"`
	// LastError — машиночитаемый код для UI (см. friendStatusErr), а не текст:
	// показывать пользователю «friend_no_back» бесполезно.
	LastError string `json:"last_error,omitempty"`
	// Snapshot — что мы последний раз получили от друга. Нужен, чтобы показать
	// его профиль: лайки друга в НАШ профиль не вливаются (иначе они были бы
	// неотличимы от наших и показывались в «Моих лайках»), поэтому его лайки,
	// скрытия и теги живут только здесь — как зеркало его вкусов для UI.
	Snapshot *FriendSnapshot `json:"snapshot,omitempty"`
}

// FriendSnapshot — профиль друга на момент последнего обмена: его лайки,
// скрытия и теги. Только чтение: это зеркало его вкусов, а не редактируемая
// копия. Хранится рядом с записью друга, чтобы не плодить файлы.
type FriendSnapshot struct {
	// SyncedAt — когда пришли эти данные (RFC3339).
	SyncedAt string `json:"synced_at"`
	// Nickname — как друг представился в последнем обмене (может отличаться
	// от Nickname в записи: он обновляется при каждом успешном обмене).
	Nickname string `json:"nickname,omitempty"`
	// Likes — id постов, которые другу понравились.
	Likes []int `json:"likes,omitempty"`
	// Disliked — id постов, которые друг скрыл (у нас это кнопка «Дизлайк»).
	Disliked []int `json:"disliked,omitempty"`
	// FavTags — его избранные теги.
	FavTags []string `json:"fav_tags,omitempty"`
	// DislikedTags — теги с обратной связью «не интересно», по убыванию.
	DislikedTags []friendDislikedTag `json:"disliked_tags,omitempty"`
	// Collections — его альбомы (имя и число постов).
	Collections []FriendSnapshotColl `json:"collections,omitempty"`
	// CommentPosts — id постов, где друг оставил комментарии.
	CommentPosts []int `json:"comment_posts,omitempty"`
}

// FriendSnapshotColl — коллекция друга в снимке: имя и сколько постов.
// Сами посты не дублируем — их id и так есть в его лайках/коллекциях.
type FriendSnapshotColl struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// ID — устойчивый идентификатор записи (в UI, в SSE, в логах). Считаем от
// нормализованного адреса: повторное добавление того же адреса не плодит дубли.
func (f *Friend) ID() string {
	sum := sha256.Sum256([]byte(f.URL))
	return hex.EncodeToString(sum[:])[:12]
}

// FriendsFile — содержимое файла друзей пользователя. Друзья, как и лайки,
// per-user аспект, поэтому живут рядом с профилем.
type FriendsFile struct {
	Version int      `json:"version"`
	MyKey   string   `json:"my_key"`
	MyName  string   `json:"my_name,omitempty"`
	Friends []Friend `json:"friends"`
}

// FriendStore — потокобезопасное хранилище списка друзей.
type FriendStore struct {
	mu   sync.RWMutex
	path string
	byID map[string]*Friend
}

// ── Что именно обмениваем ────────────────────────────────────────────────────

// friendLike — отметка «нравится» с временем (порядок лайков важен:
// сортировка вкладки «Лайки» идёт по liked_at).
type friendLike struct {
	PostID  int   `json:"post_id"`
	LikedAt int64 `json:"liked_at,omitempty"`
}

// friendCollection — коллекция друга. Совпадает с нашей Collection, но id
// переезжает: коллекции объединяются по имени, иначе у каждого свой id и
// общий альбом не собрался бы.
type friendCollection struct {
	Name      string `json:"name"`
	Posts     []int  `json:"posts,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// friendComment — комментарий друга. Username здесь чужой логин, поэтому в
// нашей БД он хранится с префиксом instance (см. friendCommentUsername),
// иначе «vasya» с чужого сайта слился бы с нашим «vasya».
type friendComment struct {
	PostID    int    `json:"post_id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// friendPayload — то, чем два инстанса обмениваются. Асимметрично урезанное:
// чужие скрытия и рекомендации нам не нужны (это личное), а вот лайки,
// коллекции и комментарии — ровно то, за что мы зовём «друзей».
type friendPayload struct {
	Version     int                `json:"version"`
	App         string             `json:"app"`
	Instance    string             `json:"instance"`
	User        string             `json:"user"`
	Nickname    string             `json:"nickname,omitempty"`
	Avatar      string             `json:"avatar,omitempty"`
	SyncedAt    string             `json:"synced_at"`
	Likes       []friendLike       `json:"likes"`
	Collections []friendCollection `json:"collections,omitempty"`
	// HiddenPosts — посты, которые друг скрыл. У нас кнопка «Скрыть» (она же
	// «Дизлайк» в интерфейсе) пишет и в hidden_posts, и в RecDisliked по тегам,
	// поэтому «дизлайки» друга = скрытые посты + штрафные теги.
	HiddenPosts []int    `json:"hidden_posts,omitempty"`
	FavTags     []string `json:"fav_tags,omitempty"`
	HiddenTags  []string `json:"hidden_tags,omitempty"`
	// DislikedTags — теги с обратной связью «не интересно» (RecDisliked),
	// по убыванию счётчика: их показываем в профиле друга.
	DislikedTags []friendDislikedTag `json:"disliked_tags,omitempty"`
	// Comments — комментарии, которые друг оставил у нас.
	Comments []friendComment `json:"comments,omitempty"`
}

// friendDislikedTag — тег, которому друг отдавал «голоса против», со счётчиком.
type friendDislikedTag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

const friendPayloadVersion = 1

// maxFriendDislikedTags ограничивает «голоса против»: больше сотни тегов в
// профиле читать невозможно.
const maxFriendDislikedTags = 100

// mergeStats — что реально добавилось при обмене (для ответа и логов).
type mergeStats struct {
	Likes       int `json:"likes"`
	Collections int `json:"collections"`
	Comments    int `json:"comments"`
}

func (m mergeStats) total() int { return m.Likes + m.Collections + m.Comments }

// friendClientTimeout короче обычного: синк идёт по требованию и не должен
// висеть на мёртвом друге дольше, чем живёт запрос пользователя.
const friendClientTimeout = 20 * time.Second

// Потолки обмена по требованию (фонового цикла больше нет):
//
//	friendSyncBudget         — кнопка «Обменяться»/«Обновить»: пользователь сам
//	                           ждёт, можно дать полный обмен.
//	friendProfileOpenBudget  — открытие профиля друга: страница должна
//	                           открыться быстро. Не дождались — отдаём прошлый
//	                           снимок, а строка «обмен» покажет причину.
const (
	friendSyncBudget        = 12 * time.Second
	friendProfileOpenBudget = 8 * time.Second
)
