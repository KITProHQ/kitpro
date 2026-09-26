package ingress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxRuntimeUsesOnlyFixedCommandsAndAtomicGeneration(t *testing.T) {
	root := t.TempDir()
	var calls [][]string
	runtime := NginxRuntime{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}}
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
	if len(calls) != 3 {
		t.Fatalf("calls = %#v", calls)
	}
	if got := strings.Join(calls[0], " "); got != "/usr/sbin/nginx -t -p "+filepath.Join(root, "generations/1")+"/ -c nginx.conf" {
		t.Fatalf("unexpected validation command %q", got)
	}
	if got := strings.Join(calls[1], " "); got != "/usr/bin/systemctl reload-or-restart kitpro-ingress.service" {
		t.Fatalf("unexpected reload command %q", got)
	}
	current, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil || current != filepath.Join(root, "generations/1") {
		t.Fatalf("current=%q err=%v", current, err)
	}
}

func TestNginxRuntimeRejectsOverwriteAndRollsBackLink(t *testing.T) {
	root := t.TempDir()
	failed := false
	runtime := NginxRuntime{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/bin/systemctl" && failed {
			return []byte("synthetic failure"), errors.New("exit")
		}
		return nil, nil
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
