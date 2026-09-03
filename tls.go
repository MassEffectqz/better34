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
	v := strings.ToLower(os.Getenv("BRIEFLY_TLS"))
	return v == "1" || v == "true" || v == "yes"
}

const (
	// Слэши намеренно: пакет os в Go принимает "/" и на Windows.
	tlsCertPath = "data/tls/cert.pem"
	tlsKeyPath  = "data/tls/key.pem"
)

// loadOrGenerateCert возвращает self-signed сертификат, покрывающий все
// разрешённые хосты (allowedHosts). Сертификат сохраняется в data/tls и
// переиспользуется: постоянный отпечаток означает, что браузер (особенно
// на телефоне) принимает предупреждение о самоподписанном сертификате
// один раз, а не после каждого перезапуска сервера.
func loadOrGenerateCert() (tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath); err == nil {
		return cert, nil
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
		Subject:               pkix.Name{CommonName: "briefly-local"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range allowedHosts {
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
func demuxAccept(ln net.Listener, tlsLn, plainLn *chanListener) {
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
			tlsLn.ch <- pc
		} else {
			// Не TLS (включая обрывы): редиректор ответит или закроет сам.
			plainLn.ch <- pc
		}
	}
}

// altSvcMiddleware рекламирует HTTP/3 на том же порту: современные браузеры
// по Alt-Svc сами переключаются на QUIC, когда сеть его поддерживает.
func altSvcMiddleware() gin.HandlerFunc {
	h3Active := strings.ToLower(strings.TrimSpace(os.Getenv("BRIEFLY_H3"))) != "0"
	if !tlsEnabled() || !h3Active {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if c.Request.TLS != nil {
			c.Header("Alt-Svc", `h3=":`+os.Getenv("BRIEFLY_PORT")+`"; ma=86400`)
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
// основной listener, plain-сервер и H3-сервер (могут быть nil).
func setupTLS(addr string) (scheme string, rawLn net.Listener, plainSrv *http.Server, h3Server *http3.Server) {
	scheme = "http"
	if !tlsEnabled() {
		return
	}
	_, err := loadOrGenerateCert()
	if err != nil {
		slog.Warn("BRIEFLY_TLS: не удалось подготовить сертификат, работаю по HTTP", "error", err)
		return
	}
	scheme = "https"
	rawLn, err = net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Server failed: %v", err)
	}
	tlsLn := newChanListener(rawLn.Addr(), 64)
	plainLn := newChanListener(rawLn.Addr(), 64)
	go demuxAccept(rawLn, tlsLn, plainLn)
	plainSrv = &http.Server{
		Handler:           httpsRedirectHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		_ = plainSrv.Serve(plainLn)
	}()
	// Задача 10: HTTP/3 (QUIC) — отдельный UDP-сокет на том же порту.
	// Браузер видит Alt-Svc и сам уходит на h3, если сеть позволяет.
	if strings.ToLower(strings.TrimSpace(os.Getenv("BRIEFLY_H3"))) != "0" {
		h3Server = &http3.Server{
			Addr: addr,
			TLSConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				NextProtos: []string{"h3"},
			},
		}
		go func() {
			if err := h3Server.ListenAndServeTLS(tlsCertPath, tlsKeyPath); err != nil &&
				!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				slog.Warn("HTTP/3: QUIC недоступен, продолжаем по TCP", "error", err)
			}
		}()
	}
	return
}
