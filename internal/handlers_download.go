package internal

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// sourceLabelForFileURL определяет метку «откуда пост» для file_url: имя
// провайдера, которому принадлежит хост CDN, иначе сам хост.
func (h *Handler) sourceLabelForFileURL(fileURL string) string {
	if u, err := url.Parse(fileURL); err == nil && u.Hostname() != "" {
		if h.providers != nil {
			host := strings.ToLower(u.Hostname())
			for _, p := range h.providers {
				if p.AllowsHost(host) {
					return p.Name()
				}
			}
		}
		return u.Hostname()
	}
	return ""
}

func (h *Handler) DownloadPost(c *gin.Context) {
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

	if h.downloader == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "downloader not initialized"})
		return
	}

	h.downloader.Submit(DownloadJob{
		PostID:   post.ID,
		FileURL:  post.FileURL,
		FileType: post.FileType,
		Source:   h.sourceLabelForFileURL(post.FileURL),
		Referer:  h.refererForFileURL(post.FileURL),
	})

	c.JSON(http.StatusAccepted, gin.H{"message": "download queued", "post_id": post.ID})
}

func (h *Handler) DownloadMultiple(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) > 2000 {
		req.IDs = req.IDs[:2000]
	}

	if h.downloader == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "downloader not initialized"})
		return
	}

	db := GetDB()
	queued := 0
	seen := make(map[int]bool)
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		post := db.Get(id)
		if post == nil || post.Downloaded {
			continue
		}
		if !post.Downloaded && post.FileURL != "" {
			h.downloader.Submit(DownloadJob{
				PostID:   post.ID,
				FileURL:  post.FileURL,
				FileType: post.FileType,
				Source:   h.sourceLabelForFileURL(post.FileURL),
				Referer:  h.refererForFileURL(post.FileURL),
			})
			queued++
		}
	}

	c.JSON(http.StatusAccepted, gin.H{"message": fmt.Sprintf("queued %d downloads", queued), "queued": queued})
}

func (h *Handler) DownloadResults(c *gin.Context) {
	if h.downloader == nil {
		c.JSON(http.StatusOK, gin.H{"results": []interface{}{}})
		return
	}

	results := []gin.H{}
	db := GetDB()

	for _, result := range h.downloader.ConsumeResults() {
		if result.Error != nil {
			results = append(results, gin.H{
				"post_id": result.PostID,
				"success": false,
				"error":   result.Error.Error(),
			})
		} else {
			db.SetDownloaded(result.PostID, result.FilePath, result.ThumbPath)
			if result.Source != "" {
				db.SetPostSource(result.PostID, result.Source)
			}
			results = append(results, gin.H{
				"post_id": result.PostID,
				"success": true,
			})
		}
	}

	if len(results) > 0 {
		db.BumpSave()
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

func (h *Handler) DownloadStatus(c *gin.Context) {
	if h.downloader == nil {
		c.JSON(http.StatusOK, gin.H{"queued": 0, "active": 0, "done": 0})
		return
	}
	q, a, d := h.downloader.Status()
	c.JSON(http.StatusOK, gin.H{"queued": q, "active": a, "done": d, "done_ids": h.downloader.RecentDoneIDs()})
}

func (h *Handler) DownloadPause(c *gin.Context) {
	if h.downloader != nil {
		h.downloader.Pause()
	}
	c.JSON(http.StatusOK, gin.H{"message": "paused"})
}

func (h *Handler) DownloadResume(c *gin.Context) {
	if h.downloader != nil {
		h.downloader.Resume()
	}
	c.JSON(http.StatusOK, gin.H{"message": "resumed"})
}

func (h *Handler) DownloadQueue(c *gin.Context) {
	if h.downloader == nil {
		c.JSON(http.StatusOK, gin.H{"queue": []interface{}{}, "active": []interface{}{}})
		return
	}
	queue := h.downloader.QueueList()
	items := make([]gin.H, 0, len(queue))
	for _, j := range queue {
		items = append(items, gin.H{"post_id": j.PostID, "file_url": j.FileURL, "file_type": j.FileType})
	}
	active := h.downloader.ActiveIDs()
	c.JSON(http.StatusOK, gin.H{"queue": items, "active": active})
}

func (h *Handler) DownloadCancel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if h.downloader != nil {
		h.downloader.Cancel(id)
	}
	c.JSON(http.StatusOK, gin.H{"message": "cancelled"})
}

func (h *Handler) DownloadMoveUp(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ok := false
	if h.downloader != nil {
		ok = h.downloader.MoveUp(id)
	}
	c.JSON(http.StatusOK, gin.H{"ok": ok})
}

func (h *Handler) DownloadMoveDown(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ok := false
	if h.downloader != nil {
		ok = h.downloader.MoveDown(id)
	}
	c.JSON(http.StatusOK, gin.H{"ok": ok})
}
