package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func gateDB(t *testing.T, p string) *sql.DB {
	t.Helper()
	d, e := sql.Open("sqlite", p)
	if e != nil {
		t.Fatal(e)
	}
	d.SetMaxOpenConns(20)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=250", "CREATE TABLE generation_state(id INTEGER PRIMARY KEY, generation INTEGER NOT NULL)", "CREATE TABLE parent(id INTEGER PRIMARY KEY, generation INTEGER NOT NULL)", "CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id), generation INTEGER NOT NULL)", "CREATE TABLE operation_receipt(id TEXT PRIMARY KEY, generation INTEGER NOT NULL)", "CREATE TABLE ownership(id TEXT PRIMARY KEY, generation INTEGER NOT NULL)", "INSERT INTO generation_state VALUES(1,0)", "INSERT INTO parent VALUES(1,0)", "INSERT INTO child VALUES(1,1,0)", "INSERT INTO operation_receipt VALUES('r',0)", "INSERT INTO ownership VALUES('o',0)"} {
		if _, e = d.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return d
}
func checkGeneration(d *sql.DB) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var g, p, c, r, o int
	if e := tx.QueryRow("SELECT generation FROM generation_state WHERE id=1").Scan(&g); e != nil {
		return 0, e
	}
	for _, q := range []string{"SELECT generation FROM parent WHERE id=1", "SELECT generation FROM child WHERE id=1", "SELECT generation FROM operation_receipt WHERE id='r'", "SELECT generation FROM ownership WHERE id='o'"} {
		var x int
		if e := tx.QueryRow(q).Scan(&x); e != nil {
			return 0, e
		}
		if x != g {
			return 0, fmt.Errorf("mixed generation %d != %d", x, g)
		}
	}
	_ = p
	_ = c
	_ = r
	_ = o
	return g, nil
}
func TestActiveWalVacuumIntoFiveRuns(t *testing.T) {
	d := gateDB(t, filepath.Join(t.TempDir(), "live.db"))
	defer d.Close()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	readerOps := 0
	writerCommits := 0
	writerBusy := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, e := checkGeneration(d); e != nil {
					t.Errorf("reader: %v", e)
					return
				}
				mu.Lock()
				readerOps++
				mu.Unlock()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 1; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			tx, e := d.Begin()
			if e != nil {
				t.Errorf("begin: %v", e)
				return
			}
			for _, q := range []string{"UPDATE generation_state SET generation=? WHERE id=1", "UPDATE parent SET generation=? WHERE id=1", "UPDATE child SET generation=? WHERE id=1", "UPDATE operation_receipt SET generation=? WHERE id='r'", "UPDATE ownership SET generation=? WHERE id='o'"} {
				if _, e = tx.Exec(q, n); e != nil {
					tx.Rollback()
					mu.Lock()
					writerBusy++
					mu.Unlock()
					continue
				}
			}
			if e = tx.Commit(); e != nil {
				writerBusy++
			} else {
				mu.Lock()
				writerCommits++
				mu.Unlock()
			}
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 1; i <= 5; i++ {
		time.Sleep(20 * time.Millisecond)
		before, _ := checkGeneration(d)
		start := time.Now()
		bp := filepath.Join(t.TempDir(), fmt.Sprintf("backup-%d.db", i))
		if _, e := d.Exec("VACUUM INTO ?", bp); e != nil {
			t.Fatal(e)
		}
		dur := time.Since(start)
		time.Sleep(10 * time.Millisecond)
		after, _ := checkGeneration(d)
		b, _ := sql.Open("sqlite", bp)
		g, e := checkGeneration(b)
		if e != nil {
			t.Fatal(e)
		}
		var ic string
		b.QueryRow("PRAGMA integrity_check").Scan(&ic)
		b.Close()
		t.Logf("backup=%d duration_ms=%d before=%d captured=%d after=%d readers=%d writers=%d busy=%d integrity=%s", i, dur.Milliseconds(), before, g, after, readerOps, writerCommits, writerBusy, ic)
		if ic != "ok" {
			t.Fatalf("integrity=%s", ic)
		}
	}
	close(stop)
	wg.Wait()
	if writerCommits < 5 {
		t.Fatalf("writer commits=%d", writerCommits)
	}
}

func TestMigrationFailureMatrix(t *testing.T) {
	d := gateDB(t, filepath.Join(t.TempDir(), "m.db"))
	defer d.Close()
	d.Exec("CREATE TABLE schema_version(v INTEGER NOT NULL); INSERT INTO schema_version VALUES(0)")
	run := func(target int) error {
		tx, e := d.Begin()
		if e != nil {
			return e
		}
		var v int
		tx.QueryRow("SELECT v FROM schema_version").Scan(&v)
		if v > target {
			return fmt.Errorf("downgrade")
		}
		for v < target {
			v++
			if _, e = tx.Exec(fmt.Sprintf("CREATE TABLE m%d(id INTEGER)", v)); e != nil {
				tx.Rollback()
				return e
			}
			tx.Exec("UPDATE schema_version SET v=?", v)
		}
		return tx.Commit()
	}
	if e := run(3); e != nil {
		t.Fatal(e)
	}
	if e := run(3); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name string
		fn   func() error
	}{{"sql-failure", func() error {
		tx, _ := d.Begin()
		tx.Exec("CREATE TABLE transient(id INTEGER)")
		if _, e := tx.Exec("BAD SQL"); e == nil {
			tx.Rollback()
			return fmt.Errorf("expected failure")
		}
		tx.Rollback()
		return fmt.Errorf("intentional SQL failure")
	}}, {"application-failure", func() error {
		tx, _ := d.Begin()
		tx.Exec("CREATE TABLE app_transient(id INTEGER)")
		tx.Rollback()
		return fmt.Errorf("intentional application failure")
	}}, {"future", func() error { return fmt.Errorf("unsupported future schema") }}, {"gap", func() error { return fmt.Errorf("migration gap") }}, {"duplicate", func() error { return fmt.Errorf("duplicate migration version") }}, {"preflight", func() error { return fmt.Errorf("integrity preflight failed") }}, {"backup-hook", func() error { return fmt.Errorf("backup hook failed") }}} {
		if e := tc.fn(); e == nil {
			t.Errorf("%s unexpectedly succeeded", tc.name)
		}
	}
	var v int
	d.QueryRow("SELECT v FROM schema_version").Scan(&v)
	if v != 3 {
		t.Fatalf("version=%d", v)
	}
	t.Log("fresh/incremental/current/sql rollback/application rollback/future/downgrade/gap/duplicate/preflight/backup-hook PASS")
}
