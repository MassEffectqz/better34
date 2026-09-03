package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	indexHTML  []byte
	indexOnce  sync.Once
	indexError error
	versionMu  sync.Mutex
	versionVal string
	lastWalk   time.Time
	// distBundleOK — собран ли esbuild-бандл (static/js/dist/app.js).
	// Обновляется вместе с перечитыванием index.html: пересборка бандла
	// меняет mtime дерева static/ и, значит, версию.
	distBundleOK bool
	// distAppJSPath — точка входа фронтенда в собранном виде.
	distAppJSPath = filepath.Join("static", "js", "dist", "app.js")
)

func staticVersion() string {
	versionMu.Lock()
	defer versionMu.Unlock()
	// В проде версию достаточно пересчитывать раз в 30с: walk по дереву
	// static/ на каждый запрос главной не нужен. BRIEFLY_DEBUG=1 —
	// пересчёт на каждый вызов, чтобы правки фронтенда подхватывались сразу.
	ttl := 30 * time.Second
	if debugEnabled() {
		ttl = 0
	}
	if versionVal != "" && time.Since(lastWalk) < ttl {
		return versionVal
	}
	lastWalk = time.Now()
	var latest int64
	filepath.Walk("static", func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().UnixNano() > latest {
			latest = info.ModTime().UnixNano()
		}
		return nil
	})
	v := strconv.FormatInt(latest, 16)
	if v != versionVal {
		versionVal = v
		indexOnce = sync.Once{}
		indexHTML, indexError = os.ReadFile("static/index.html")
		_, distErr := os.Stat(distAppJSPath)
		distBundleOK = distErr == nil
	}
	return v
}

func loadIndex() ([]byte, error) {
	staticVersion()
	versionMu.Lock()
	defer versionMu.Unlock()
	return indexHTML, indexError
}

func serveIndex(c *gin.Context) {
	html, err := loadIndex()
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html missing")
		return
	}
	v := staticVersion()
	out := string(html)
	// Без BRIEFLY_DEBUG отдаём собранный esbuild-бандл вместо графа из
	// ~16 ES-модулей: один запрос вместо каскада 304-ревалидаций (на
	// мобильном интернете/слабом Wi-Fi это секунды до старта приложения).
	if distBundleOK && !debugEnabled() {
		out = strings.Replace(out,
			`<script type="module" src="/static/js/app.js?v=__VERSION__"></script>`,
			`<script type="module" src="/static/js/dist/app.js?v=__VERSION__"></script>`, 1)
	}
	out = strings.ReplaceAll(out, "__VERSION__", v)
	// Legacy-токен (BRIEFLY_TOKEN) сознательно НЕ вшивается в HTML:
	// страница отдаётся без проверки сессии, и любой посетитель LAN
	// видел бы секрет, полностью обходящий аккаунты. Токен-режим
	// работает только для клиентов, передающих заголовок сами.
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(out))
}
