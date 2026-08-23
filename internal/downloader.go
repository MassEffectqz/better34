package internal

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var ErrCancelled = errors.New("download cancelled")

// maxDownloadSize — лимит размера скачиваемого файла (совпадает с лимитом
// proxyCache): защита от случайного заполнения диска гигабайтным файлом.
const maxDownloadSize = 4 << 20

type DownloadJob struct {
	PostID   int
	FileURL  string
	FileType string
	// Referer сайта-источника поста (CDN некоторых сайтов проверяет его).
	// Пусто → легаси-значение rule34.xxx для задач из старой очереди.
	Referer string
}

type DownloadResult struct {
	PostID      int
	FilePath    string
	ThumbPath   string
	DuplicateOf int
	Error       error
}

type Downloader struct {
	queue         []DownloadJob
	queueMu       sync.Mutex
	queueCond     *sync.Cond
	queueFile     string
	results       chan DownloadResult
	storedResults []DownloadResult
	resultsMu     sync.Mutex
	doneIDs       []int
	workers       int
	wg            sync.WaitGroup
	client        *http.Client
	thumb         *ThumbnailGenerator
	onResult      func(DownloadResult)

	paused       atomic.Bool
	activeIDs    map[int]bool
	cancelled    map[int]bool
	activeCancel map[int]context.CancelFunc
	stopCh       chan struct{}
	closeOnce    sync.Once

	queueLen    atomic.Int32
	activeCount atomic.Int32
	doneCount   atomic.Int32
}

func NewDownloader(workers int, onResult func(DownloadResult)) *Downloader {
	if workers <= 0 {
		workers = 3
	}

	transport := buildTransport()

	d := &Downloader{
		queue:        make([]DownloadJob, 0, 100),
		results:      make(chan DownloadResult, 100),
		workers:      workers,
		onResult:     onResult,
		activeIDs:    make(map[int]bool),
		cancelled:    make(map[int]bool),
		activeCancel: make(map[int]context.CancelFunc),
		stopCh:       make(chan struct{}),
		client: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:       workers * 2,
				IdleConnTimeout:    0,
				DisableCompression: true,
				DialContext:        transport.DialContext,
			},
		},
		thumb: NewThumbnailGenerator(),
	}
	d.queueCond = sync.NewCond(&d.queueMu)
	d.queueFile = filepath.Join("data", "download_queue.json")
	d.loadQueue()

	go func() {
		for result := range d.results {
			if d.onResult != nil {
				d.onResult(result)
			}
			if result.Error == nil && result.DuplicateOf == 0 {
				db := GetDB()
				db.SetDownloaded(result.PostID, result.FilePath, result.ThumbPath)
				db.BumpSave()
			}
			payload := map[string]any{
				"type":    "result",
				"post_id": result.PostID,
				"success": result.Error == nil,
			}
			if result.Error != nil {
				payload["error"] = result.Error.Error()
			}
			if result.DuplicateOf > 0 {
				payload["duplicate_of"] = result.DuplicateOf
				payload["success"] = false
			}
			publishSSE(payload)
			d.resultsMu.Lock()
			d.storedResults = append(d.storedResults, result)
			if len(d.storedResults) > 64 {
				d.storedResults = d.storedResults[len(d.storedResults)-64:]
			}
			d.doneIDs = append(d.doneIDs, result.PostID)
			if len(d.doneIDs) > 16 {
				d.doneIDs = d.doneIDs[len(d.doneIDs)-16:]
			}
			d.resultsMu.Unlock()
			d.publishStatus()
		}
	}()

	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}

	return d
}

func (d *Downloader) loadQueue() {
	data, err := os.ReadFile(d.queueFile)
	if err != nil {
		return
	}
	var jobs []DownloadJob
	if json.Unmarshal(data, &jobs) != nil || len(jobs) == 0 {
		return
	}
	d.queue = jobs
	d.queueLen.Store(int32(len(jobs)))
	log.Printf("Restored %d queued downloads from %s", len(jobs), d.queueFile)
}

