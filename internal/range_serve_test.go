package internal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// Перемотка видео в браузере требует Range-запросов: без них <video>
// скачивает файл целиком при каждом seek. http.ServeFile поддерживает
// их из коробки — тест фиксирует контракт (206 + Accept-Ranges).
func TestServeLocalFileSupportsRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	path := filepath.Join(dir, "video.mp4")
	data := bytes.Repeat([]byte("x"), 4096)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/file/1", nil)
	req.Header.Set("Range", "bytes=100-199")
	c.Request = req

	serveLocalFile(c, path)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusPartialContent)
	}
	if got := w.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want %q", got, "bytes")
	}
	if got := w.Header().Get("Content-Range"); got != "bytes 100-199/4096" {
		t.Fatalf("Content-Range = %q, want %q", got, "bytes 100-199/4096")
	}
	if w.Body.Len() != 100 {
		t.Fatalf("body len = %d, want 100", w.Body.Len())
	}
}
