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

// downscaleForThumb — двухступенчатый даунскейл: для очень больших
// исходников сначала быстрый box-фильтр до ~2x цели, потом Lanczos на
// малой картинке. На итоговых 200–500px разница с чистым Lanczos
// незаметна, а скорость на 4000×8000 исходниках — в разы.
func downscaleForThumb(src image.Image, size int) image.Image {
	if b := src.Bounds(); b.Dx() > size*4 || b.Dy() > size*4 {
		src = imaging.Resize(src, size*2, 0, imaging.Box)
	}
	return src
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
	thumb := imaging.Fit(downscaleForThumb(srcImg, size), size, size, imaging.Lanczos)

	os.MkdirAll(tg.thumbDir, 0o755)
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
	thumb := imaging.Fit(downscaleForThumb(r, size), size, size, imaging.Lanczos)

	os.MkdirAll(tg.thumbDir, 0o755)
	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(80)); err != nil {
		return "", fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return thumbPath, nil
}
