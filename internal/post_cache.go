package internal

import (
	"container/list"
	"sync"
	"time"
)

type lruItem struct {
	id        int
	post      *Post
	expiresAt time.Time
}

// PostLRUCache — потокобезопасный LRU-кэш для структуры *Post с ограничением по размеру и TTL.
type PostLRUCache struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[int]*list.Element
	ll       *list.List
}

func NewPostLRUCache(capacity int, ttl time.Duration) *PostLRUCache {
	if capacity <= 0 {
		capacity = 2048
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &PostLRUCache{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[int]*list.Element, capacity),
		ll:       list.New(),
	}
}

// Get возвращает изолированную копию поста из кэша либо nil, если записи нет или её TTL истёк.
func (c *PostLRUCache) Get(id int) *Post {
	if c == nil || id <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[id]
	if !ok {
		return nil
	}
	item := el.Value.(*lruItem)
	if time.Now().After(item.expiresAt) {
		c.ll.Remove(el)
		delete(c.items, id)
		return nil
	}
	c.ll.MoveToFront(el)
	cp := *item.post
	return &cp
}

// Put сохраняет копию поста в кэш с обновлением TTL и вытеснением старых записей.
func (c *PostLRUCache) Put(p *Post) {
	if c == nil || p == nil || p.ID <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	cp := *p
	now := time.Now()
	if el, ok := c.items[p.ID]; ok {
		item := el.Value.(*lruItem)
		item.post = &cp
		item.expiresAt = now.Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}

	for c.ll.Len() >= c.capacity {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*lruItem).id)
	}

	el := c.ll.PushFront(&lruItem{
		id:        p.ID,
		post:      &cp,
		expiresAt: now.Add(c.ttl),
	})
	c.items[p.ID] = el
}

// Invalidate удаляет пост с указанным id из кэша при обновлении или удалении.
func (c *PostLRUCache) Invalidate(id int) {
	if c == nil || id <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[id]; ok {
		c.ll.Remove(el)
		delete(c.items, id)
	}
}

// Clear полностью очищает кэш.
func (c *PostLRUCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[int]*list.Element, c.capacity)
	c.ll.Init()
}
