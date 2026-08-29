package internal

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GET /api/download-zip?ids=1,2,3 — отдаёт ZIP-архив скачанных файлов
// выбранных постов. Нескачанные и отсутствующие на диске посты
// пропускаются; в ответе заголовок X-Zip-Skipped с их числом.
func (h *Handler) DownloadZip(c *gin.Context) {
	parts := strings.Split(c.Query("ids"), ",")
	var ids []int
	for _, s := range parts {
		if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids required"})
		return
	}
	if len(ids) > 2000 {
		ids = ids[:2000]
	}

	db := GetDB()
	type entry struct {
		id   int
		path string
	}
	var files []entry
	skipped := 0
	for _, id := range ids {
		post := db.Get(id)
		if post == nil || !post.Downloaded || post.FilePath == "" {
			skipped++
			continue
		}
		if _, err := os.Stat(post.FilePath); err != nil {
			skipped++
			continue
		}
		files = append(files, entry{id, post.FilePath})
	}

	name := "briefly-" + time.Now().Format("2006-01-02") + ".zip"
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	c.Header("X-Zip-Skipped", strconv.Itoa(skipped))
	if len(files) == 0 {
		// Валидный пустой zip, чтобы клиент не получил битый ответ.
		zw := zip.NewWriter(c.Writer)
		zw.Close()
		return
	}

	// Файлы по порядку возрастания id: детерминированный архив.
	sort.Slice(files, func(i, j int) bool { return files[i].id < files[j].id })

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()

	used := make(map[string]int, len(files))
	buf := make([]byte, 256<<10)
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f.path))
		base := fmt.Sprintf("%d_%s%s", f.id, "post", ext)
		if n := used[base]; n > 0 {
			base = fmt.Sprintf("%d_post_%d%s", f.id, n, ext)
		}
		used[base]++

		src, err := os.Open(f.path)
		if err != nil {
			skipped++
			continue
		}
		fh := &zip.FileHeader{Name: base, Method: zip.Store, Modified: time.Now()}
		dst, err := zw.CreateHeader(fh)
		if err != nil {
			src.Close()
			return
		}
		if _, err := io.CopyBuffer(dst, src, buf); err != nil {
			src.Close()
			return
		}
		src.Close()
	}
}