func (d *Downloader) persistQueue() {
	d.queueMu.Lock()
	jobs := make([]DownloadJob, len(d.queue))
	copy(jobs, d.queue)
	d.queueMu.Unlock()
	if len(jobs) == 0 {
		os.Remove(d.queueFile)
		return
	}
	data, err := json.Marshal(jobs)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(d.queueFile), 0755)
	if err := os.WriteFile(d.queueFile, data, 0644); err != nil {
		log.Printf("failed to persist download queue: %v", err)
	}
}

func (d *Downloader) worker() {
	defer d.wg.Done()

	var job DownloadJob
	for {
		d.queueMu.Lock()
		for !d.isStopped() && (d.paused.Load() || len(d.queue) == 0) {
			d.queueCond.Wait()
		}
		if d.isStopped() {
			d.queueMu.Unlock()
			return
		}
		job = d.queue[0]
		d.queue = d.queue[1:]
		d.queueMu.Unlock()
		d.persistQueue()

		d.queueMu.Lock()
		cancelled := d.cancelled[job.PostID]
		d.queueMu.Unlock()
		if cancelled {
			d.queueLen.Add(-1)
			d.queueMu.Lock()
			delete(d.cancelled, job.PostID)
			d.queueMu.Unlock()
			continue
		}

		d.queueLen.Add(-1)
		d.activeCount.Add(1)
		ctx, cancelCtx := context.WithCancel(context.Background())
		d.queueMu.Lock()
		d.activeIDs[job.PostID] = true
		d.activeCancel[job.PostID] = cancelCtx
		wasCancelled := d.cancelled[job.PostID]
		d.queueMu.Unlock()
		if wasCancelled {
			cancelCtx()
			d.queueMu.Lock()
			delete(d.activeIDs, job.PostID)
			delete(d.activeCancel, job.PostID)
			delete(d.cancelled, job.PostID)
			d.queueMu.Unlock()
			d.activeCount.Add(-1)
			continue
		}

		result := d.downloadFile(ctx, job)

		d.queueMu.Lock()
		delete(d.activeIDs, job.PostID)
		delete(d.activeCancel, job.PostID)
		d.queueMu.Unlock()
		cancelCtx()
		d.activeCount.Add(-1)
		if errors.Is(result.Error, ErrCancelled) {
			d.queueMu.Lock()
			delete(d.cancelled, job.PostID)
			d.queueMu.Unlock()
			continue
		}
		d.doneCount.Add(1)
		d.results <- result
	}
}

func (d *Downloader) isStopped() bool {
	select {
	case <-d.stopCh:
		return true
	default:
		return false
	}
}

