package internal

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

type ThumbnailGenerator struct {
	thumbDir string
}

func NewThumbnailGenerator() *ThumbnailGenerator {
	return &ThumbnailGenerator{
		thumbDir: "data/thumbs",
	}
}

func (tg *ThumbnailGenerator) ThumbPath(postID int) string {
	return filepath.Join(tg.thumbDir, fmt.Sprintf("%d.jpg", postID))
}

func (tg *ThumbnailGenerator) Generate(sourcePath string, postID int) (string, error) {
	thumbPath := tg.ThumbPath(postID)

	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	ext := strings.ToLower(filepath.Ext(sourcePath))
	if ext == ".mp4" || ext == ".webm" || ext == ".gif" {
		return sourcePath, nil
	}

	srcImg, err := imaging.Open(sourcePath, imaging.AutoOrientation(true))
	if err != nil {
		return sourcePath, nil
	}

	size := GetConfig().GetThumbSize()
	thumb := imaging.Fit(srcImg, size, size, imaging.Lanczos)

	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(80)); err != nil {
		return "", fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return thumbPath, nil
}

func (tg *ThumbnailGenerator) GenerateFromReader(r image.Image, postID int) (string, error) {
	thumbPath := tg.ThumbPath(postID)

	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	size := GetConfig().GetThumbSize()
	thumb := imaging.Fit(r, size, size, imaging.Lanczos)

	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(80)); err != nil {
		return "", fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return thumbPath, nil
}
