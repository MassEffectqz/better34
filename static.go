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
	// refreshing — идёт ли фоновый пересчёт версии: singleflight, чтобы при
	// наплыве запросов после TTL не плодилась горутина на каждый запрос.
	refreshing bool
	// distBundleOK — собран ли esbuild-бандл (static/js/dist/app.js).
	// Обновляется вместе с перечитыванием index.html: пересборка бандла
	// меняет mtime дерева static/ и, значит, версию.
	distBundleOK bool
	// distAppJSPath — точка входа фронтенда в собранном виде.
	distAppJSPath = filepath.Join("static", "js", "dist", "app.js")
)

func staticVersionTTL() time.Duration {
	if debugEnabled() {
		return 0
	}
	return 30 * time.Second
}

// walkStaticVersion пересчитывает версию статики и перечитывает index.html.
// walk по дереву static/ идёт БЕЗ versionMu: он долгий и не должен блокировать
// синхронный путь loadIndex/staticVersion (P2-11). force — принудительный
// пересчёт (debug-режим: правки фронтенда подхватываются сразу).
func walkStaticVersion(force bool) {
	versionMu.Lock()
	if !force && versionVal != "" && time.Since(lastWalk) < staticVersionTTL() {
		refreshing = false
		versionMu.Unlock()
		return // уже свежая
	}
	versionMu.Unlock()

	var latest int64
	filepath.Walk("static", func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().UnixNano() > latest {
			latest = info.ModTime().UnixNano()
		}
		return nil
	})
	v := strconv.FormatInt(latest, 16)

	versionMu.Lock()
	lastWalk = time.Now()
	refreshing = false
	if v != versionVal {
		versionVal = v
		indexOnce = sync.Once{}
		indexHTML, indexError = os.ReadFile("static/index.html")
		_, distErr := os.Stat(distAppJSPath)
		distBundleOK = distErr == nil
	}
	versionMu.Unlock()
}

// staticVersion возвращает версию статики. TTL-просрочка отдаёт старую версию,
// а пересчёт запускает один раз (dedup по refreshing), а не в горутине на
// каждый запрос. Первый вызов и debug считают синхронно.
func staticVersion() string {
	if staticVersionTTL() == 0 {
		// debug: синхронный пересчёт на каждый вызов.
		walkStaticVersion(true)
		versionMu.Lock()
		v := versionVal
		versionMu.Unlock()
		return v
	}
	versionMu.Lock()
	if versionVal != "" && time.Since(lastWalk) < staticVersionTTL() {
		v := versionVal
		versionMu.Unlock()
		return v
	}
	if versionVal != "" {
		// Stale-while-revalidate: сразу отдаём прошлую версию, пересчёт — в фон.
		if !refreshing {
			refreshing = true
			go walkStaticVersion(false)
		}
		v := versionVal
		versionMu.Unlock()
		return v
	}
	versionMu.Unlock()
	// Первый запрос после старта: считаем синхронно.
	walkStaticVersion(false)
	versionMu.Lock()
	v := versionVal
	versionMu.Unlock()
	return v
}

// refreshStaticVersion пересчитывает версию и index.html (совместимость).
func refreshStaticVersion() {
	walkStaticVersion(false)
}

func loadIndex() ([]byte, error) {
	staticVersion()
	versionMu.Lock()
	defer versionMu.Unlock()
	return indexHTML, indexError
}

func serveIndex(c *gin.Context) {
	staticVersion() // первый вызов/запуск фонового пересчёта до снимка
	// Единый снимок под одной блокировкой: иначе versionVal/distBundleOK
	// читались бы параллельно с записями в walkStaticVersion (data race).
	versionMu.Lock()
	v, html, bundleOK, err := versionVal, indexHTML, distBundleOK, indexError
	versionMu.Unlock()
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html missing")
		return
	}
	out := string(html)
	// Без BRIEFLY_DEBUG отдаём собранный esbuild-бандл вместо графа из
	// ~16 ES-модулей: один запрос вместо каскада 304-ревалидаций (на
	// мобильном интернете/слабом Wi-Fi это секунды до старта приложения).
	if bundleOK && !debugEnabled() {
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
