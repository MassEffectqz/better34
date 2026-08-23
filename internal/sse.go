package internal

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

type sseHub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

var hub = sseHub{subs: make(map[chan string]struct{})}

func publishSSE(payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for ch := range hub.subs {
		select {
		case ch <- string(data):
		default:
			// Медленный подписчик: не блокируем остальных и не теряем
			// события молча — разрываем соединение, клиент переподключится
			// (EventSource auto-reconnect) и получит свежий статус.
			delete(hub.subs, ch)
			close(ch)
		}
	}
}

func subscribeSSE() (<-chan string, func()) {
	ch := make(chan string, 128)
	hub.mu.Lock()
	hub.subs[ch] = struct{}{}
	hub.mu.Unlock()
	return ch, func() {
		hub.mu.Lock()
		delete(hub.subs, ch)
		hub.mu.Unlock()
	}
}

func (h *Handler) StreamEvents(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ch, unsubscribe := subscribeSSE()
	defer unsubscribe()

	sendJSON := func(payload map[string]any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		c.SSEvent("event", string(data))
		flusher.Flush()
	}

	if h.downloader != nil {
		q, a, d := h.downloader.Status()
		sendJSON(map[string]any{
			"type":     "status",
			"queued":   q,
			"active":   a,
			"done":     d,
			"done_ids": h.downloader.RecentDoneIDs(),
		})
	} else {
		sendJSON(map[string]any{"type": "status", "queued": 0, "active": 0, "done": 0, "done_ids": []int{}})
	}

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return // канал закрыт: медленного подписчика отключили — клиент переподключится
			}
			c.SSEvent("event", msg)
			flusher.Flush()
		}
	}
}
