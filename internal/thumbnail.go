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
	"sync"
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

var (
	ffmpegCheckOnce sync.Once
	ffmpegBinary    string
)

func ffmpegAvailable() string {
	ffmpegCheckOnce.Do(func() {
		p, err := exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegBinary = p
		}
	})
	return ffmpegBinary
}

func (tg *ThumbnailGenerator) ThumbPath(postID int) string {
	webp := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.webp", postID))
	if _, err := os.Stat(webp); err == nil {
		return webp
	}
	jpg := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.jpg", postID))
	if _, err := os.Stat(jpg); err == nil {
		return jpg
	}
	if ffmpegAvailable() != "" {
		return webp
	}
	return jpg
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
	webpPath := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.webp", postID))
	if _, err := os.Stat(webpPath); err == nil {
		return webpPath, nil
	}
	jpgPath := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.jpg", postID))
	if _, err := os.Stat(jpgPath); err == nil {
		return jpgPath, nil
	}

	ext := strings.ToLower(filepath.Ext(sourcePath))
	if videoExtensions[ext] {
		return tg.generateVideoThumbnail(sourcePath, postID)
	}

	os.MkdirAll(tg.thumbDir, 0o755)
	size := GetConfig().GetThumbSize()

	// Если доступен ffmpeg, создаём WebP миниатюру: на 30–50% легче JPEG при той же четкости
	if ffmpeg := ffmpegAvailable(); ffmpeg != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		scaleFilter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear", size, size)
		cmd := exec.CommandContext(ctx, ffmpeg, "-y", "-i", sourcePath, "-vframes", "1", "-vf", scaleFilter, "-c:v", "libwebp", "-quality", "75", webpPath)
		if err := cmd.Run(); err == nil {
			return webpPath, nil
		}
	}

	srcImg, err := imaging.Open(sourcePath, imaging.AutoOrientation(true))
	if err != nil {
		// Не возвращаем исходник как «миниатюру»: сетка иначе грузит сотни
		// мегабайт оригинала вместо 300px JPEG, и это навсегда оседает в
		// thumb_path. Пустой путь → превью покажет заглушку.
		return "", fmt.Errorf("failed to open image %s: %w", sourcePath, err)
	}

	thumb := imaging.Fit(downscaleForThumb(srcImg, size), size, size, imaging.Lanczos)
	if err := imaging.Save(thumb, jpgPath, imaging.JPEGQuality(80)); err != nil {
		return "", fmt.Errorf("failed to save thumbnail: %w", err)
	}

	return jpgPath, nil
}

// generateVideoThumbnail — извлекает кадр из видео через ffmpeg.
// При любой неудаче (ffmpeg отсутствует, кадр не извлёкся, таймаут) возвращает
// ОШИБКУ, а не исходный файл: превью-сетка не должна отдавать видео целиком.
func (tg *ThumbnailGenerator) generateVideoThumbnail(sourcePath string, postID int) (string, error) {
	ffmpegPath := ffmpegAvailable()
	if ffmpegPath == "" {
		return "", fmt.Errorf("ffmpeg not found")
	}

	webpPath := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.webp", postID))
	os.MkdirAll(tg.thumbDir, 0o755)

	// Извлекаем кадр на 0.5сек, масштабируем до thumbSize в WebP
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
		"-c:v", "libwebp",
		"-quality", "75",
		webpPath,
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
		// Fallback к JPEG если libwebp не поддержан конкретным билдом ffmpeg
		jpgPath := filepath.Join(tg.thumbDir, fmt.Sprintf("%d.jpg", postID))
		argsJpg := []string{"-y", "-ss", "0.5", "-i", sourcePath, "-vframes", "1",
			"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=fast_bilinear,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", size, size, size, size),
			"-q:v", "2", jpgPath}
		cmdJpg := exec.CommandContext(ctx, ffmpegPath, argsJpg...)
		if errJpg := cmdJpg.Run(); errJpg == nil {
			return jpgPath, nil
		}
		log.Printf("ffmpeg thumbnail %s failed: %v; stderr=%s", sourcePath, err, stderr.String())
		return "", err
	}

	return webpPath, nil
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
