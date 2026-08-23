package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
}

type PostDB struct {
	mu      sync.RWMutex
	posts   map[int]*Post
	path    string
	dirty   bool
	saveCh  chan struct{}
	closeCh chan struct{}
	done    chan struct{}
}

var (
	postDB *PostDB
	dbOnce sync.Once
)

const minSaveInterval = 3 * time.Second

func GetDB() *PostDB {
	dbOnce.Do(func() {
		postDB = NewPostDB("data/db.json")
	})
	return postDB
}

func NewPostDB(path string) *PostDB {
	db := &PostDB{
		posts:   make(map[int]*Post),
		path:    path,
		saveCh:  make(chan struct{}, 1),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	db.load()
	go db.autoSave()
	return db
}

func (db *PostDB) load() {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return
	}
	var posts []*Post
	if err := json.Unmarshal(data, &posts); err != nil {
		return
	}
	for _, p := range posts {
		db.posts[p.ID] = p
	}
}

func (db *PostDB) Save() error {
	db.mu.Lock()
	posts := make([]*Post, 0, len(db.posts))
	for _, p := range db.posts {
		cp := *p
		posts = append(posts, &cp)
	}
	db.dirty = false
	db.mu.Unlock()

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].ID < posts[j].ID
	})

	// Без MarshalIndent: на текущем объёме (десятки МБ) отступы удваивают
	// и размер файла, и работу CPU/аллокатора при каждом автосейве.
	// Читается обратно json.Unmarshal'ом независимо от формата.
	data, err := json.Marshal(posts)
	if err != nil {
		return err
	}
	return atomicWriteFile(db.path, data, 0644)
}

// BumpSave откладывает запись: скачивания завершаются часто, а перезаписывать
// весь файл на каждый файл накладно. Реальная запись выполняется не чаще раза
// в minSaveInterval (см. autoSave), плюс безусловный сейв при завершении и из
// основных хендлеров.
func (db *PostDB) BumpSave() {
	select {
	case db.saveCh <- struct{}{}:
	default:
	}
}

func (db *PostDB) autoSave() {
	defer close(db.done)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	debounce := time.NewTimer(minSaveInterval)
	if !debounce.Stop() {
		select {
		case <-debounce.C:
		default:
		}
	}
	pending := false
	lastSave := time.Now()
	for {
		select {
		case <-db.saveCh:
			if !pending {
				d := minSaveInterval - time.Since(lastSave)
				if d < 0 {
					d = 0
				}
				debounce.Reset(d)
				pending = true
			}
		case <-debounce.C:
			pending = false
			db.mu.RLock()
			dirty := db.dirty
			db.mu.RUnlock()
			if dirty {
				db.Save()
			}
			lastSave = time.Now()
		case <-ticker.C:
			db.mu.RLock()
			dirty := db.dirty
			db.mu.RUnlock()
			if dirty {
				db.Save()
				lastSave = time.Now()
				if pending {
					pending = false
					if !debounce.Stop() {
						select {
						case <-debounce.C:
						default:
						}
					}
				}
			}
		case <-db.closeCh:
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			db.Save()
			return
		}
	}
}

func (db *PostDB) Close() {
	close(db.closeCh)
	<-db.done
}

func (db *PostDB) AddOrUpdate(post *Post) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.posts[post.ID] = post
	db.dirty = true
}

func (db *PostDB) UpsertMeta(p *Post) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	if old, ok := db.posts[p.ID]; ok {
		if old.Tags == p.Tags &&
			old.FileURL == p.FileURL &&
			old.PreviewURL == p.PreviewURL &&
			old.FileType == p.FileType &&
			old.Width == p.Width &&
			old.Height == p.Height &&
			old.FileSize == p.FileSize &&
			old.Score == p.Score &&
			old.Rating == p.Rating {
			return false
		}
		old.Tags = p.Tags
		old.FileURL = p.FileURL
		old.PreviewURL = p.PreviewURL
		old.FileType = p.FileType
		old.Width = p.Width
		old.Height = p.Height
		old.FileSize = p.FileSize
		old.Score = p.Score
		old.Rating = p.Rating
		db.dirty = true
		return true
	}
	cp := *p
	db.posts[p.ID] = &cp
	db.dirty = true
	return true
}

