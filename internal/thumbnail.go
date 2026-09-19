package internal

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

// videoExtensions — расширения видеофайлов.
var videoExtensions = map[string]bool{
	".mp4": true, ".webm": true, ".mov": true, ".mkv": true,
	".avi": true, ".flv": true, ".m4v": true, ".gif": true,
}

func (tg *ThumbnailGenerator) Generate(sourcePath string, postID int) (string, error) {
	thumbPath := tg.ThumbPath(postID)

	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	ext := strings.ToLower(filepath.Ext(sourcePath))
	if videoExtensions[ext] {
		return tg.generateVideoThumbnail(sourcePath, postID)
	}

	srcImg, err := imaging.Open(sourcePath, imaging.AutoOrientation(true))
	if err != nil {
		// Не возвращаем исходник как «миниатюру»: сетка иначе грузит сотни
		// мегабайт оригинала вместо 300px JPEG, и это навсегда оседает в
		// thumb_path. Пустой путь → превью покажет заглушку.
		return "", fmt.Errorf("failed to open image %s: %w", sourcePath, err)
	}

	size := GetConfig().GetThumbSize()
	thumb := imaging.Fit(downscaleForThumb(srcImg, size), size, size, imaging.Lanczos)

	os.MkdirAll(tg.thumbDir, 0o755)
	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(80)); err != nil {
		return "", fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return thumbPath, nil
}

// generateVideoThumbnail — извлекает кадр из видео через ffmpeg.
// При любой неудаче (ffmpeg отсутствует, кадр не извлёкся, таймаут) возвращает
// ОШИБКУ, а не исходный файл: превью-сетка не должна отдавать видео целиком.
func (tg *ThumbnailGenerator) generateVideoThumbnail(sourcePath string, postID int) (string, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found: %w", err)
	}

	thumbPath := tg.ThumbPath(postID)
	os.MkdirAll(tg.thumbDir, 0o755)

	// Извлекаем кадр на 0.5сек, масштабируем до thumbSize
	size := GetConfig().GetThumbSize()
	args := []string{"-y"}
	if os.Getenv("BRIEFLY_FFMPEG_HWACCEL") == "1" {
		args = append(args, "-hwaccel", "auto")
	}
	args = append(args,
		"-ss", "0.5",
		"-i", sourcePath,
		"-vframes", "1",
		// fast_bilinear — в разы быстрее на 4K-видео, разница на 300px незаметна
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", size, size, size, size),
		"-q:v", "2",
		thumbPath,
	)

	// Зависший ffmpeg (битый файл) не должен навсегда блокировать воркер
	// скачивания: CommandContext убивает процесс по таймауту. stderr копим
	// в строку, чтобы при сбое была видна причина.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stderr := new(strings.Builder)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		log.Printf("ffmpeg thumbnail %s failed: %v; stderr=%s", sourcePath, err, stderr.String())
		return "", err
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
