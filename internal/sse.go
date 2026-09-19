package internal

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

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
			// Канал подписчика заполнен: пропускаем событие.
			// Не удаляем подписчика — он может быть просто медленным.
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

// sseSubscriberCount — число открытых SSE-подключений (для /api/metrics).
func sseSubscriberCount() int {
	hub.mu.Lock()
	n := len(hub.subs)
	hub.mu.Unlock()
	return n
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

	sendData := func(data string) {
		c.SSEvent("event", data)
		flusher.Flush()
	}
	sendJSON := func(payload map[string]any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		sendData(string(data))
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
	// Heartbeat комментарием раз в 25с: мобильные NAT/gateway не рубят
	// простое соединение, и статус загрузок не теряется до переподключения.

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	// Коалесинг статус-событий: пачка событий загрузки схлопывается в одно.

	var (
		lastStatus    time.Time
		pendingStatus string
		flushAt       <-chan time.Time
	)
	flushPending := func() {
		if pendingStatus != "" {
			lastStatus = time.Now()
			sendData(pendingStatus)
			pendingStatus = ""
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// SSE-комментарий клиент игнорирует, но держит соединение живым.
			if _, err := c.Writer.WriteString(": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
			flushPending()
		case <-flushAt:
			flushAt = nil
			flushPending()
		case msg := <-ch:
			// Канал подписчика не закрывается извне (publishSSE только шлёт,
			// unsubscribe удаляет из карты) — ветка !ok недостижима, не проверяем.
			var ev map[string]any
			if json.Unmarshal([]byte(msg), &ev) == nil && ev["type"] == "status" {
				pendingStatus = msg // последнее событие выигрывает
				if time.Since(lastStatus) >= 50*time.Millisecond {
					flushPending()
				} else if flushAt == nil {
					wait := 50*time.Millisecond - time.Since(lastStatus)
					flushAt = time.After(wait)
				}
				continue
			}
			sendData(msg)
		}
	}
}
