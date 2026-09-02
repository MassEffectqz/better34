package internal

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Автобэкап БД: раз в сутки выполняется VACUUM INTO — снимок пишется в новый
// файл, не мешая читателям/писателю основного соединения (WAL), и заодно
// уплотняет базу. Ротация: файлы старше N дней удаляются, при превышении
// лимита количества — сначала самые старые. Управляется переменными
// BRIEFLY_DB_BACKUPS (сколько копий хранить, 0 — выключить) и
// BRIEFLY_DB_BACKUP_DAYS (максимальный возраст копии, 0 — без ограничения).

const dbBackupInterval = 24 * time.Hour

var backupOnce sync.Once

// StartDBAutoBackup запускает фоновый цикл автобэкапа. Реакция на вызов
// ленивая: без BRIEFLY_DB_BACKUPS (и без включённой настройкой) ничего не
// происходит. Вызывается из GetDB — в тестах с tmp-БД директории backup нет.
func StartDBAutoBackup(db *PostDB) {
	backupOnce.Do(func() {
		quota := dbBackupQuota()
		if quota <= 0 {
			return
		}
		go runBackupLoop(db)
	})
}

func runBackupLoop(db *PostDB) {
	// Первый бэкап — shortly after старта (дать схеме/импорту доучиться),
	// дальше раз в сутки. Лог пишем через slog-логгер main (logOutput).
	for {
		time.Sleep(2 * time.Minute)
		if err := db.BackupNow(); err != nil {
			if !isNoBackupDir(err) {
				log.Printf("[db] backup: %v", err)
			}
		}
		time.Sleep(dbBackupInterval - 2*time.Minute)
	}
}

func isNoBackupDir(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "backup dir unavailable")
}

// BackupNow выполняет один бэкап: VACUUM INTO data/backups/posts-YYYYMMDD-HHMMSS.db
// плюс ротация. Экспортируемо для интеграционного теста.
func (db *PostDB) BackupNow() error {
	dir := filepath.Join(filepath.Dir(db.path), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("backup dir unavailable: %w", err)
	}

	// VACUUM INTO нельзя выполнять в транзакции — открываем отдельное
	// соединение. Ночная вакуумизация попутно уплотняет файл.
	conn, err := sql.Open("sqlite", db.path)
	if err != nil {
		return fmt.Errorf("backup open: %w", err)
	}
	defer conn.Close()

	name := "posts-" + time.Now().Format("20060102-150405") + ".db"
	dst := filepath.Join(dir, name)
	if _, err := conn.Exec("VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("backup vacuum: %w", err)
	}
	_ = os.Chmod(dst, 0o600)
	log.Printf("[db] backup created: %s", dst)

	pruneBackups(dir, dbBackupQuota(), dbBackupMaxAge())
	return nil
}

// pruneBackups удаляет самые старые копии при превышении лимита количества
// и все копии старше максимального возраста.
func pruneBackups(dir string, quota, maxAge int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type backupFile struct {
		name string
		path string
		ts   time.Time
		size int64
	}
	var files []backupFile
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "posts-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, backupFile{
			name: e.Name(),
			path: filepath.Join(dir, e.Name()),
			ts:   info.ModTime(),
			size: info.Size(),
		})
		total += info.Size()
	}
	if len(files) == 0 {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ts.Before(files[j].ts) })

	var removed int
	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().AddDate(0, 0, -maxAge)
	}
	var kept []backupFile
	for _, f := range files {
		if !cutoff.IsZero() && f.ts.Before(cutoff) {
			if os.Remove(f.path) == nil {
				removed++
				total -= f.size
			}
			continue
		}
		kept = append(kept, f)
	}
	if quota > 0 {
		for len(kept) > quota {
			if os.Remove(kept[0].path) == nil {
				removed++
				total -= kept[0].size
			}
			kept = kept[1:]
		}
	}
	if removed > 0 {
		log.Printf("[db] backup pruned: %d файлов осталось, %.1f MB", len(kept), float64(total)/1024/1024)
	}
}

func dbBackupQuota() int {
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_DB_BACKUPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 7
}

func dbBackupMaxAge() int {
	if v := strings.TrimSpace(os.Getenv("BRIEFLY_DB_BACKUP_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 30
}
