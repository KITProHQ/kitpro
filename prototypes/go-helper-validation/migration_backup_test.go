package main

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func TestConsistentWalBackupAndMigration(t *testing.T) {
	d := t.TempDir()
	live := filepath.Join(d, "helper.db")
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "CREATE TABLE schema_version(version INTEGER NOT NULL)", "INSERT INTO schema_version VALUES(1)", "CREATE TABLE receipt(id TEXT PRIMARY KEY, value TEXT NOT NULL)", "INSERT INTO receipt VALUES('r1','before')"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(d, "backup.db")
	if _, err = db.Exec("VACUUM INTO ?", backup); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE receipt SET value='after' WHERE id='r1'"); err != nil {
		t.Fatal(err)
	}
	b, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var value string
	if err = b.QueryRow("SELECT value FROM receipt WHERE id='r1'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "before" {
		t.Fatalf("backup generation=%q", value)
	}
	var integrity string
	if err = b.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}

func TestMigrationRunnerIsOrderedAndRepeatable(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(0)"); err != nil {
		t.Fatal(err)
	}
	run := func(target int) error {
		tx, e := db.Begin()
		if e != nil {
			return e
		}
		var v int
		if e = tx.QueryRow("SELECT version FROM schema_version").Scan(&v); e != nil {
			return e
		}
		for v < target {
			v++
			if v == 1 {
				if _, e = tx.Exec("CREATE TABLE test_data(id INTEGER PRIMARY KEY)"); e != nil {
					tx.Rollback()
					return e
				}
			}
			if _, e = tx.Exec("UPDATE schema_version SET version=?", v); e != nil {
				tx.Rollback()
				return e
			}
		}
		return tx.Commit()
	}
	if err = run(1); err != nil {
		t.Fatal(err)
	}
	if err = run(1); err != nil {
		t.Fatal(err)
	}
	if err = run(0); err != nil {
		t.Fatal(err)
	}
	var v int
	_ = db.QueryRow("SELECT version FROM schema_version").Scan(&v)
	if v != 1 {
		t.Fatalf("version=%d", v)
	}
}

func TestWalReaderWriterContentionIsBounded(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "contention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=250", "CREATE TABLE t(id INTEGER PRIMARY KEY, value TEXT)"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if e := db.QueryRow("SELECT count(*) FROM t").Scan(&n); e != nil {
				errs <- e
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := db.Exec("INSERT INTO t(value) VALUES(?)", "v"); e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
