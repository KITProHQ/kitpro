// Disposable probes only; this is not production KITPro code.
package goruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

func canonical(v any) ([]byte, error) { return json.Marshal(v) }

func TestCanonicalHash(t *testing.T) {
	a := map[string]any{"operation": "Ping", "params": map[string]any{"x": "y"}}
	b := map[string]any{"params": map[string]any{"x": "y"}, "operation": "Ping"}
	x, _ := canonical(a)
	y, _ := canonical(b)
	// Typed structs, not maps, are the production recommendation; map ordering is
	// deliberately normalized here by encoding/json in current Go.
	if fmt.Sprint(string(x)) != fmt.Sprint(string(y)) {
		t.Fatalf("canonical mismatch: %s %s", x, y)
	}
	h := sha256.Sum256(x)
	if h == ([32]byte{}) {
		t.Fatal("empty hash")
	}
}

func TestFramingAndPeerCredentials(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "helper.sock")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		uc, ok := c.(*net.UnixConn)
		if !ok {
			done <- fmt.Errorf("not unix")
			return
		}
		raw, e := uc.SyscallConn()
		if e != nil {
			done <- e
			return
		}
		var cred *unix.Ucred
		raw.Control(func(fd uintptr) { cred, _ = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
		if cred == nil || int(cred.Uid) != os.Getuid() {
			done <- fmt.Errorf("bad credentials")
		} else {
			done <- nil
		}
	}()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOpenat2Containment(t *testing.T) {
	root := t.TempDir()
	fd, err := unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	res := &unix.OpenHow{Flags: unix.O_RDONLY, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS}
	if _, err = unix.Openat2(fd, "../escape", res); err == nil {
		t.Fatal("traversal accepted")
	}
}

func TestDockerEngineUnixSocket(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("Docker socket unavailable; run on disposable validation VM")
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	c := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	resp, err := c.Get("http://docker/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("docker status %s", resp.Status)
	}
}

func TestSQLiteWALTransactionsAndForeignKeys(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "CREATE TABLE parent(id INTEGER PRIMARY KEY)", "CREATE TABLE child(id INTEGER PRIMARY KEY, parent INTEGER REFERENCES parent(id))", "INSERT INTO parent VALUES(1)"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("INSERT INTO child VALUES(1,999)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
	tx, _ := db.Begin()
	if _, err = tx.Exec("INSERT INTO child VALUES(2,1)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode=%q err=%v", mode, err)
	}
	var integrity string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}

func TestAtomicReceipt(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "receipt")
	tmp, err := os.CreateTemp(d, ".receipt.*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tmp.WriteString(`{"state":"succeeded"}`); err != nil {
		t.Fatal(err)
	}
	if err = tmp.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		t.Fatal(err)
	}
	dir, _ := os.Open(d)
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestGracefulShutdown(t *testing.T) {
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	go s.Serve(l)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

var _ = binary.BigEndian
var _ = syscall.SIGTERM
var _ = unsafe.Sizeof(uintptr(0))