// Get возвращает копию поста: возврат живого указателя после снятия RLock
// позволял мутаторам (SetDownloaded, BatchRename) и читателям гоняться
// за одним и тем же объектом.
func (db *PostDB) Get(id int) *Post {
	db.mu.RLock()
	defer db.mu.RUnlock()
	p, ok := db.posts[id]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

func (db *PostDB) GetDownloaded() []*Post {
	db.mu.RLock()
	defer db.mu.RUnlock()
	var result []*Post
	for _, p := range db.posts {
		if p.Downloaded {
			cp := *p
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID > result[j].ID
	})
	return result
}

func (db *PostDB) SearchDownloaded(tags string) []*Post {
	db.mu.RLock()
	defer db.mu.RUnlock()

	groups := strings.Split(tags, "|")
	var result []*Post
	for _, p := range db.posts {
		if !p.Downloaded {
			continue
		}
		postTags := strings.Fields(strings.ToLower(p.Tags))
		tagSet := make(map[string]bool, len(postTags))
		for _, pt := range postTags {
			tagSet[pt] = true
		}
		matched := false
		for _, g := range groups {
			tagList := strings.Fields(strings.ToLower(g))
			if len(tagList) == 0 {
				continue
			}
			all := true
			for _, t := range tagList {
				if !tagSet[t] {
					all = false
					break
				}
			}
			if all {
				matched = true
				break
			}
		}
		if matched {
			cp := *p
			result = append(result, &cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID > result[j].ID
	})
	return result
}

func (db *PostDB) PostExists(id int) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	_, exists := db.posts[id]
	return exists
}

func (db *PostDB) SetDownloaded(id int, filePath, thumbPath string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if p, ok := db.posts[id]; ok {
		p.Downloaded = true
		p.FilePath = filePath
		p.ThumbPath = thumbPath
		db.dirty = true
	}
}

func (db *PostDB) UnsetDownloaded(id int) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if p, ok := db.posts[id]; ok {
		p.Downloaded = false
		p.FilePath = ""
		p.ThumbPath = ""
		db.dirty = true
	}
}

func (db *PostDB) UnsetDownloadedByPath(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, p := range db.posts {
		if p.Downloaded && p.FilePath != "" {
			if pPath, err := filepath.Abs(p.FilePath); err == nil && pPath == abs {
				p.Downloaded = false
				p.FilePath = ""
				db.dirty = true
			}
		}
	}
}

func (db *PostDB) CleanNonDownloaded() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	count := 0
	for id, p := range db.posts {
		if !p.Downloaded {
			delete(db.posts, id)
			count++
		}
	}
	return count
}

func (db *PostDB) Stats() map[string]int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	total := len(db.posts)
	downloaded := 0
	for _, p := range db.posts {
		if p.Downloaded {
			downloaded++
		}
	}
	return map[string]int{
		"total":      total,
		"downloaded": downloaded,
	}
}

func (db *PostDB) SuggestTagsLocal(prefix string, limit int) []TagSuggestion {
	db.mu.RLock()
	defer db.mu.RUnlock()
	freq := make(map[string]int)
	lower := strings.ToLower(prefix)
	for _, p := range db.posts {
		if !p.Downloaded {
			continue
		}
		for _, t := range strings.Fields(p.Tags) {
			if strings.HasPrefix(strings.ToLower(t), lower) {
				freq[t]++
			}
		}
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range freq {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	if limit <= 0 {
		limit = 10
	}
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	out := make([]TagSuggestion, len(sorted))
	for i, kv := range sorted {
		out[i] = TagSuggestion{Label: kv.k, Value: kv.k, Count: kv.v}
	}
	return out
}

// AllTagsFreq считает частотность тегов по всем постам в БД (просмотренные
// и скачанные) — приближение «популярности» тега для штрафа в рекомендациях.
func (db *PostDB) AllTagsFreq() map[string]int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	freq := make(map[string]int)
	for _, p := range db.posts {
		if p.Tags == "" {
			continue
		}
		for _, t := range strings.Fields(p.Tags) {
			freq[t]++
		}
	}
	return freq
}

func (db *PostDB) TagStats(limit int) map[string]int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	freq := make(map[string]int)
	for _, p := range db.posts {
		if !p.Downloaded {
			continue
		}
		for _, t := range strings.Fields(p.Tags) {
			freq[t]++
		}
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range freq {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	if limit <= 0 {
		limit = 50
	}
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	out := make(map[string]int, len(sorted))
	for _, kv := range sorted {
		out[kv.k] = kv.v
	}
	return out
}

func (p *Post) String() string {
	maxLen := min(len(p.Tags), 100)
	return fmt.Sprintf("[%d] %s (%.1f MB) - %s", p.ID, p.FileType, float64(p.FileSize)/1024/1024, p.Tags[:maxLen])
}
