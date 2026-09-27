package ingress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNginxRuntimeUsesOnlyFixedCommandsAndAtomicGeneration(t *testing.T) {
	root := t.TempDir()
	var calls []struct {
		target     string
		generation int64
	}
	if err := os.MkdirAll(filepath.Join(root, "run"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run", "nginx.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	var reloads, processChecks int
	runtime := NginxRuntime{Root: root, Check: func(_ context.Context, target string, generation int64) error {
		calls = append(calls, struct {
			target     string
			generation int64
		}{target, generation})
		return nil
	}, Reload: func(context.Context) error { reloads++; return nil }, VerifyProcess: func() error { processChecks++; return nil }}
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
	if calls[0].target != "generation" || calls[0].generation != 1 || calls[1] != calls[0] {
		t.Fatalf("unexpected validation calls %#v", calls)
	}
	if reloads != 1 || processChecks != 1 {
		t.Fatalf("reloads=%d process_checks=%d", reloads, processChecks)
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
	runtime := NginxRuntime{Root: root, Check: func(_ context.Context, _ string, _ int64) error {
		return nil
	}, Reload: func(context.Context) error {
		if failed {
			return errors.New("synthetic failure")
		}
		return nil
	}, VerifyProcess: func() error { return nil }}
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
	var reloads, processChecks int
	runtime := NginxRuntime{Root: root, Check: func(_ context.Context, target string, generation int64) error {
		calls++
		if target != "current" || generation != 0 {
			t.Fatalf("unexpected validation target %q generation %d", target, generation)
		}
		return nil
	}, Reload: func(context.Context) error { reloads++; return nil }, VerifyProcess: func() error { processChecks++; return nil }}
	if err := runtime.ReloadAndVerifyCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || reloads != 1 || processChecks != 1 {
		t.Fatalf("calls=%d reloads=%d process_checks=%d", calls, reloads, processChecks)
	}
}

func TestVerifyManagedNginxReadsConfiguredPIDPath(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "nginx.pid")
	if err := os.WriteFile(pidPath, []byte("not-a-pid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedNginx(pidPath); err == nil || err.Error() != "invalid managed nginx PID" {
		t.Fatalf("unexpected result: %v", err)
	}
}
