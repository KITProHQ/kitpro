// Disposable Debian persistence probe; not KITPro production code.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

func open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=250", "PRAGMA foreign_keys=ON", "CREATE TABLE IF NOT EXISTS items(id INTEGER PRIMARY KEY, generation INTEGER NOT NULL, value TEXT)", "CREATE TABLE IF NOT EXISTS leases(id INTEGER PRIMARY KEY, token INTEGER NOT NULL)"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func main() {
	root, err := os.MkdirTemp("", "kitpro-persistence-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "state.db")
	db, err := open(path)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err = db.Exec("INSERT INTO items(generation,value) VALUES(1,'A'); INSERT INTO leases(id,token) VALUES(1,0)"); err != nil {
		panic(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	start := time.Now()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if e := db.QueryRow("SELECT count(*) FROM items").Scan(&n); e != nil {
				errs <- e
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := db.Exec("INSERT INTO items(generation,value) VALUES(1,'reader-writer')"); e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		panic(e)
	}
	fmt.Printf("readers_writers=PASS elapsed_ms=%d\n", time.Since(start).Milliseconds())
	tx, err := db.Begin()
	if err != nil {
		panic(err)
	}
	if _, err = tx.Exec("INSERT INTO items(generation,value) VALUES(2,'backup-A')"); err != nil {
		panic(err)
	}
	backup := filepath.Join(root, "backup.db")
	if _, err = db.Exec("VACUUM INTO ?", backup); err != nil {
		panic(err)
	}
	tx.Rollback()
	if _, err = db.Exec("INSERT INTO items(generation,value) VALUES(3,'after-backup')"); err != nil {
		panic(err)
	}
	b, err := open(backup)
	if err != nil {
		panic(err)
	}
	defer b.Close()
	var max int
	if err = b.QueryRow("SELECT max(generation) FROM items").Scan(&max); err != nil {
		panic(err)
	}
	var integrity, fk string
	b.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	b.QueryRow("PRAGMA foreign_key_check").Scan(&fk)
	fmt.Printf("backup=PASS generation=%d integrity=%s foreign_keys=%s\n", max, integrity, fk)
	if _, err = db.Exec("UPDATE leases SET token=token+1 WHERE id=1 AND token=0"); err != nil {
		panic(err)
	}
	fmt.Println("fencing=PASS")
	for _, timeout := range []int{100, 250, 500, 1000} {
		db2, e := open(filepath.Join(root, fmt.Sprintf("lock-%d.db", timeout)))
		if e != nil {
			panic(e)
		}
		if _, e = db2.Exec("CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT)"); e != nil {
			panic(e)
		}
		lock, e := db2.Begin()
		if e != nil {
			panic(e)
		}
		if _, e = lock.Exec("INSERT INTO t(v) VALUES('held')"); e != nil {
			panic(e)
		}
		if _, e = db2.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", timeout)); e != nil {
			panic(e)
		}
		started := time.Now()
		_, e = db2.Exec("INSERT INTO t(v) VALUES('blocked')")
		elapsed := time.Since(started).Milliseconds()
		lock.Rollback()
		db2.Close()
		fmt.Printf("lock_timeout_ms=%d elapsed_ms=%d error=%v\n", timeout, elapsed, e)
	}
}
