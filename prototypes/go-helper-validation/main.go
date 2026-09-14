// Disposable production-shaped helper probe. Not KITPro production code.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const maxMessage = 64 * 1024

type Request struct {
	Version   int               `json:"version"`
	ID        string            `json:"request_id"`
	Operation string            `json:"operation"`
	Params    map[string]string `json:"params"`
}
type Response struct {
	OK        bool   `json:"ok"`
	RequestID string `json:"request_id,omitempty"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

func canonical(r Request) ([]byte, error) {
	type typed struct {
		Version   int               `json:"version"`
		ID        string            `json:"request_id"`
		Operation string            `json:"operation"`
		Params    map[string]string `json:"params"`
	}
	return json.Marshal(typed{r.Version, r.ID, r.Operation, r.Params})
}
func hashRequest(r Request) (string, error) {
	b, e := canonical(r)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func readFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 || n > maxMessage {
		return nil, errors.New("invalid frame length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}
func writeFrame(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > maxMessage {
		return errors.New("response too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, e = w.Write(h[:]); e != nil {
		return e
	}
	_, e = w.Write(b)
	return e
}

type Helper struct {
	socket     string
	state      *sql.DB
	allowedUID uint32
	root       string
	docker     *http.Client
	clients    sync.WaitGroup
}

func (h *Helper) dockerGet(path string) (map[string]any, error) {
	req, _ := http.NewRequest(http.MethodGet, "http://docker"+path, nil)
	resp, e := h.docker.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker status %s", resp.Status)
	}
	var out map[string]any
	e = json.NewDecoder(io.LimitReader(resp.Body, maxMessage)).Decode(&out)
	return out, e
}
func (h *Helper) handle(r Request) Response {
	out := Response{OK: true, RequestID: r.ID}
	sum, e := hashRequest(r)
	if e != nil {
		return Response{RequestID: r.ID, Error: "invalid request"}
	}
	var state string
	_ = h.state.QueryRow("SELECT value FROM receipts WHERE id=?", r.ID).Scan(&state)
	if state == "succeeded:"+sum {
		return out
	}
	if state != "" && state != "succeeded:"+sum {
		return Response{RequestID: r.ID, Error: "operation id conflict"}
	}
	if r.Version != 1 {
		return Response{RequestID: r.ID, Error: "unsupported protocol"}
	}
	switch r.Operation {
	case "Ping":
		out.Result = map[string]any{"protocol": 1, "request_hash": sum}
	case "InspectDocker":
		out.Result, e = h.dockerGet("/version")
	case "PrepareTestDirectory":
		slot := r.Params["slot"]
		if slot == "" || strings.Contains(slot, "/") || filepath.IsAbs(slot) {
			e = errors.New("invalid slot")
		} else {
			p := filepath.Join(h.root, slot)
			e = os.MkdirAll(p, 0700)
			out.Result = map[string]any{"path": p}
		}
	case "Openat2Validation":
		fd, openErr := unix.Open(h.root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if openErr != nil {
			e = openErr
			break
		}
		defer unix.Close(fd)
		_, probeErr := unix.Openat2(fd, "../escape", &unix.OpenHow{
			Flags:   unix.O_PATH | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS,
		})
		if probeErr == nil {
			e = errors.New("openat2 escape unexpectedly allowed")
		} else {
			out.Result = map[string]any{"rejected": true, "error": probeErr.Error()}
		}
	case "InspectTestResource":
		out.Result = map[string]any{"resource": r.Params["resource"]}
	case "NegativeProbe":
		out.Result = negativeProbe()
	case "ProbeShellExecution":
		err := exec.Command("/bin/sh", "-c", "exit 0").Run()
		if err == nil {
			e = errors.New("shell execution unexpectedly allowed")
		} else {
			out.Result = map[string]any{"denied": true, "error": err.Error()}
		}
	case "ProbeExecutableExecution":
		err := exec.Command("/bin/echo", "kitpro-validation").Run()
		if err == nil {
			e = errors.New("executable execution unexpectedly allowed")
		} else {
			out.Result = map[string]any{"denied": true, "error": err.Error()}
		}
	default:
		e = errors.New("unknown operation")
	}
	if e != nil {
		return Response{RequestID: r.ID, Error: e.Error()}
	}
	_, _ = h.state.Exec("INSERT OR REPLACE INTO receipts(id,value) VALUES(?,?)", r.ID, "succeeded:"+sum)
	return out
}

func negativeProbe() map[string]string {
	result := map[string]string{}
	for name, path := range map[string]string{"home": "/home/josh/.profile", "ssh": "/home/josh/.ssh/id_ed25519", "root": "/root/.bashrc", "etc": "/etc/kitpro-probe-denied", "srv": "/srv/other/marker"} {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err == nil {
			f.Close()
			result[name] = "unexpectedly allowed"
		} else {
			result[name] = err.Error()
		}
	}
	for name, address := range map[string]string{"inet": "127.0.0.1:0", "inet6": "[::1]:0"} {
		l, err := net.Listen("tcp", address)
		if err == nil {
			l.Close()
			result[name] = "unexpectedly allowed"
		} else {
			result[name] = err.Error()
		}
	}
	return result
}

func (h *Helper) serve(ctx context.Context) error {
	var l net.Listener
	var e error
	if os.Getenv("LISTEN_FDS") == "1" {
		l, e = net.FileListener(os.NewFile(3, "kitpro-helper.sock"))
	} else {
		_ = os.Remove(h.socket)
		if err := os.MkdirAll(filepath.Dir(h.socket), 0750); err != nil {
			return err
		}
		l, e = net.Listen("unix", h.socket)
	}
	if e != nil {
		return e
	}
	defer l.Close()
	defer h.clients.Wait()
	if os.Getenv("LISTEN_FDS") != "1" {
		_ = os.Chmod(h.socket, 0660)
	}
	for {
		l.(*net.UnixListener).SetDeadline(time.Now().Add(250 * time.Millisecond))
		c, e := l.Accept()
		if ne, ok := e.(net.Error); ok && ne.Timeout() {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		if e != nil {
			return e
		}
		h.clients.Add(1)
		go h.client(c)
	}
}
func (h *Helper) client(c net.Conn) {
	defer h.clients.Done()
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	raw, _ := uc.SyscallConn()
	var cred *unix.Ucred
	_ = raw.Control(func(fd uintptr) { cred, _ = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if cred == nil || cred.Uid != h.allowedUID {
		return
	}
	b, e := readFrame(c)
	if e != nil {
		return
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var r Request
	if e = dec.Decode(&r); e != nil {
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return
	}
	_ = writeFrame(c, h.handle(r))
}

func main() {
	socket := os.Getenv("KITPRO_TEST_SOCKET")
	if socket == "" {
		socket = "/tmp/kitpro-go-helper/helper.sock"
	}
	statePath := os.Getenv("KITPRO_TEST_DB")
	if statePath == "" {
		statePath = "/tmp/kitpro-go-helper/helper.db"
	}
	root := os.Getenv("KITPRO_TEST_ROOT")
	if root == "" {
		root = "/tmp/kitpro-go-helper/storage"
	}
	_ = os.MkdirAll(filepath.Dir(statePath), 0700)
	db, e := sql.Open("sqlite", statePath)
	if e != nil {
		panic(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if e = os.Chmod(statePath, 0600); e != nil && !os.IsNotExist(e) {
		panic(e)
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=250", "PRAGMA foreign_keys=ON", "CREATE TABLE IF NOT EXISTS receipts(id TEXT PRIMARY KEY,value TEXT NOT NULL)"} {
		if _, e = db.Exec(q); e != nil {
			panic(e)
		}
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	allowedUID := uint32(os.Getuid())
	if raw := os.Getenv("KITPRO_TEST_ALLOWED_UID"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			panic(err)
		}
		allowedUID = uint32(parsed)
	}
	h := &Helper{socket: socket, state: db, allowedUID: allowedUID, root: root, docker: &http.Client{Transport: tr, Timeout: 3 * time.Second}}
	ctx, cancel := signalContext()
	defer cancel()
	if e = h.serve(ctx); e != nil {
		panic(e)
	}
}
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signalNotify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-ch; cancel() }()
	return ctx, cancel
}

var signalNotify = func(c chan<- os.Signal, s ...os.Signal) { signal.Notify(c, s...) }
