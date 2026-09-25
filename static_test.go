package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
)

func TestEmbeddedFrontendWorksWithoutDisk(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := readStaticFile("index.html"); err != nil {
		t.Fatalf("embedded index.html unavailable: %v", err)
	}
	matches, err := fs.Glob(embeddedStatic(), "js/tests/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("frontend tests must not be embedded: %v", matches)
	}
}

func TestFrontendRoutesUseEmbeddedFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(staticCacheMiddleware())
	mountFrontend(r)
	r.NoRoute(serveIndex)

	for _, route := range []string{"/", "/static/css/style.css", "/static/js/dist/app.js", "/sw.js"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK || resp.Body.Len() == 0 {
			t.Errorf("%s: status=%d body=%d", route, resp.Code, resp.Body.Len())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/static/js/dist/app.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || resp.Header().Get("Content-Encoding") != "br" {
		t.Errorf("embedded brotli: status=%d encoding=%q", resp.Code, resp.Header().Get("Content-Encoding"))
	}
}

func TestPrecompressedBundleIsDecodable(t *testing.T) {
	raw, err := readStaticFile("js/dist/app.js")
	if err != nil {
		t.Fatalf("bundle unavailable: %v", err)
	}
	for _, suffix := range []string{".br", ".gz"} {
		name := "js/dist/app.js" + suffix
		data, err := readStaticFile(name)
		if err != nil {
			t.Logf("%s not embedded, skipped", name)
			continue
		}
		var out []byte
		switch suffix {
		case ".br":
			out, err = io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
		case ".gz":
			var zr *gzip.Reader
			zr, err = gzip.NewReader(bytes.NewReader(data))
			if err == nil {
				out, err = io.ReadAll(zr)
				_ = zr.Close()
			}
		}
		if err != nil {
			t.Fatalf("%s is not decodable: %v", name, err)
		}
		if !bytes.Equal(out, raw) {
			t.Fatalf("%s does not match js/dist/app.js (%d vs %d bytes)", name, len(out), len(raw))
		}
	}
}

func TestBrokenPrecompressedFallsBackToPlainFile(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "static", "js", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := []byte("console.log(1)")
	if err := os.WriteFile(filepath.Join(dist, "app.js"), plain, 0o644); err != nil {
		t.Fatal(err)
	}
	// Мусор вместо валидного brotli/gzip — именно так выглядел битый .br,
	// который ломал загрузку страницы (ERR_CONTENT_DECODING_FAILED).
	if err := os.WriteFile(filepath.Join(dist, "app.js.br"), []byte("not-brotli-at-all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "app.js.gz"), []byte("not-gzip-at-all"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(staticCacheMiddleware())
	r.Use(gzipMiddleware())
	mountFrontend(r)
	r.NoRoute(serveIndex)

	req := httptest.NewRequest(http.MethodGet, "/static/js/dist/app.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	// Битый .br не должен отдаваться как готовый ответ: сервер обязан
	// отдать пересжатую на лету (или несжатую) версию, которую браузер прочтёт.
	raw := resp.Body.Bytes()
	var decoded []byte
	var derr error
	switch enc := resp.Header().Get("Content-Encoding"); enc {
	case "br":
		decoded, derr = io.ReadAll(brotli.NewReader(bytes.NewReader(raw)))
	case "gzip":
		var zr *gzip.Reader
		zr, derr = gzip.NewReader(bytes.NewReader(raw))
		if derr == nil {
			decoded, derr = io.ReadAll(zr)
			_ = zr.Close()
		}
	default:
		decoded = raw
	}
	if derr != nil {
		t.Fatalf("served body with encoding %q is not decodable: %v",
			resp.Header().Get("Content-Encoding"), derr)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("decoded body = %q, want %q", decoded, plain)
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d", resp.Code)
	}
}

func TestLocalFrontendOverridesEmbeddedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "static", "index.html"), []byte("local index"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("BRIEFLY_DEBUG", "1")
	versionMu.Lock()
	versionVal, lastWalk, indexHTML, indexError = "", time.Time{}, nil, nil
	versionMu.Unlock()

	data, err := readStaticFile("index.html")
	if err != nil || string(data) != "local index" {
		t.Fatalf("local override failed: data=%q err=%v", data, err)
	}
}

func TestCleanStaticNameRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../secret", "a/../../secret", "/../secret"} {
		if _, err := cleanStaticName(name); err == nil {
			t.Errorf("cleanStaticName(%q) accepted traversal", name)
		}
	}
	if got, err := cleanStaticName("css/style.css"); err != nil || got != "css/style.css" {
		t.Fatalf("cleanStaticName valid path = %q, %v", got, err)
	}
	if embeddedVersion() == "" {
		t.Fatal("embedded version is empty")
	}
}