func (d *Downloader) downloadFile(ctx context.Context, job DownloadJob) DownloadResult {
	savePath := filepath.Join(GetConfig().GetDownloadPath(), fmt.Sprintf("%d", job.PostID))
	if err := os.MkdirAll(savePath, 0755); err != nil {
		return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("failed to create dir: %w", err)}
	}

	ext := filepath.Ext(job.FileURL)
	if ext == "" {
		ext = ".jpg"
	}
	filePath := filepath.Join(savePath, fmt.Sprintf("original%s", ext))

	if _, err := os.Stat(filePath); err == nil {
		thumbPath, _ := d.thumb.Generate(filePath, job.PostID)
		return DownloadResult{
			PostID:    job.PostID,
			FilePath:  filePath,
			ThumbPath: thumbPath,
		}
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return DownloadResult{PostID: job.PostID, Error: ErrCancelled}
		}

		partialPath := filePath + ".part"
		resumeFrom := int64(0)
		if st, err := os.Stat(partialPath); err == nil && st.Size() > 0 {
			resumeFrom = st.Size()
			if resumeFrom > maxDownloadSize {
				os.Remove(partialPath)
				return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("file exceeds size limit (%d bytes)", maxDownloadSize)}
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", job.FileURL, nil)
		if err != nil {
			return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("failed to create request: %w", err)}
		}
		req.Header.Set("User-Agent", "Briefly/1.0")
		referer := job.Referer
		if referer == "" {
			referer = "https://rule34.xxx/"
		}
		req.Header.Set("Referer", referer)
		if resumeFrom > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
		}

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("download failed: %w", err)
			if errors.Is(err, context.Canceled) {
				return DownloadResult{PostID: job.PostID, Error: ErrCancelled}
			}
			if attempt < 3 {
				log.Printf("retry %d/%d for post %d: %v", attempt, 3, job.PostID, err)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK, http.StatusPartialContent:
			// 200 — сервер проигнорировал Range, пишем с нуля; 206 — докачиваем.
		case http.StatusRequestedRangeNotSatisfiable:
			// .part уже содержит весь файл.
			resp.Body.Close()
			if st, err := os.Stat(partialPath); err == nil && st.Size() > maxDownloadSize {
				os.Remove(partialPath)
				return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("file exceeds size limit (%d bytes)", maxDownloadSize)}
			}
			if err := os.Rename(partialPath, filePath); err != nil {
				return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("failed to rename file: %w", err)}
			}
			thumbPath, err := d.thumb.Generate(filePath, job.PostID)
			return DownloadResult{
				PostID:    job.PostID,
				FilePath:  filePath,
				ThumbPath: thumbPath,
				Error:     err,
			}
		default:
			lastErr = fmt.Errorf("download failed with status %d", resp.StatusCode)
			resp.Body.Close()
			if resp.StatusCode >= 500 && attempt < 3 {
				log.Printf("retry %d/%d for post %d: status %d", attempt, 3, job.PostID, resp.StatusCode)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
				continue
			}
			return DownloadResult{PostID: job.PostID, Error: lastErr}
		}

		if resp.ContentLength > maxDownloadSize {
			resp.Body.Close()
			return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("file too large: %d bytes (limit %d)", resp.ContentLength, maxDownloadSize)}
		}

		if resp.StatusCode == http.StatusOK {
			resumeFrom = 0
		}

		var outFile *os.File
		if resumeFrom > 0 {
			outFile, err = os.OpenFile(partialPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		} else {
			outFile, err = os.Create(partialPath)
		}
		if err != nil {
			resp.Body.Close()
			return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("failed to create file: %w", err)}
		}

		written, copyErr := io.Copy(outFile, io.LimitReader(resp.Body, maxDownloadSize-resumeFrom+1))
		resp.Body.Close()
		outFile.Close()
		if copyErr != nil {
			if errors.Is(copyErr, context.Canceled) || ctx.Err() != nil {
				return DownloadResult{PostID: job.PostID, Error: ErrCancelled}
			}
			lastErr = fmt.Errorf("failed to write file: %w", copyErr)
			if attempt < 3 {
				log.Printf("retry %d/%d for post %d: %v", attempt, 3, job.PostID, copyErr)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			continue
		}

		if written > maxDownloadSize-resumeFrom {
			os.Remove(partialPath)
			return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("file exceeds size limit (%d bytes)", maxDownloadSize)}
		}

		if written == 0 {
			lastErr = fmt.Errorf("downloaded file is empty")
			if attempt < 3 {
				log.Printf("retry %d/%d for post %d: empty response", attempt, 3, job.PostID)
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
			continue
		}

		if dup := d.checkDuplicate(partialPath, job.PostID); dup > 0 {
			return DownloadResult{PostID: job.PostID, DuplicateOf: dup}
		}

		if err := os.Rename(partialPath, filePath); err != nil {
			os.Remove(partialPath)
			return DownloadResult{PostID: job.PostID, Error: fmt.Errorf("failed to rename file: %w", err)}
		}

		thumbPath, err := d.thumb.Generate(filePath, job.PostID)
		return DownloadResult{
			PostID:    job.PostID,
			FilePath:  filePath,
			ThumbPath: thumbPath,
			Error:     err,
		}
	}

	return DownloadResult{PostID: job.PostID, Error: lastErr}
}

