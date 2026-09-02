package internal

import (
	"image"
	"math"

	"github.com/disintegration/imaging"
)

// ── BlurHash (кодирование) ───────────────────────────────────────────────
// Стандартный алгоритм (https://blurha.sh, компоненты 3×3): картинка в
// linear-RGB раскладывается косинус-базисом, коэффициенты пакуются в
// компактную base83-строку. Декодер — на клиенте (static/js/blurhash.js):
// строка < 30 байт вместо тысяч байт превью-картинки.

const base83Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

const (
	blurhashNX = 3
	blurhashNY = 3
)

func base83Encode(value, length int) string {
	buf := make([]byte, length)
	digit := 1
	for i := length - 1; i >= 0; i-- {
		buf[i] = base83Chars[(value/digit)%83]
		digit *= 83
	}
	return string(buf)
}

func sRGBToLinear(v int) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func signPow(v, exp float64) float64 {
	if v < 0 {
		return -math.Pow(-v, exp)
	}
	return math.Pow(v, exp)
}

func linearToSRGB(v float64) int {
	x := math.Max(0, math.Min(1, v))
	if x <= 0.0031308 {
		return int(x*12.92*255 + 0.5)
	}
	return int((1.055*math.Pow(x, 1/2.4)-0.055)*255 + 0.5)
}

func encodeDC(f [3]float64) string {
	r := linearToSRGB(f[0])
	g := linearToSRGB(f[1])
	b := linearToSRGB(f[2])
	return base83Encode((r<<16)+(g<<8)+b, 4)
}

func encodeAC(f [3]float64, actualMax float64) string {
	quant := func(v float64) int {
		return int(math.Max(0, math.Min(18, math.Floor(signPow(v/actualMax, 0.5)*9+9.5))))
	}
	qr, qg, qb := quant(f[0]), quant(f[1]), quant(f[2])
	return base83Encode(qr*19*19+qg*19+qb, 4)
}

// BlurHashFile кодирует blurhash из файла; "" — файл не прочитался.
func BlurHashFile(path string) string {
	img, err := imaging.Open(path)
	if err != nil {
		return ""
	}
	return BlurHashEncode(img)
}

// BlurHashEncode возвращает blurhash строки картинки; "" — не получилось.
func BlurHashEncode(img image.Image) string {
	if img == nil {
		return ""
	}
	small := imaging.Fit(img, 32, 32, imaging.Lanczos)
	b := small.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 2 || h < 2 {
		return ""
	}
	// Пиксели в linear-RGB.
	lin := make([][][3]float64, h)
	for y := 0; y < h; y++ {
		lin[y] = make([][3]float64, w)
		for x := 0; x < w; x++ {
			r, g, bl, _ := small.At(b.Min.X+x, b.Min.Y+y).RGBA()
			lin[y][x] = [3]float64{
				sRGBToLinear(int(r >> 8)),
				sRGBToLinear(int(g >> 8)),
				sRGBToLinear(int(bl >> 8)),
			}
		}
	}
	// Косинус-коэффициенты (нормировка как в референс-реализации).
	factors := make([][3]float64, blurhashNX*blurhashNY)
	for j := 0; j < blurhashNY; j++ {
		for i := 0; i < blurhashNX; i++ {
			norm := 1.0
			if i != 0 || j != 0 {
				norm = 2
			}
			var f [3]float64
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					basis := norm *
						math.Cos(math.Pi*float64(i)*float64(x)/float64(w)) *
						math.Cos(math.Pi*float64(j)*float64(y)/float64(h))
					p := lin[y][x]
					f[0] += basis * p[0]
					f[1] += basis * p[1]
					f[2] += basis * p[2]
				}
			}
			scale := 1.0 / float64(w*h)
			factors[j*blurhashNX+i] = [3]float64{f[0] * scale, f[1] * scale, f[2] * scale}
		}
	}
	dc := factors[0]
	ac := factors[1:]
	maxAc := 0.0
	for _, f := range ac {
		for _, v := range f {
			if math.Abs(v) > maxAc {
				maxAc = math.Abs(v)
			}
		}
	}
	var quantMax int
	if maxAc > 0 {
		quantMax = int(math.Max(0, math.Min(82, math.Floor(maxAc*166-0.5))))
	}
	actualMax := float64(quantMax+1) / 166

	out := base83Encode((blurhashNX-1)+(blurhashNY-1)*9, 1)
	out += base83Encode(quantMax, 1)
	out += encodeDC(dc)
	for _, f := range ac {
		out += encodeAC(f, actualMax)
	}
	return out
}
