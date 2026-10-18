package internal

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type persistedSearchEntry struct {
	Posts []Rule34Post `json:"posts"`
	TS    time.Time    `json:"ts"`
}

const searchCacheTTL = 10 * time.Minute
const searchCacheMax = 512

// searchCacheMaxStale — предельный возраст «устаревшего» ответа, который ещё
// отдаётся мгновенно (SWR) и параллельно освежается фоном; старше — на сеть.
const searchCacheMaxStale = 6 * time.Hour

// booruCache — LRU-кэш поиска с отложенной записью на диск.
// У каждого провайдера свой экземпляр и свой файл.
type booruCache struct {
	mu      sync.Mutex
	m       map[string]searchCacheEntry
	file    string
	saveMu  sync.Mutex
	pending atomic.Bool
}

func newBooruCache(file string, legacyFiles ...string) *booruCache {
	c := &booruCache{m: make(map[string]searchCacheEntry), file: file}
	c.loadFromDisk(legacyFiles...)
	return c
}

func (c *booruCache) get(key string) ([]Rule34Post, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.ts) > searchCacheTTL {
		return nil, false
	}
	out := make([]Rule34Post, len(e.posts))
	copy(out, e.posts)
	return out, true
}

// getStale возвращает закэшированный ответ независимо от возраста — для SWR:
// SearchPosts решает, отдать ли его мгновенно, пока свежесть восстанавливается
// фоном (повторный лист/перезаход не ждёт RTT до CDN).
func (c *booruCache) getStale(key string) ([]Rule34Post, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, time.Time{}, false
	}
	out := make([]Rule34Post, len(e.posts))
	copy(out, e.posts)
	return out, e.ts, true
}

func (c *booruCache) put(key string, posts []Rule34Post) {
	c.mu.Lock()
	if _, ok := c.m[key]; !ok && len(c.m) >= searchCacheMax {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.m {
			if oldest.IsZero() || e.ts.Before(oldest) {
				oldestKey, oldest = k, e.ts
			}
		}
		delete(c.m, oldestKey)
	}
	cp := make([]Rule34Post, len(posts))
	copy(cp, posts)
	c.m[key] = searchCacheEntry{posts: cp, ts: time.Now()}
	c.mu.Unlock()
	c.scheduleSave()
}

func (c *booruCache) loadFromDisk(legacyFiles ...string) {
	files := append([]string{c.file}, legacyFiles...)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var entries map[string]persistedSearchEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			continue
		}
		now := time.Now()
		loaded := 0
		c.mu.Lock()
		for k, e := range entries {
			// Держим и устаревшие: после рестарта они отдадутся мгновенно
			// (SWR) и обновятся фоном, раз клиент их снова запросил.
			if now.Sub(e.TS) > searchCacheMaxStale {
				continue
			}
			if len(c.m) >= searchCacheMax {
				break
			}
			c.m[k] = searchCacheEntry{posts: e.Posts, ts: e.TS}
			loaded++
		}
		c.mu.Unlock()
		if loaded > 0 {
			log.Printf("[search-cache:%s] loaded %d entries from %s", c.file, loaded, f)
		}
		return
	}
}

func (c *booruCache) saveToDisk() {
	now := time.Now()
	c.mu.Lock()
	entries := make(map[string]persistedSearchEntry, len(c.m))
	for k, e := range c.m {
		// Устаревшие (в пределах searchCacheMaxStale) персистим тоже:
		// иначе после рестарта SWR-холодный старт нечем обслужить.
		if now.Sub(e.ts) > searchCacheMaxStale {
			continue
		}
		entries[k] = persistedSearchEntry{Posts: e.posts, TS: e.ts}
	}
	c.mu.Unlock()
	if len(entries) == 0 {
		return
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(c.file), 0755)
	if err := os.WriteFile(c.file, data, 0644); err != nil {
		log.Printf("[search-cache] failed to persist %s: %v", c.file, err)
		return
	}
	log.Printf("[search-cache] persisted %d entries to %s", len(entries), c.file)
}

func (c *booruCache) scheduleSave() {
	c.saveMu.Lock()
	if c.pending.Swap(true) {
		c.saveMu.Unlock()
		return
	}
	c.saveMu.Unlock()
	go func() {
		time.Sleep(15 * time.Second)
		c.pending.Store(false)
		c.saveToDisk()
	}()
}
