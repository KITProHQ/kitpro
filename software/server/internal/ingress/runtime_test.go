package ingress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestNginxRuntimeUsesOnlyFixedCommandsAndAtomicGeneration(t *testing.T) {
	root := t.TempDir()
	var calls [][]string
	if err := os.MkdirAll(filepath.Join(root, "run"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "nginx.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	var signals []syscall.Signal
	runtime := NginxRuntime{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}, Signal: func(_ int, signal syscall.Signal) error { signals = append(signals, signal); return nil }}
	candidate := Candidate{Generation: 1, Configuration: []byte("events {}\nhttp {}\n")}
	if err := runtime.Prepare(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Validate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Activate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Verify(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v", calls)
	}
	if got := strings.Join(calls[0], " "); got != "/usr/sbin/nginx -t -p "+filepath.Join(root, "generations/1")+"/ -c nginx.conf" {
		t.Fatalf("unexpected validation command %q", got)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGHUP || signals[1] != 0 {
		t.Fatalf("unexpected signals %#v", signals)
	}
	current, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil || current != filepath.Join(root, "generations/1") {
		t.Fatalf("current=%q err=%v", current, err)
	}
}

func TestNginxRuntimeRejectsOverwriteAndRollsBackLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bootstrap"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "nginx.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	failed := false
	runtime := NginxRuntime{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		return nil, nil
	}, Signal: func(_ int, _ syscall.Signal) error {
		if failed {
			return errors.New("synthetic failure")
		}
		return nil
	}}
	first := Candidate{Generation: 1, Configuration: []byte("one")}
	if err := runtime.Prepare(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Prepare(context.Background(), first); err == nil {
		t.Fatal("existing immutable generation was overwritten")
	}
	if err := runtime.Activate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := Candidate{Generation: 2, PreviousGeneration: 1, Configuration: []byte("two")}
	if err := runtime.Prepare(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	failed = true
	if err := runtime.Activate(context.Background(), second); err == nil {
		t.Fatal("expected reload failure")
	}
	failed = false
	if err := runtime.Rollback(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	current, _ := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if current != filepath.Join(root, "generations/1") {
		t.Fatalf("rollback current=%q", current)
	}
	if _, err := os.Stat(filepath.Join(root, "generations/2/nginx.conf")); err != nil {
		t.Fatal(err)
	}
}

func TestNginxRuntimeReloadsAndVerifiesCurrentCertificateConfiguration(t *testing.T) {
	root := t.TempDir()
	generation := filepath.Join(root, "generations", "1")
	if err := os.MkdirAll(filepath.Join(root, "run"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(generation, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, "nginx.conf"), []byte("events {}"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(generation, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "nginx.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	var calls int
	var signals []syscall.Signal
	runtime := NginxRuntime{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		return nil, nil
	}, Signal: func(_ int, signal syscall.Signal) error { signals = append(signals, signal); return nil }}
	if err := runtime.ReloadAndVerifyCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(signals) != 2 || signals[0] != syscall.SIGHUP || signals[1] != 0 {
		t.Fatalf("calls=%d signals=%v", calls, signals)
	}
}
