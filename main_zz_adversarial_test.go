package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// G3: staticVersion() пишет indexHTML/indexError под versionMu, а loadIndex()
// читает их БЕЗ блокировки. При изменении статики (этап разработки, деплой
// на лету) — гонка между пересчётом версии и отдачей страницы.
// Воспроизведение: go test -race . -run TestAdversarialIndexHTMLRace -v
func TestAdversarialIndexHTMLRace(t *testing.T) {
	versionMu.Lock()
	versionVal = "seed-a"
	lastWalk = time.Time{}
	indexOnce = sync.Once{}
	versionMu.Unlock()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			versionMu.Lock()
			versionVal = fmt.Sprintf("bogus-%d", i%2)
			lastWalk = time.Time{}
			versionMu.Unlock()
			staticVersion() // под versionMu пишет indexHTML = os.ReadFile(...)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = loadIndex() // читает indexHTML без versionMu
		}
	}()
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// M1 (проверка): ротация лога на Windows. os.Rename с заменой существующего
// .old должен работать — файл обязан оставаться ограниченным.
// Если ротация сломана, файл вырастет до 1000+ байт при maxBytes=100.
func TestAdversarialRotatingWriterBounds(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "briefly.log")
	w := newRotatingWriter(logPath, 100)
	defer func() {
		if w.file != nil {
			w.file.Close()
		}
	}()

	chunk := make([]byte, 50)
	for i := 0; i < 20; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}
	w.file.Close()
	w.file = nil

	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	t.Logf("log size = %d (maxBytes=100)", st.Size())
	if st.Size() > int64(100+len(chunk)) {
		t.Errorf("M1: ротация сломана — файл лога вырос до %d байт (maxBytes=100)", st.Size())
	}
}
