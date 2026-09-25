package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Production frontend only: static/js/tests is intentionally not embedded.
//
//go:embed static/index.html static/offline.html static/manifest.webmanifest static/sw.js
//go:embed static/css static/js/*.js static/js/dist static/icons
var frontendFS embed.FS

var (
	indexHTML  []byte
	indexError error
	indexOnce  sync.Once

	versionMu    sync.Mutex
	versionVal   string
	lastWalk     time.Time
	refreshing   bool
	distBundleOK bool
)

func embeddedStatic() fs.FS {
	sub, err := fs.Sub(frontendFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

type localOverlayFS struct{}

func cleanStaticName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
		return "", os.ErrInvalid
	}
	return name, nil
}

func (localOverlayFS) Open(name string) (fs.File, error) {
	clean, err := cleanStaticName(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if file, diskErr := os.Open(filepath.Join("static", filepath.FromSlash(clean))); diskErr == nil {
		return file, nil
	}
	return embeddedStatic().Open(clean)
}

func readStaticFile(name string) ([]byte, error) {
	clean, err := cleanStaticName(name)
	if err != nil {
		return nil, err
	}
	if file, diskErr := os.Open(filepath.Join("static", filepath.FromSlash(clean))); diskErr == nil {
		defer file.Close()
		return io.ReadAll(file)
	}
	return fs.ReadFile(embeddedStatic(), clean)
}

func staticFileExists(name string) bool {
	clean, err := cleanStaticName(name)
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join("static", filepath.FromSlash(clean))); err == nil {
		return true
	}
	_, err = fs.Stat(embeddedStatic(), clean)
	return err == nil
}

func embeddedVersion() string {
	h := sha256.New()
	for _, name := range []string{"index.html", "js/dist/app.js", "css/style.css"} {
		if data, err := fs.ReadFile(embeddedStatic(), name); err == nil {
			_, _ = h.Write([]byte(name))
			_, _ = h.Write(data)
		}
	}
	return "embedded-" + hex.EncodeToString(h.Sum(nil))[:16]
}

func staticVersionTTL() time.Duration {
	if debugEnabled() {
		return 0
	}
	return 30 * time.Second
}

// walkStaticVersion computes a local overlay version and reloads index.html.
func walkStaticVersion(force bool) {
	versionMu.Lock()
	if !force && versionVal != "" && time.Since(lastWalk) < staticVersionTTL() {
		refreshing = false
		versionMu.Unlock()
		return
	}
	versionMu.Unlock()

	var latest int64
	_ = filepath.Walk("static", func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.ModTime().UnixNano() > latest {
			latest = info.ModTime().UnixNano()
		}
		return nil
	})
	version := "embedded-" + strconv.FormatInt(latest, 16)
	if latest == 0 {
		version = embeddedVersion()
	}

	html, err := readStaticFile("index.html")
	bundleOK := staticFileExists("js/dist/app.js")

	versionMu.Lock()
	lastWalk = time.Now()
	refreshing = false
	if version != versionVal {
		versionVal = version
	}
	indexHTML, indexError = html, err
	distBundleOK = bundleOK
	versionMu.Unlock()
}

func staticVersion() string {
	if staticVersionTTL() == 0 {
		walkStaticVersion(true)
	} else {
		versionMu.Lock()
		empty := versionVal == ""
		stale := !empty && time.Since(lastWalk) >= staticVersionTTL()
		if stale && !refreshing {
			refreshing = true
		}
		versionMu.Unlock()
		if empty {
			walkStaticVersion(false)
		} else if stale {
			go walkStaticVersion(false)
		}
	}
	versionMu.Lock()
	defer versionMu.Unlock()
	return versionVal
}

func refreshStaticVersion() { walkStaticVersion(false) }

func loadIndex() ([]byte, error) {
	staticVersion()
	versionMu.Lock()
	defer versionMu.Unlock()
	return indexHTML, indexError
}

func mountFrontend(r *gin.Engine) {
	staticFS := localOverlayFS{}
	r.StaticFS("/static", http.FS(staticFS))
	r.GET("/sw.js", func(c *gin.Context) {
		http.ServeFileFS(c.Writer, c.Request, staticFS, "sw.js")
	})
}

func serveIndex(c *gin.Context) {
	staticVersion()
	versionMu.Lock()
	v, html, bundleOK, err := versionVal, indexHTML, distBundleOK, indexError
	versionMu.Unlock()
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html missing")
		return
	}
	out := string(html)
	if bundleOK && !debugEnabled() {
		out = strings.Replace(out,
			`<script type="module" src="/static/js/app.js?v=__VERSION__"></script>`,
			`<script type="module" src="/static/js/dist/app.js?v=__VERSION__"></script>`, 1)
	}
	out = strings.ReplaceAll(out, "__VERSION__", v)
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(out))
}
