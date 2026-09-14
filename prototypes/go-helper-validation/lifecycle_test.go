package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func runHelperOnce(t *testing.T, socket, dbPath, root string, requestID string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=250", "CREATE TABLE IF NOT EXISTS receipts(id TEXT PRIMARY KEY,value TEXT NOT NULL)"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	h := &Helper{socket: socket, state: db, allowedUID: uint32(os.Getuid()), root: root, docker: &http.Client{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.serve(ctx) }()
	for i := 0; i < 30; i++ {
		if c, e := net.Dial("unix", socket); e == nil {
			b, _ := json.Marshal(Request{Version: 1, ID: requestID, Operation: "Ping", Params: map[string]string{}})
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
			c.Write(hdr[:])
			c.Write(b)
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("helper did not stop")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHelperRestartAgainstExistingWalDatabase(t *testing.T) {
	d := t.TempDir()
	socket := filepath.Join(d, "helper.sock")
	dbPath := filepath.Join(d, "helper.db")
	root := filepath.Join(d, "storage")
	runHelperOnce(t, socket, dbPath, root, "first")
	runHelperOnce(t, socket, dbPath, root, "second")
}
