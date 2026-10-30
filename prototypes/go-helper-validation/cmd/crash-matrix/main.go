// Disposable process-kill SQLite probe; not KITPro production code.
package main

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func dbopen(p string) *sql.DB {
	d, e := sql.Open("sqlite", p)
	if e != nil {
		panic(e)
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=250", "CREATE TABLE IF NOT EXISTS rows(id INTEGER PRIMARY KEY,value TEXT)"} {
		if _, e = d.Exec(q); e != nil {
			panic(e)
		}
	}
	return d
}
func main() {
	if os.Getenv("CHILD") == "1" {
		child()
		return
	}
	root, _ := os.MkdirTemp("", "crash-")
	defer os.RemoveAll(root)
	for i := 0; i < 10; i++ {
		for _, u := range []bool{false, true} {
			p := filepath.Join(root, fmt.Sprintf("%d-%t.db", i, u))
			r, w, _ := os.Pipe()
			c := exec.Command(os.Args[0])
			c.Env = append(os.Environ(), "CHILD=1", "DB="+p)
			if u {
				c.Env = append(c.Env, "UNCOMMITTED=1")
			}
			c.ExtraFiles = []*os.File{w}
			if e := c.Start(); e != nil {
				panic(e)
			}
			w.Close()
			buf := make([]byte, 1)
			if _, e := r.Read(buf); e != nil {
				panic(e)
			}
			c.Process.Kill()
			c.Wait()
			d := dbopen(p)
			var n int
			d.QueryRow("SELECT count(*) FROM rows WHERE value=?", map[bool]string{true: "uncommitted", false: "committed"}[u]).Scan(&n)
			var ic string
			d.QueryRow("PRAGMA integrity_check").Scan(&ic)
			if (u && n != 0) || (!u && n != 1) || ic != "ok" {
				panic(fmt.Sprintf("case=%t rows=%d integrity=%s", u, n, ic))
			}
			d.Close()
		}
	}
	fmt.Println("crash_cases=20/20 PASS")
}
func child() {
	d := dbopen(os.Getenv("DB"))
	defer d.Close()
	tx, _ := d.Begin()
	tx.Exec("INSERT INTO rows(value) VALUES(?)", map[bool]string{true: "uncommitted", false: "committed"}[os.Getenv("UNCOMMITTED") == "1"])
	if os.Getenv("UNCOMMITTED") != "1" {
		tx.Commit()
	}
	f := os.NewFile(3, "signal")
	f.Write([]byte{1})
	select {}
}

var _ = syscall.SIGKILL
