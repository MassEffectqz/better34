package internal

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *Handler) BatchRename(c *gin.Context) {
	var req struct {
		Template string `json:"template"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Template == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "template is required"})
		return
	}

	db := GetDB()
	posts := db.GetDownloaded()
	renamed := 0
	errors := []string{}

	for _, p := range posts {
		if p.FilePath == "" {
			continue
		}
		oldDir := filepath.Dir(p.FilePath)
		oldFile := filepath.Base(p.FilePath)

		name := req.Template
		name = strings.ReplaceAll(name, "{id}", fmt.Sprintf("%d", p.ID))
		rating := p.Rating
		if rating == "" {
			rating = "unknown"
		}
		name = strings.ReplaceAll(name, "{rating}", rating)
		name = strings.ReplaceAll(name, "{score}", fmt.Sprintf("%d", p.Score))
		name = strings.ReplaceAll(name, "{width}", fmt.Sprintf("%d", p.Width))
		name = strings.ReplaceAll(name, "{height}", fmt.Sprintf("%d", p.Height))
		tags := strings.ReplaceAll(p.Tags, " ", "_")
		if len(tags) > 100 {
			tags = tags[:100]
		}
		name = strings.ReplaceAll(name, "{tags}", tags)

		invalid := strings.NewReplacer("<", "", ">", "", ":", "", "\"", "", "/", "", "\\", "", "|", "", "?", "", "*", "")
		name = invalid.Replace(name)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		newDir := filepath.Join(filepath.Dir(oldDir), name)
		if newDir == oldDir {
			continue
		}

		if _, err := os.Stat(newDir); err == nil {
			errors = append(errors, fmt.Sprintf("%d: %q already exists", p.ID, name))
			continue
		}

		if err := os.Rename(oldDir, newDir); err != nil {
			errors = append(errors, fmt.Sprintf("%d: %v", p.ID, err))
			continue
		}

		newPath := filepath.Join(newDir, oldFile)
		p.FilePath = newPath

		if p.ThumbPath != "" {
			oldThumbDir := filepath.Dir(p.ThumbPath)
			newThumbDir := filepath.Join(filepath.Dir(oldThumbDir), name)
			if _, err := os.Stat(newThumbDir); os.IsNotExist(err) {
				if err := os.Rename(oldThumbDir, newThumbDir); err == nil {
					p.ThumbPath = filepath.Join(newThumbDir, filepath.Base(p.ThumbPath))
				}
			}
		}

		db.AddOrUpdate(p)
		renamed++
	}

	if renamed > 0 {
		db.Save()
	}

	c.JSON(http.StatusOK, gin.H{
		"renamed": renamed,
		"errors":  errors,
	})
}

func (h *Handler) CleanDB(c *gin.Context) {
	db := GetDB()
	count := db.CleanNonDownloaded()
	db.Save()
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("deleted %d records", count), "deleted": count})
}

func (h *Handler) DeletePost(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	db := GetDB()
	post := db.Get(id)
	if post == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}

	if post.FilePath != "" {
		// Удаляем только сам файл поста; каталог — только если он опустел
		// (SavePath может указывать на общую папку с другими файлами).
		os.Remove(post.FilePath)
		if dir := filepath.Dir(post.FilePath); dir != "." && dir != "" {
			if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
				os.Remove(dir)
			}
		}
	}

	thumbPath := filepath.Join("data", "thumbs", fmt.Sprintf("%d.jpg", id))
	os.Remove(thumbPath)

	// Комментарии удалённого поста удаляем каскадом: иначе в БД навсегда
	// остаются записи, ссылающиеся на несуществующий пост.
	db.DeleteCommentsForPost(id)

	if post.Downloaded {
		db.UnsetDownloaded(id)
		db.Save()
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
