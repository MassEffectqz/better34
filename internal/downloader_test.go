package internal

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloaderQueue(t *testing.T) {

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fake-image-bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	GetConfig().SetDownloadPath(filepath.Join(dir, "posts"))

	d := NewDownloader(2, nil)
	d.setQueueFile(filepath.Join(dir, "queue.json"))
	defer d.Close()

	d.Pause()

	d.Submit(DownloadJob{PostID: 1, FileURL: srv.URL + "/1.jpg"})
	d.Submit(DownloadJob{PostID: 2, FileURL: srv.URL + "/2.jpg"})
	d.Submit(DownloadJob{PostID: 3, FileURL: srv.URL + "/3.jpg"})

	d.Submit(DownloadJob{PostID: 1, FileURL: srv.URL + "/1.jpg"})

	time.Sleep(50 * time.Millisecond)

	q, a, _ := d.Status()
	if q != 3 || a != 0 {
		t.Errorf("expected queued=3 active=0, got queued=%d active=%d", q, a)
	}

	list := d.QueueList()
	if len(list) != 3 {
		t.Errorf("QueueList returned %d items, expected 3", len(list))
	}

	if !d.MoveUp(3) {
		t.Error("MoveUp(3) should succeed")
	}
	if !d.MoveDown(1) {
		t.Error("MoveDown(1) should succeed")
	}

	d.Cancel(2)

	list = d.QueueList()
	for _, j := range list {
		if j.PostID == 2 {
			t.Error("cancelled job still in queue")
		}
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items after cancel, got %d", len(list))
	}

	d.Resume()
	time.Sleep(200 * time.Millisecond)

	ids := d.ActiveIDs()
	t.Logf("after resume active IDs: %v", ids)

	q2, a2, d2 := d.Status()
	if len(ids) == 0 && q2 == 0 && a2 == 0 && d2 > 0 {
		t.Log("all jobs completed quickly")
	}
}
