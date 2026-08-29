package internal

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disintegration/imaging"
)

// Параллельные резолвы одного хоста должны схлопываться в один DoH-запрос.
func TestDoHResolveSingleflight(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(30 * time.Millisecond)
		fmt.Fprintf(w, `{"Answer":[{"name":"grid.example","type":1,"TTL":300,"data":"93.184.216.34"}]}`)
	}))
	defer srv.Close()

	r := NewDoHResolver(srv.URL)
	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, err := r.Resolve("grid.example")
			if err != nil || ip.String() != "93.184.216.34" {
				t.Errorf("Resolve: ip=%v err=%v", ip, err)
			}
		}()
	}
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("DoH hits = %d for %d concurrent resolves, want 1", got, n)
	}
}

// После неудачи хост уходит в негативный кэш: повторные резолвы не
// долбят DoH-сервер (fallback — системный DNS на уровне DialContext).
func TestDoHNegativeCache(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewDoHResolver(srv.URL)
	for i := 0; i < 5; i++ {
		if _, err := r.Resolve("down.example"); err == nil {
			t.Fatal("expected error for failing DoH")
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("DoH hits = %d, want 1 (negative cache must suppress retries)", got)
	}
}

// Двухступенчатый даунскейл даёт корректные размеры превью.
func TestThumbnailTwoStepDownscale(t *testing.T) {
	dir := t.TempDir()
	tg := NewThumbnailGenerator()
	tg.thumbDir = filepath.Join(dir, "thumbs") // не трогаем реальный data/thumbs
	size := GetConfig().GetThumbSize()
	if size <= 0 {
		size = 256
	}

	src := image.NewRGBA(image.Rect(0, 0, size*10, size*5))
	for x := 0; x < src.Bounds().Dx(); x += size {
		for y := 0; y < src.Bounds().Dy(); y += size {
			src.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	srcPath := filepath.Join(dir, "big.png")
	f, _ := os.Create(srcPath)
	png.Encode(f, src)
	f.Close()

	thumbPath, err := tg.Generate(srcPath, 999001)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	img, err := imaging.Open(thumbPath)
	if err != nil {
		t.Fatalf("open thumb: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > size || b.Dy() > size {
		t.Errorf("thumb %dx%d exceeds %dpx limit", b.Dx(), b.Dy(), size)
	}
	// Пропорции сохранены: 2:1 исходник → 2:1 превью.
	if b.Dx() != b.Dy()*2 {
		t.Errorf("aspect broken: %dx%d, want 2:1", b.Dx(), b.Dy())
	}

	// Маленькие исходники идут в Lanczos напрямую без первой ступени.
	small := image.NewRGBA(image.Rect(0, 0, size/2, size/4))
	_ = downscaleForThumb(small, size)
}

// closedLocalAddr выдаёт заведомо закрытый локальный адрес: подключение
// к нему отказывает мгновенно (не таймаутится) — стабильно и для CI.
func closedLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// Если первый DoH-эндпоинт мёртв, резолвер должен уйти на второй,
// а не молча деградировать до системного DNS.
func TestDoHFailsOverToSecondaryEndpoint(t *testing.T) {
	dead := closedLocalAddr(t)

	var hits atomic.Int64
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprintf(w, `{"Answer":[{"name":"failover.example","type":1,"TTL":300,"data":"203.0.113.7"}]}`)
	}))
	defer secondary.Close()

	resolver := NewDoHResolver("http://"+dead, secondary.URL)
	ip, err := resolver.Resolve("failover.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ip.String() != "203.0.113.7" {
		t.Errorf("ip = %v, want 203.0.113.7", ip)
	}
	if hits.Load() == 0 {
		t.Error("secondary endpoint was never queried")
	}
}

// Мёртвый SOCKS-прокси не должен молча прятать деградацию до прямого
// канала: откат обязан работать и попадать в журнал строкой [proxy].
func TestBuildTransportDeadProxyFallsBackAndLogs(t *testing.T) {
	t.Setenv("BRIEFLY_PROXY_URL", "")
	cfg := GetConfig()
	cfg.SetProxyURL(closedLocalAddr(t))
	defer cfg.SetProxyURL("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prevW, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	defer func() { log.SetOutput(prevW); log.SetFlags(prevFlags) }()

	client := buildAPIHTTPClient()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("direct fallback request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
	if !strings.Contains(buf.String(), "[proxy]") || !strings.Contains(buf.String(), "откат") {
		t.Errorf("dead proxy fallback must be logged, got:\n%s", buf.String())
	}
}

// fakeSocks5 поднимает вырожденный SOCKS5-сервер: отвечает на handshake
// успехом и фиксирует факт получения CONNECT. После отметки соединение
// закрывается — клиент увидит EOF, тесту важен сам факт прохода через прокси.
func fakeSocks5(t *testing.T, connectObserved *atomic.Bool) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				buf := make([]byte, 128)
				// Приветствие: [ver][nmethods][методы×nmethods].
				n, err := io.ReadFull(c, buf[:2])
				if err != nil || buf[0] != 0x05 {
					t.Logf("[fake-socks] greeting: n=%d ver=%#x err=%v", n, buf[0], err)
					return
				}
				if nm := int(buf[1]); nm > 0 {
					if _, err := io.ReadFull(c, buf[:nm]); err != nil {
						return
					}
				}
				if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
					return
				}
				n, err = io.ReadFull(c, buf[:4]) // [ver][cmd][rsv][atyp]
				if err != nil || buf[0] != 0x05 || buf[1] != 0x01 {
					t.Logf("[fake-socks] cmd head: n=%d head=% X err=%v", n, buf[:max(n, 0)], err)
					return
				}
				switch buf[3] {
				case 0x01: // IPv4: адрес + порт
					if _, err := io.ReadFull(c, buf[4:10]); err != nil {
						return
					}
				case 0x03: // домен: длина + имя + порт
					if _, err := io.ReadFull(c, buf[4:5]); err != nil {
						return
					}
					ln := int(buf[4])
					if _, err := io.ReadFull(c, buf[5:5+ln+2]); err != nil { // имя + порт целиком
						return
					}
				case 0x04: // IPv6
					if _, err := io.ReadFull(c, buf[4:22]); err != nil {
						return
					}
				default:
					t.Logf("[fake-socks] неизвестный atyp %#x", buf[3])
					return
				}

				connectObserved.Store(true)
				// Успешный ответ на CONNECT, чтобы клиент считал прокси рабочим и
				// двинулся дальше (запрос всё равно упадёт: ретрансляции нет).
				_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			}(conn)
		}
	}()
	return l.Addr().String()
}

// BRIEFLY_PROXY_URL перекрывает proxy_url из конфига, а именно используется
// по назначению: CONNECT реально проходит через указанный прокси.
func TestBuildTransportEnvOverrideProxyIsUsed(t *testing.T) {
	cfg := GetConfig()
	cfg.SetProxyURL("")
	defer cfg.SetProxyURL("")

	var seen atomic.Bool
	socksAddr := fakeSocks5(t, &seen)

	t.Setenv("BRIEFLY_PROXY_URL", "socks5://"+socksAddr)
	client := &http.Client{Timeout: 5 * time.Second, Transport: buildTransport()}
	// Домен заведомо не существует: если бы транспорт пошёл напрямую,
	// фейковый SOCKS не получил бы ни одного CONNECT.
	_, err := client.Get("https://nonexistent-host-for-test.example/index.php")
	if err == nil {
		t.Fatal("request through degenerate socks must fail")
	}
	if !seen.Load() {
		t.Error("BRIEFLY_PROXY_URL ignored: SOCKS handshake with CONNECT never happened")
	}
}
