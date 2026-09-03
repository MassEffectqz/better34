package internal

import (
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
)

// ── Perceptual hash (pHash) ──────────────────────────────────────────────
// Классический pHash: картинка приводится к 32×32 в градациях серого,
// берётся 2D DCT, из неё низкочастотный блок 8×8 (без DC), каждый байт
// сравнивается с медианой → 64-битный отпечаток. Похожесть — расстояние
// Хэмминга. Видео не хэшируются (декодировать кадр здесь нечем).

const (
	pHashSize = 32
	pHashGrid = 8
)

// dctTable[j][x] = cos(pi*(2x+1)*j/2N) — считается один раз.
var dctTable = buildDCTTable(pHashSize)

func buildDCTTable(n int) [][]float64 {
	t := make([][]float64, n)
	for j := range t {
		t[j] = make([]float64, n)
		for x := 0; x < n; x++ {
			t[j][x] = math.Cos(math.Pi * (2*float64(x) + 1) * float64(j) / (2 * float64(n)))
		}
	}
	return t
}

// PerceptualHashFile считает pHash файла с изображением. Пустая строка —
// файл не изображение (видео) или не читается: это не ошибка потока скачивания.
func PerceptualHashFile(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if videoExtensions[ext] {
		return percepHashVideo(path)
	}
	img, err := imaging.Open(path)
	if err != nil {
		return ""
	}
	return PerceptualHash(img)
}

// percepHashVideo извлекает кадр из видео через ffmpeg и считает pHash.
func percepHashVideo(path string) string {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}

	// Временный файл для кадра
	tmpFile, err := os.CreateTemp("", "phash-*.jpg")
	if err != nil {
		return ""
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command(ffmpegPath,
		"-y",
		"-ss", "0.5",
		"-i", path,
		"-vframes", "1",
		"-q:v", "2",
		tmpPath,
	)
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return ""
	}

	img, err := imaging.Open(tmpPath)
	if err != nil {
		return ""
	}
	return PerceptualHash(img)
}

func PerceptualHash(img image.Image) string {
	if img == nil {
		return ""
	}
	small := imaging.Fill(img, pHashSize, pHashSize, imaging.Center, imaging.Lanczos)
	small = imaging.Grayscale(small)
	b := small.Bounds()
	if b.Dx() < pHashSize || b.Dy() < pHashSize {
		return ""
	}
	// Яркости в float64.
	pix := make([][]float64, pHashSize)
	for y := 0; y < pHashSize; y++ {
		pix[y] = make([]float64, pHashSize)
		for x := 0; x < pHashSize; x++ {
			r, _, _, _ := small.At(b.Min.X+x, b.Min.Y+y).RGBA()
			pix[y][x] = float64(r >> 8)
		}
	}
	// 2D DCT: сначала строки, потом столбцы (сепарабельно).
	rows := dct2D(pix)
	blocks := make([]float64, 0, pHashGrid*pHashGrid)
	for j := 0; j < pHashGrid; j++ {
		for i := 0; i < pHashGrid; i++ {
			if i == 0 && j == 0 {
				continue // DC не несёт структуры
			}
			blocks = append(blocks, rows[j][i])
		}
	}
	// Медиана 63 значений.
	sortSlice(blocks)
	median := blocks[len(blocks)/2]
	var hash uint64
	var bit int
	for j := 0; j < pHashGrid; j++ {
		for i := 0; i < pHashGrid; i++ {
			if i == 0 && j == 0 {
				continue
			}
			if rows[j][i] > median {
				hash |= 1 << bit
			}
			bit++
		}
	}
	return encodePHash(hash)
}

func dct2D(pix [][]float64) [][]float64 {
	n := len(pix)
	tmp := make([][]float64, n)
	for j := 0; j < n; j++ {
		tmp[j] = make([]float64, n)
		for i := 0; i < n; i++ {
			var sum float64
			for x := 0; x < n; x++ {
				sum += dctTable[i][x] * pix[j][x]
			}
			tmp[j][i] = sum
		}
	}
	out := make([][]float64, n)
	for j := 0; j < n; j++ {
		out[j] = make([]float64, n)
		for i := 0; i < n; i++ {
			var sum float64
			for y := 0; y < n; y++ {
				sum += dctTable[j][y] * tmp[y][i]
			}
			out[j][i] = sum
		}
	}
	return out
}

func sortSlice(f []float64) {
	// Маленький слайс (63 элемента) — обычная сортировка вставками дешевле
	// универсального sort.Slice без интерфейсных аллокаций.
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j] < f[j-1]; j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

func encodePHash(h uint64) string {
	const hexDigits = "0123456789abcdef"
	buf := make([]byte, 16)
	for i := 0; i < 16; i++ {
		buf[15-i] = hexDigits[(h>>(4*i))&0xF]
	}
	return string(buf)
}

func decodePHash(s string) (uint64, bool) {
	if len(s) != 16 {
		return 0, false
	}
	var h uint64
	for _, c := range strings.ToLower(s) {
		var v uint64
		switch {
		case c >= '0' && c <= '9':
			v = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v = uint64(c-'a') + 10
		default:
			return 0, false
		}
		h = h<<4 | v
	}
	return h, true
}

// HammingPHash — расстояние Хэмминга между двумя hex-pHash.
func HammingPHash(a, b string) (int, bool) {
	ha, ok1 := decodePHash(a)
	hb, ok2 := decodePHash(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return math_popcount(ha ^ hb), true
}

func math_popcount(x uint64) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}
