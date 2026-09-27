package ingress

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReloadBridgeRequiresMatchingAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	request := filepath.Join(directory, "ingress-reload-test.request")
	complete := filepath.Join(directory, "ingress-reload-test.complete")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			value, err := os.ReadFile(request)
			if err == nil {
				_ = os.WriteFile(complete, value, 0600)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := requestReload(ctx, request, complete); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestReloadBridgeHonorsCallerDeadline(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := requestReload(ctx, filepath.Join(directory, "request"), filepath.Join(directory, "complete"))
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("bounded reload err=%v elapsed=%s", err, time.Since(started))
	}
}
