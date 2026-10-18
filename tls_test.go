package main

import (
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
)

func TestCertCoversHosts(t *testing.T) {
	cert, err := loadOrGenerateCert()
	if err != nil {
		t.Fatalf("loadOrGenerateCert failed: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate failed: %v", err)
	}

	if !certCoversHosts(leaf, []string{"localhost", "127.0.0.1"}) {
		t.Errorf("certCoversHosts should return true for localhost and 127.0.0.1")
	}
	if certCoversHosts(leaf, []string{"nonexistent.example.org"}) {
		t.Errorf("certCoversHosts should return false for nonexistent host")
	}
}

func TestTLSErrorFilter(t *testing.T) {
	var buf strings.Builder
	logger := newTLSErrorFilterLogger(&buf)

	// Filtered messages
	logger.Print("http: TLS handshake error from 192.168.1.97:42220: remote error: tls: unknown certificate")
	logger.Print("http: TLS handshake error from 127.0.0.1:55555: EOF")
	logger.Print("http: TLS handshake error from 192.168.1.97:42224: remote error: tls: bad certificate")
	if buf.Len() != 0 {
		t.Errorf("expected benign TLS errors to be filtered, got: %q", buf.String())
	}

	// Real unexpected error should pass through
	logger.Print("http: unexpected panic in request handler")
	if !strings.Contains(buf.String(), "unexpected panic") {
		t.Errorf("expected genuine error to be logged, got: %q", buf.String())
	}
}

func TestSetupTLSHTTP3(t *testing.T) {
	t.Setenv("BRIEFLY_TLS", "1")
	t.Setenv("BRIEFLY_H3", "1")
	t.Setenv("BRIEFLY_PORT", "3998")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	scheme, tlsLn, plainSrv, h3Server, rawLn, done, cert := setupTLS("127.0.0.1:3998", handler)
	if scheme != "https" {
		t.Fatalf("expected scheme https, got %q", scheme)
	}
	if tlsLn == nil || rawLn == nil || done == nil || len(cert.Certificate) == 0 {
		t.Fatalf("setupTLS returned incomplete components")
	}

	// Cleanup
	close(done)
	_ = rawLn.Close()
	if plainSrv != nil {
		_ = plainSrv.Close()
	}
	if h3Server != nil {
		_ = h3Server.Close()
	}
}