// checkDuplicate считает md5 скачанного файла и ищет совпадение среди уже
// скачанных постов. При дубликате удаляет .part и возвращает id оригинала.
// Хэш сохраняется в БД, чтобы последующие проверки работали без пересчёта.
func (d *Downloader) checkDuplicate(path string, postID int) int {
	if !DBReady() {
		return 0 // глобальной БД нет (тесты/утилиты) — дедуп неприменим
	}
	sum, err := md5File(path)
	if err != nil {
		return 0
	}
	db := GetDB()
	db.SetPostMD5(postID, sum)
	if exist := db.FindDownloadedByMd5(sum, postID); exist > 0 {
		os.Remove(path)
		log.Printf("dedup: пост %d — дубликат #%d (md5 %s...), файл не сохранён", postID, exist, sum[:8])
		return exist
	}
	return 0
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (d *Downloader) Submit(job DownloadJob) {
	d.queueMu.Lock()
	for _, j := range d.queue {
		if j.PostID == job.PostID {
			d.queueMu.Unlock()
			return
		}
	}
	if d.activeIDs[job.PostID] {
		d.queueMu.Unlock()
		return
	}
	d.cancelled[job.PostID] = false
	d.queue = append(d.queue, job)
	d.queueLen.Add(1)
	d.queueMu.Unlock()
	d.queueCond.Signal()
	d.persistQueue()
	d.publishStatus()
}

func (d *Downloader) Pause() {
	d.paused.Store(true)
	d.publishStatus()
}

func (d *Downloader) Resume() {
	d.paused.Store(false)
	d.queueCond.Broadcast()
	d.publishStatus()
}

func (d *Downloader) Cancel(id int) {
	d.queueMu.Lock()
	d.cancelled[id] = true
	for i, j := range d.queue {
		if j.PostID == id {
			d.queue = append(d.queue[:i], d.queue[i+1:]...)
			d.queueLen.Add(-1)
			break
		}
	}
	cancelCtx := d.activeCancel[id]
	d.queueMu.Unlock()
	if cancelCtx != nil {
		cancelCtx()
	}
	d.persistQueue()
	d.publishStatus()
}

func (d *Downloader) RebuildClient() {
	transport := buildTransport()
	d.client.Transport = &http.Transport{
		MaxIdleConns:       d.workers * 2,
		IdleConnTimeout:    0,
		DisableCompression: true,
		DialContext:        transport.DialContext,
	}
}

func (d *Downloader) MoveUp(id int) bool {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	for i, j := range d.queue {
		if j.PostID == id && i > 0 {
			d.queue[i], d.queue[i-1] = d.queue[i-1], d.queue[i]
			return true
		}
	}
	return false
}

func (d *Downloader) MoveDown(id int) bool {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	for i, j := range d.queue {
		if j.PostID == id && i < len(d.queue)-1 {
			d.queue[i], d.queue[i+1] = d.queue[i+1], d.queue[i]
			return true
		}
	}
	return false
}

func (d *Downloader) QueueList() []DownloadJob {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	out := make([]DownloadJob, len(d.queue))
	copy(out, d.queue)
	return out
}

func (d *Downloader) setQueueFile(path string) {
	d.queueFile = path
}

func (d *Downloader) ActiveIDs() []int {
	d.queueMu.Lock()
	defer d.queueMu.Unlock()
	out := make([]int, 0, len(d.activeIDs))
	for id := range d.activeIDs {
		out = append(out, id)
	}
	return out
}

func (d *Downloader) Status() (queued, active, done int) {
	return int(d.queueLen.Load()), int(d.activeCount.Load()), int(d.doneCount.Load())
}

func (d *Downloader) publishStatus() {
	q, a, done := d.Status()
	publishSSE(map[string]any{
		"type":     "status",
		"queued":   q,
		"active":   a,
		"done":     done,
		"done_ids": d.RecentDoneIDs(),
	})
}

func (d *Downloader) ConsumeResults() []DownloadResult {
	d.resultsMu.Lock()
	defer d.resultsMu.Unlock()
	results := d.storedResults
	d.storedResults = nil
	return results
}

func (d *Downloader) RecentDoneIDs() []int {
	d.resultsMu.Lock()
	defer d.resultsMu.Unlock()
	out := make([]int, len(d.doneIDs))
	copy(out, d.doneIDs)
	return out
}

func (d *Downloader) Close() {
	d.closeOnce.Do(func() {
		close(d.stopCh)
		d.queueCond.Broadcast()
		d.wg.Wait()
		close(d.results)
	})
}
