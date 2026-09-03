package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File

	dirReady bool // папка лога создана при старте
}

// logDir — отдельная папка для логов: data/logs по умолчанию, путь
// переопределяется BRIEFLY_LOG_DIR. Переменная может прийти из .env
// (loadDotEnv() в main()), поэтому вычисляется лениво.
var (
	logDir     string
	logDirOnce sync.Once
)

func resolveLogDir() string {
	logDirOnce.Do(func() {
		logDir = strings.TrimSpace(os.Getenv("BRIEFLY_LOG_DIR"))
		if logDir == "" {
			logDir = "data/logs"
		}
	})
	return logDir
}

// newRotatingWriter заранее создаёт папку под лог-файл, чтобы OpenFile
// в rotate() не молча терял лог на первом запуске.
func newRotatingWriter(path string, maxBytes int64) *rotatingWriter {
	w := &rotatingWriter{path: path, maxBytes: maxBytes}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		w.dirReady = os.MkdirAll(dir, 0o755) == nil
	} else {
		w.dirReady = true
	}
	w.rotate()
	return w
}

func (w *rotatingWriter) rotate() {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	if info, err := os.Stat(w.path); err == nil && info.Size() >= w.maxBytes {
		os.Rename(w.path, w.path+".old")
	}
	// Страховка: папку лога могли удалить на работающем сервере.
	if !w.dirReady {
		if dir := filepath.Dir(w.path); dir != "" && dir != "." {
			w.dirReady = os.MkdirAll(dir, 0o755) == nil
		} else {
			w.dirReady = true
		}
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	w.file = f
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		w.rotate()
		if w.file == nil {
			return len(p), nil
		}
	}
	n, err := w.file.Write(p)
	if err == nil {
		if info, statErr := w.file.Stat(); statErr == nil && info.Size() >= w.maxBytes {
			w.rotate()
		}
	}
	return n, err
}

// logOutput — общий вывод логов: консоль + ротируемый файл (10 МБ) в
// отдельной папке (data/logs, путь меняет BRIEFLY_LOG_DIR).
// Один rotator на процесс: и slog, и пакет log пишут в него.
var logOutput = io.MultiWriter(os.Stdout, newRotatingWriter(filepath.Join(resolveLogDir(), "briefly.log"), 10<<20))

// newSlogLogger собирает структурированный логгер (текстовый, поддержка
// уровней). Уровень задаёт BRIEFLY_LOG_LEVEL: debug|info|warn|error.
func newSlogLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: logLevelFromEnv()}))
}

func logLevelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BRIEFLY_LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// loadDotEnv — простая загрузка .env без сторонних зависимостей: KEY=VALUE
// построчно, поддерживаются комментарии (#) и кавычки. Уже заданные переменные
// окружения имеют приоритет и не перезаписываются. Прочитанные значения не
// успевают к переменным, инициализируемым на этапе package-init
// (BRIEFLY_ALLOWED_HOSTS, BRIEFLY_TOKEN — ими управляет run.ps1).
func loadDotEnv() {
	for _, path := range []string{".env", "data/.env"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(ln)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if k == "" || os.Getenv(k) != "" {
				continue
			}
			os.Setenv(k, v)
		}
	}
}
