package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var genericTags = map[string]bool{
	"1girl": true, "1boy": true, "2girls": true, "2boys": true, "solo": true,
	"multiple_girls": true, "female": true, "male": true,
	"highres": true, "absurdres": true, "long_hair": true, "short_hair": true,
	"blush": true, "smile": true, "simple_background": true, "white_background": true,
	"photo": true, "color": true, "english_text": true, "commentary": true,
	"looking_at_viewer": true, "open_mouth": true, "closed_eyes": true,
}

func (h *Handler) SearchRandom(c *gin.Context) {
	page := 1 + rand.Intn(1500)
	posts, err := h.provider().SearchPosts("", page, 1, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var enriched []gin.H
	db := GetDB()
	for _, p := range posts {
		entry := gin.H{
			"id": p.ID, "tags": p.Tags, "file_url": p.FileURL,
			"preview_url": p.PreviewURL, "file_type": p.FileType,
			"width": p.Width, "height": p.Height, "file_size": p.FileSize,
			"score": p.Score, "rating": p.Rating, "downloaded": false,
		}
		if ex := db.Get(p.ID); ex != nil {
			entry["downloaded"] = ex.Downloaded
		}
		enriched = append(enriched, entry)
	}
	c.JSON(http.StatusOK, gin.H{"posts": enriched})
}

func (h *Handler) GetRelated(c *gin.Context) {
	idStr := c.Query("id")
	id, _ := strconv.Atoi(idStr)
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}

	var tags string
	if p := GetDB().Get(id); p != nil && p.Tags != "" {
		tags = p.Tags
	} else {
		posts, err := h.provider().SearchPosts(fmt.Sprintf("id:%d", id), 1, 1, 0)
		if err == nil && len(posts) > 0 {
			tags = posts[0].Tags
		}
	}

	var picked []string
	seen := make(map[string]bool)
	for _, t := range strings.Fields(tags) {
		tag := strings.ToLower(strings.TrimLeft(t, "+-"))
		if genericTags[tag] || seen[tag] || len(tag) < 3 || len(tag) > 34 {
			continue
		}
		seen[tag] = true
		picked = append(picked, tag)
		if len(picked) == 3 {
			break
		}
	}
	if len(picked) == 0 {
		c.JSON(http.StatusOK, gin.H{"posts": []interface{}{}})
		return
	}

	result, err := h.provider().SearchPosts(strings.Join(picked, " "), 1, 24, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	out := make([]gin.H, 0, len(result))
	for _, p := range result {
		if p.ID == id {
			continue
		}
		entry := gin.H{
			"id": p.ID, "tags": p.Tags, "file_url": p.FileURL,
			"preview_url": p.PreviewURL, "file_type": p.FileType,
			"width": p.Width, "height": p.Height,
			"score": p.Score, "rating": p.Rating, "downloaded": false,
		}
		if ex := GetDB().Get(p.ID); ex != nil {
			entry["downloaded"] = ex.Downloaded
		}
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, gin.H{"posts": out, "tags": picked})
}

func (h *Handler) DownloadLiked(c *gin.Context) {
	if h.downloader == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "downloader not initialized"})
		return
	}
	profile := ProfileFor(c)
	profile.mu.RLock()
	ids := make([]int, 0, len(profile.LikedPosts))
	for id := range profile.LikedPosts {
		ids = append(ids, id)
	}
	profile.mu.RUnlock()

	db := GetDB()
	queued := 0
	for _, id := range ids {
		p := db.Get(id)
		if p == nil || p.Downloaded || p.FileURL == "" {
			continue
		}
		h.downloader.Submit(DownloadJob{
			PostID:   p.ID,
			FileURL:  p.FileURL,
			FileType: p.FileType,
			Referer:  h.provider().RefererURL(),
		})
		queued++
	}
	c.JSON(http.StatusAccepted, gin.H{"queued": queued, "total": len(ids)})
}

func saveRoot() string {
	root := GetConfig().GetSavePath()
	if root == "" {
		root = "data/posts"
	}
	return root
}

func (h *Handler) FindFiles(c *gin.Context) {
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"files": []interface{}{}})
		return
	}
	root := saveRoot()
	var out []gin.H
	limit := 3000
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !strings.Contains(strings.ToLower(info.Name()), q) {
			return nil
		}
		out = append(out, gin.H{
			"name": info.Name(),
			"path": path,
			"size": info.Size(),
		})
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"root": root, "files": out})
}

type dupGroup struct {
	Size  int64    `json:"size"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (h *Handler) FindDuplicates(c *gin.Context) {
	root := saveRoot()
	bySize := make(map[int64][]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		bySize[info.Size()] = append(bySize[info.Size()], path)
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var sizes []int64
	for size, list := range bySize {
		if size > 0 && len(list) > 1 {
			sizes = append(sizes, size)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })

	var groups []dupGroup
	for _, size := range sizes {
		byHash := make(map[string][]string)
		for _, path := range bySize[size] {
			hash, err := fileSHA256(path)
			if err != nil {
				continue
			}
			byHash[hash] = append(byHash[hash], path)
		}
		for hash, files := range byHash {
			if len(files) > 1 {
				sort.Strings(files)
				groups = append(groups, dupGroup{Size: size, Hash: hash, Files: files})
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"dups": groups})
}

func (h *Handler) CleanDuplicates(c *gin.Context) {
	if c.GetHeader("X-Confirm-Dupes") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "подтверждение X-Confirm-Dupes обязательно"})
		return
	}
	root := saveRoot()
	groups := make(map[int64][]string)
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Size() <= 0 {
			return nil
		}
		groups[info.Size()] = append(groups[info.Size()], path)
		return nil
	})

	var removed []string
	db := GetDB()
	seenHash := make(map[int64]map[string]string)
	for size, paths := range groups {
		if len(paths) < 2 {
			continue
		}
		if seenHash[size] == nil {
			seenHash[size] = make(map[string]string)
		}
		for _, path := range paths {
			hash, err := fileSHA256(path)
			if err != nil {
				continue
			}
			keep, ok := seenHash[size][hash]
			if ok {
				removed = append(removed, path)
				os.Remove(path)
				db.UnsetDownloadedByPath(path)
				continue
			}
			seenHash[size][hash] = path
			_ = keep
		}
	}
	c.JSON(http.StatusOK, gin.H{"removed": removed, "count": len(removed)})
}
