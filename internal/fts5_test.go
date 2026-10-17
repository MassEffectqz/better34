package internal

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFTS5Support(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE VIRTUAL TABLE test_fts USING fts5(tags);`)
	if err != nil {
		t.Fatalf("FTS5 table creation failed: %v", err)
	}

	_, err = db.Exec(`INSERT INTO test_fts(tags) VALUES('1girl blue_hair smile');`)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	var count int
	err = db.QueryRow(`SELECT count(*) FROM test_fts WHERE test_fts MATCH 'blue_hair';`).Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}
