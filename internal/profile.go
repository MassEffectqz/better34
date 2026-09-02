package internal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

type Profile struct {
	mu          sync.RWMutex
	path        string
	LikedPosts  map[int]bool    `json:"liked_posts"`
	LikedAt     map[int]int64   `json:"liked_at,omitempty"`
	HiddenPosts map[int]bool    `json:"hidden_posts"`
	Presets     []QueryPreset   `json:"presets"`
	FavTags     map[string]bool `json:"fav_tags"`
	HiddenTags  map[string]bool `json:"hidden_tags"`
	RecDisliked map[string]int  `json:"rec_disliked,omitempty"`
	Collections []Collection    `json:"collections,omitempty"`
}

// Collection — именованный альбом постов с сохранённым порядком добавления.
type Collection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Posts     []int  `json:"posts"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

type QueryPreset struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Query      string   `json:"query"`
	Kind       string   `json:"kind,omitempty"` // "query" (default) | "tags"
	Tags       []string `json:"tags,omitempty"` // legacy: одна группа тегов (hidden/fav)
	HiddenTags []string `json:"hidden_tags,omitempty"`
	FavTags    []string `json:"fav_tags,omitempty"`
}

func newPresetID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Profile) ensurePresetIDs() {
	for i := range p.Presets {
		if p.Presets[i].ID == "" {
			p.Presets[i].ID = newPresetID()
		}
	}
}

var (
	profile     *Profile
	profileOnce sync.Once
)

func GetProfile() *Profile {
	profileOnce.Do(func() {
		profile = NewProfile("data/profile.json")
	})
	return profile
}

func NewProfile(path string) *Profile {
	p := &Profile{
		path:        path,
		LikedPosts:  make(map[int]bool),
		LikedAt:     make(map[int]int64),
		HiddenPosts: make(map[int]bool),
		Presets:     []QueryPreset{},
		FavTags:     make(map[string]bool),
		HiddenTags:  make(map[string]bool),
		RecDisliked: make(map[string]int),
		Collections: []Collection{},
	}
	p.load()
	return p
}

func (p *Profile) load() {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, p)
	if p.LikedAt == nil {
		p.LikedAt = make(map[int]int64)
	}
	if p.RecDisliked == nil {
		p.RecDisliked = make(map[string]int)
	}
	if p.Collections == nil {
		p.Collections = []Collection{}
	}
	p.ensurePresetIDs()
}

// AddRecDisliked накапливает обратную связь «не интересно»: теги поста,
// скрытого из ленты рекомендаций, получают штраф в следующих подборках.
func (p *Profile) AddRecDisliked(tags []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tags {
		if t == "" {
			continue
		}
		p.RecDisliked[t]++
	}
}

func (p *Profile) Save() error {
	p.mu.Lock()
	p.ensurePresetIDs()
	p.mu.Unlock()
	p.mu.RLock()
	defer p.mu.RUnlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(p.path, data, 0600)
}

func (p *Profile) ToggleLike(postID int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.LikedPosts[postID] {
		delete(p.LikedPosts, postID)
		delete(p.LikedAt, postID)
		return false
	}
	p.LikedPosts[postID] = true
	p.LikedAt[postID] = time.Now().Unix()
	// Лайк снимает скрытие — пост не может быть одновременно лайкнутым и скрытым.
	delete(p.HiddenPosts, postID)
	return true
}

func (p *Profile) ToggleHide(postID int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.HiddenPosts[postID] {
		delete(p.HiddenPosts, postID)
		return false
	}
	p.HiddenPosts[postID] = true
	return true
}

func (p *Profile) ToggleFavTag(tag string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FavTags[tag] {
		delete(p.FavTags, tag)
		return false
	}
	p.FavTags[tag] = true
	return true
}

func (p *Profile) ToggleHiddenTag(tag string) bool {
	tag = strings.TrimLeft(tag, "+-")
	if tag == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.HiddenTags[tag] {
		delete(p.HiddenTags, tag)
		return false
	}
	p.HiddenTags[tag] = true
	return true
}

func (p *Profile) AddPreset(name, query, kind string, tags, hiddenTags, favTags []string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "query"
	}
	switch kind {
	case "query":
		if query == "" {
			return
		}
	case "hidden", "fav": // legacy: одна группа тегов
		if len(tags) == 0 {
			return
		}
		if kind == "hidden" {
			hiddenTags = tags
		} else {
			favTags = tags
		}
		kind = "tags"
	case "tags":
		if len(tags) > 0 && len(hiddenTags) == 0 && len(favTags) == 0 {
			hiddenTags = tags
		}
		if len(hiddenTags)+len(favTags) == 0 {
			return
		}
	default:
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensurePresetIDs()
	for i := range p.Presets {
		if strings.EqualFold(p.Presets[i].Name, name) {
			p.Presets[i].Query = query
			p.Presets[i].Kind = kind
			p.Presets[i].Tags = tags
			p.Presets[i].HiddenTags = hiddenTags
			p.Presets[i].FavTags = favTags
			return
		}
	}
	p.Presets = append(p.Presets, QueryPreset{ID: newPresetID(), Name: name, Query: query, Kind: kind, Tags: tags, HiddenTags: hiddenTags, FavTags: favTags})
}

// ApplyTagPreset заменяет набор тегов указанного типа снимком из пресета.
// Возвращает -1, если пресет не найден.
func (p *Profile) ApplyTagPreset(id, typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Presets {
		if p.Presets[i].ID != id {
			continue
		}
		pr := &p.Presets[i]
		hidden := pr.HiddenTags
		if len(hidden) == 0 && pr.Kind == "hidden" {
			hidden = pr.Tags
		}
		fav := pr.FavTags
		if len(fav) == 0 && pr.Kind == "fav" {
			fav = pr.Tags
		}
		switch typ {
		case "hidden":
			p.HiddenTags = make(map[string]bool, len(hidden))
			for _, t := range hidden {
				p.HiddenTags[t] = true
			}
			return len(p.HiddenTags)
		case "fav":
			p.FavTags = make(map[string]bool, len(fav))
			for _, t := range fav {
				p.FavTags[t] = true
			}
			return len(p.FavTags)
		}
		return 0
	}
	return -1
}

func (p *Profile) UpdatePreset(id, name, query string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Presets {
		if p.Presets[i].ID == id {
			if t := strings.TrimSpace(name); t != "" {
				p.Presets[i].Name = t
			}
			if t := strings.TrimSpace(query); t != "" {
				p.Presets[i].Query = t
			}
			return true
		}
	}
	return false
}

func (p *Profile) DeletePreset(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Presets {
		if p.Presets[i].ID == id {
			p.Presets = append(p.Presets[:i], p.Presets[i+1:]...)
			return true
		}
	}
	return false
}

func (p *Profile) MovePreset(id string, dir int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Presets {
		if p.Presets[i].ID == id {
			j := i + dir
			if j < 0 || j >= len(p.Presets) {
				return false
			}
			p.Presets[i], p.Presets[j] = p.Presets[j], p.Presets[i]
			return true
		}
	}
	return false
}

// ── Коллекции: именованные группы постов ────────────────────────────────

func (p *Profile) findCollectionLocked(id string) *Collection {
	for i := range p.Collections {
		if p.Collections[i].ID == id {
			return &p.Collections[i]
		}
	}
	return nil
}

// AddCollection создаёт коллекцию; при совпадении имени (без учёта регистра)
// возвращает существующую. ok=false — имя пустое.
func (p *Profile) AddCollection(name string) (Collection, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Collection{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.Collections {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	c := Collection{ID: newPresetID(), Name: name, Posts: []int{}, CreatedAt: time.Now().Unix()}
	p.Collections = append(p.Collections, c)
	return c, true
}

// RenameCollection переименовывает коллекцию. false — не найдена или пустое имя.
func (p *Profile) RenameCollection(id, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.findCollectionLocked(id)
	if c == nil {
		return false
	}
	c.Name = name
	return true
}

func (p *Profile) DeleteCollection(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.Collections {
		if p.Collections[i].ID == id {
			p.Collections = append(p.Collections[:i], p.Collections[i+1:]...)
			return true
		}
	}
	return false
}

// CollectionTogglePost добавляет/убирает пост из коллекции.
// found=false — коллекции с таким id нет.
func (p *Profile) CollectionTogglePost(id string, postID int) (added, found bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.findCollectionLocked(id)
	if c == nil {
		return false, false
	}
	for i, pid := range c.Posts {
		if pid == postID {
			c.Posts = append(c.Posts[:i], c.Posts[i+1:]...)
			return false, true
		}
	}
	c.Posts = append(c.Posts, postID)
	return true, true
}

// CollectionPosts возвращает посты коллекции в порядке добавления.
func (p *Profile) CollectionPosts(id string) ([]int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c := p.findCollectionLocked(id)
	if c == nil {
		return nil, false
	}
	out := make([]int, len(c.Posts))
	copy(out, c.Posts)
	return out, true
}
