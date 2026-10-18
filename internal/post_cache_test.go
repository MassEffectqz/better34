package internal

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestPostLRUCacheBasicAndEviction(t *testing.T) {
	c := NewPostLRUCache(2, time.Minute)

	p1 := &Post{ID: 1, Tags: "art1"}
	p2 := &Post{ID: 2, Tags: "art2"}
	p3 := &Post{ID: 3, Tags: "art3"}

	c.Put(p1)
	c.Put(p2)

	if got := c.Get(1); got == nil || got.Tags != "art1" {
		t.Fatalf("expected p1 in cache, got %+v", got)
	}

	// Put p3, evicts p2 (since p1 was recently accessed)
	c.Put(p3)

	if got := c.Get(2); got != nil {
		t.Fatalf("expected p2 to be evicted, got %+v", got)
	}
	if got := c.Get(1); got == nil {
		t.Fatal("expected p1 to still be present")
	}
	if got := c.Get(3); got == nil {
		t.Fatal("expected p3 to be present")
	}

	// Invalidate
	c.Invalidate(1)
	if got := c.Get(1); got != nil {
		t.Fatal("expected p1 to be invalidated")
	}
}

func TestPostLRUCacheTTL(t *testing.T) {
	c := NewPostLRUCache(10, 20*time.Millisecond)
	c.Put(&Post{ID: 42, Tags: "quick"})

	if got := c.Get(42); got == nil {
		t.Fatal("expected post before TTL expiry")
	}

	time.Sleep(35 * time.Millisecond)

	if got := c.Get(42); got != nil {
		t.Fatalf("expected post to expire after TTL, got %+v", got)
	}
}

func TestPostLRUCacheConcurrency(t *testing.T) {
	c := NewPostLRUCache(100, time.Minute)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				postID := (id*10 + j) % 150
				c.Put(&Post{ID: postID, Tags: fmt.Sprintf("tag_%d", postID)})
				_ = c.Get(postID)
				if j%10 == 0 {
					c.Invalidate(postID)
				}
			}
		}(i)
	}

	wg.Wait()
}
