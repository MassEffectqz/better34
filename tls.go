package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quic-go/quic-go/http3"
)

// tlsEnabled включает HTTPS + HTTP/2: браузеры открывают HTTP/2 только
// поверх TLS, а без него лимит 6 соединений на хост сериализует ленту
// из десятков миниатюр. Сертификат self-signed (BRIEFLY_TLS=1).
func tlsEnabled() bool {
	v := strings.ToLower(getEnv("BETTER34_TLS"))
	return v == "1" || v == "true" || v == "yes"
}

const (
	// Слэши намеренно: пакет os в Go принимает "/" и на Windows.
	tlsCertPath = "data/tls/cert.pem"
	tlsKeyPath  = "data/tls/key.pem"
)

// certCoversHosts проверяет, что сертификат содержит все необходимые адреса и домены.
func certCoversHosts(cert *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		if strings.HasPrefix(h, ".") {
			continue
		}
		if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
			found := false
			for _, certIP := range cert.IPAddresses {
				if certIP.Equal(ip) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		} else {
			found := false
			for _, dns := range cert.DNSNames {
				if strings.EqualFold(dns, h) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// loadOrGenerateCert возвращает self-signed сертификат, покрывающий все
// разрешённые хосты (allowedHosts). Сертификат сохраняется в data/tls и
// переиспользуется: постоянный отпечаток означает, что браузер (особенно
// на телефоне) принимает предупреждение о самоподписанном сертификате
// один раз, а не после каждого перезапуска сервера.
// Если сертификат на диске устарел, истёк или не покрывает текущие адреса,
// он автоматически перегенерируется с максимальным сроком действия 365 дней
// (в соответствии с требованиями Apple ATS и современных браузеров Chromium).
func loadOrGenerateCert() (tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath); err == nil {
		if len(cert.Certificate) > 0 {
			if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
				now := time.Now()
				validityPeriod := leaf.NotAfter.Sub(leaf.NotBefore)
				// Проверяем:
				// 1. До конца действия осталось более 7 дней.
				// 2. Срок действия не превышает 398 дней (правило Apple / Chromium для TLS-сертификатов).
				// 3. Сертификат покрывает все текущие allowedHosts.
				if now.Add(7*24*time.Hour).Before(leaf.NotAfter) &&
					validityPeriod <= 398*24*time.Hour &&
					certCoversHosts(leaf, allowedHosts) {
					return cert, nil
				}
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "better34-local"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 год (<= 398 дней) — строгое требование Apple ATS и Android Chrome
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range allowedHosts {
		if strings.HasPrefix(h, ".") {
			continue
		}
		if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	var certPEM, keyPEM bytes.Buffer
	if err := pem.Encode(&certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := pem.Encode(&keyPEM, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(filepath.Dir(tlsCertPath), 0700); err == nil {
		// Ошибки записи не критичны: сертификат просто перегенерируется
		// при следующем запуске.
		_ = os.WriteFile(tlsCertPath, certPEM.Bytes(), 0644)
		_ = os.WriteFile(tlsKeyPath, keyPEM.Bytes(), 0600)
	}
	return tls.X509KeyPair(certPEM.Bytes(), keyPEM.Bytes())
}

// ── Один порт: TLS и plain HTTP одновременно ─────────────────────────────
// Порт один, а клиенты бывают разные: телефон со старой закладкой/PWA
// стучится по http:// и получает загадочную ошибку TLS handshake. Поэтому
// первый байт соединения подглядывается: 0x16 (TLS ClientHello) — в
// HTTPS-сервер (с HTTP/2), любой другой — в крошечный редиректор на https.
// Старые http-ссылки продолжают работать сами собой.

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(addr net.Addr, buf int) *chanListener {
	return &chanListener{addr: addr, ch: make(chan net.Conn, buf), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// demuxAccept раскладывает соединения основного листенера по двум
// каналам-листенерам. Дедлайн на Peek обязателен: молчащее соединение
// (сканер, зависший клиент) иначе навсегда блокировало бы accept-цикл.
// done — канал для остановки: разблокирует отправку в каналы-листенеры.
func demuxAccept(ln net.Listener, tlsLn, plainLn *chanListener, done <-chan struct{}) {
	defer ln.Close()
	defer tlsLn.Close()
	defer plainLn.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		br := bufio.NewReader(conn)
		first, perr := br.Peek(1)
		_ = conn.SetReadDeadline(time.Time{})
		pc := &peekConn{Conn: conn, r: br}
		if perr == nil && first[0] == 0x16 {
			select {
			case tlsLn.ch <- pc:
			case <-done:
				conn.Close()
				return
			}
		} else {
			// Не TLS (включая обрывы): редиректор ответит или закроет сам.
			select {
			case plainLn.ch <- pc:
			case <-done:
				conn.Close()
				return
			}
		}
	}
}

// altSvcMiddleware рекламирует HTTP/3 на том же порту: современные браузеры
// по Alt-Svc сами переключаются на QUIC, когда сеть его поддерживает.
func altSvcMiddleware() gin.HandlerFunc {
	h3Active := strings.ToLower(strings.TrimSpace(getEnv("BETTER34_H3"))) != "0"
	if !tlsEnabled() || !h3Active {
		return func(c *gin.Context) { c.Next() }
	}
	port := strings.TrimSpace(getEnv("BETTER34_PORT"))
	if port == "" {
		port = "3000"
	}
	return func(c *gin.Context) {
		if c.Request.TLS != nil {
			c.Header("Alt-Svc", `h3=":`+port+`"; ma=86400`)
		}
		c.Next()
	}
}

// httpsRedirectHandler отправляет plain HTTP на тот же хост/порт по https.
// 307 (временный) — чтобы браузер не «залипал» на редиректе, если TLS
// потом отключат.
func httpsRedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
}

// setupTLS настраивает TLS-сервер, demux и HTTP/3. Возвращает scheme,
// tlsLn — listener TLS-соединений (выход демукса), plainSrv, H3-сервер,
// rawLn — основной listener (для явного закрытия при shutdown),
// done — канал для остановки demuxAccept (разблокирует отправку в каналы),
// cert — сертификат, сгенерированный ОДИН раз: его же использует HTTPS-сервер
// в main.go (иначе при неудаче записи на диск TLS и H3 получали разные ключи).
func setupTLS(addr string, handler http.Handler) (scheme string, tlsLn net.Listener, plainSrv *http.Server, h3Server *http3.Server, rawLn net.Listener, done chan struct{}, cert tls.Certificate) {
	scheme = "http"
	if !tlsEnabled() {
		return
	}
	var err error
	cert, err = loadOrGenerateCert()
	if err != nil {
		slog.Warn("BRIEFLY_TLS: не удалось подготовить сертификат, работаю по HTTP", "error", err)
		return
	}
	scheme = "https"
	rawLn, err = net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Server failed: %v", err)
	}
	tlsChanLn := newChanListener(rawLn.Addr(), 64)
	plainLn := newChanListener(rawLn.Addr(), 64)
	done = make(chan struct{})
	go demuxAccept(rawLn, tlsChanLn, plainLn, done)
	tlsLn = tlsChanLn
	plainSrv = &http.Server{
		Handler:           httpsRedirectHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          newTLSErrorFilterLogger(logOutput),
	}
	go func() {
		_ = plainSrv.Serve(plainLn)
	}()
	// Задача 10: HTTP/3 (QUIC) — отдельный UDP-сокет на том же порту.
	// Браузер видит Alt-Svc и сам уходит на h3, если сеть позволяет.
	// Сертификат передаём из памяти (не читаем с диска), чтобы H3 и HTTPS
	// Задача 10: HTTP/3 (QUIC) — отдельный UDP-сокет на том же порту.
	// Браузер видит Alt-Svc и сам уходит на h3, если сеть позволяет.
	// Сертификат передаём из памяти через Serve(udpConn).
	if strings.ToLower(strings.TrimSpace(getEnv("BETTER34_H3"))) != "0" {
		udpConn, err := net.ListenPacket("udp", addr)
		if err != nil {
			slog.Warn("HTTP/3: QUIC UDP недоступен", "error", err)
		} else {
			h3TLSConfig := http3.ConfigureTLSConfig(&tls.Config{
				Certificates: []tls.Certificate{cert},
			})
			h3Server = &http3.Server{
				Addr:      addr,
				Handler:   handler,
				TLSConfig: h3TLSConfig,
			}
			go func() {
				defer udpConn.Close()
				if err := h3Server.Serve(udpConn); err != nil &&
					!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					slog.Warn("HTTP/3: QUIC недоступен, продолжаем по TCP", "error", err)
				}
			}()
		}
	}
	return
}

// tlsErrorFilterWriter фильтрует штатные для self-signed сертификатов ошибки хэндшейка
// (клиент закрывает соединение до подтверждения исключения в браузере или проводит фоновый опрос).
type tlsErrorFilterWriter struct {
	w io.Writer
}

func (f *tlsErrorFilterWriter) Write(p []byte) (int, error) {
	s := string(p)
	if strings.Contains(s, "TLS handshake error") && (strings.Contains(s, "unknown certificate") ||
		strings.Contains(s, "bad certificate") ||
		strings.Contains(s, "certificate unknown") ||
		strings.Contains(s, "unknown CA") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "forcibly closed by the remote host")) {
		if v := strings.ToLower(getEnv("BETTER34_DEBUG")); v == "1" || v == "true" {
			slog.Debug("TLS handshake error (suppressed)", "error", strings.TrimSpace(s))
		}
		return len(p), nil
	}
	return f.w.Write(p)
}

func newTLSErrorFilterLogger(out io.Writer) *log.Logger {
	return log.New(&tlsErrorFilterWriter{w: out}, "", 0)
}
