package internal

import (
	"sync"
	"testing"
	"time"
)

// Персист счётчиков: save → очистка кэша → load восстанавливает записи
// в пределах TTL; просроченные и нулевые не возвращаются.
func TestTagCountsPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	oldFile := tagCountsFile
	tagCountsFile = dir + "/tag_counts.json"
	t.Cleanup(func() {
		tagCountsFile = oldFile
		tagCountLoadOnce = sync.Once{}
		tagCountCache.Lock()
		tagCountCache.m = make(map[string]int)
		tagCountCache.ts = make(map[string]time.Time)
		tagCountCache.Unlock()
	})

	tagCountsSavePending.Store(false)
	tagCountStore("gelbooru", "touhou", 123456)
	tagCountStore("gelbooru", "old_tag", 42)
	tagCountCache.Lock()
	tagCountCache.ts["gelbooru|old_tag"] = time.Now().Add(-13 * time.Hour) // старше TTL
	tagCountCache.Unlock()

	tagCountsSave()

	tagCountCache.Lock()
	tagCountCache.m = make(map[string]int)
	tagCountCache.ts = make(map[string]time.Time)
	tagCountCache.Unlock()

	tagCountLoadOnce = sync.Once{}
	tagCountsLoad()

	tagCountCache.RLock()
	got := tagCountCache.m["gelbooru|touhou"]
	_, oldOK := tagCountCache.m["gelbooru|old_tag"]
	tagCountCache.RUnlock()

	if got != 123456 {
		t.Errorf("restored count = %d, want 123456", got)
	}
	if oldOK {
		t.Error("expired entry must not be restored")
	}
}

// LRU: обращение к старому элементу продлевает его жизнь — при
// вытеснении уходит «холодный» хвост, а не самый старый по вставке.
func TestProxyCacheLRUEviction(t *testing.T) {
	oldMaxItems, oldMaxBytes := proxyCache.maxItems, proxyCache.maxBytes
	oldMaxItemSize := proxyCache.maxItemSize
	proxyCache.maxItems, proxyCache.maxBytes, proxyCache.maxItemSize = 2, 1<<30, 1<<20
	t.Cleanup(func() {
		proxyCache.Lock()
		proxyCache.m = make(map[string]proxyCacheEntry)
		proxyCache.order = nil
		proxyCache.totalBytes = 0
		proxyCache.Unlock()
		proxyCache.maxItems, proxyCache.maxBytes, proxyCache.maxItemSize = oldMaxItems, oldMaxBytes, oldMaxItemSize
	})

	proxyCache.Lock()
	proxyCache.m = make(map[string]proxyCacheEntry)
	proxyCache.order = nil
	proxyCache.totalBytes = 0
	proxyCache.Unlock()

	proxyCache.store("a", []byte("aaa"), "")
	proxyCache.store("b", []byte("bbb"), "")

	// Читаем a — теперь «холодный» это b.
	if _, ok := proxyCache.get("a"); !ok {
		t.Fatal("a missing after store")
	}
	proxyCache.store("c", []byte("ccc"), "")

	if _, ok := proxyCache.get("a"); !ok {
		t.Error("LRU violated: touched 'a' was evicted instead of 'b'")
	}
	if _, ok := proxyCache.get("b"); ok {
		t.Error("cold 'b' must have been evicted first")
	}
}

// Троттлинг эвиции дискового медиа-кэша: повторные вызовы в пределах
// интервала пропускаются.
func TestMediaCacheEvictThrottled(t *testing.T) {
	withTempMediaCache(t)

	mediaCacheEvictInterval = time.Minute
	lastMediaCacheEvict.Store(0)
	t.Cleanup(func() { mediaCacheEvictInterval = 0 })

	sink := newMediaCacheSink("x")
	sink.Write([]byte("data"))
	sink.commit() // вызвал эвицию, запомнил время

	first := lastMediaCacheEvict.Load()
	if first == 0 {
		t.Fatal("commit must trigger eviction timestamp")
	}

	sink = newMediaCacheSink("y")
	sink.Write([]byte("data"))
	sink.commit() // должен быть пропущен троттлингом

	if got := lastMediaCacheEvict.Load(); got != first {
		t.Error("second evict within interval must be skipped")
	}
}

// Пул чтения БД существует и открывает больше одного соединения:
// тяжёлые SELECT не должны вставать за записями.
func TestPostDBReadWritePools(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/posts.db")
	defer db.Close()

	if db.read == nil {
		t.Fatal("read pool is nil")
	}
	if st := db.read.Stats().MaxOpenConnections; st < 2 {
		t.Errorf("read pool MaxOpenConns = %d, want >= 2", st)
	}
	if st := db.db.Stats().MaxOpenConnections; st != 1 {
		t.Errorf("write pool MaxOpenConns = %d, want 1", st)
	}

	// Сквозная проверка чтения через пул.
	p := &Post{ID: 7, Tags: "tag_a", FileType: "jpg"}
	db.AddOrUpdate(p)
	if got := db.Get(7); got == nil || got.Tags != "tag_a" {
		t.Errorf("read via pool broken: %+v", got)
	}
}
