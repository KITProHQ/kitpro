package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"
)

const (
	defaultReloadRequest  = "/run/kitpro/ingress-reload.request"
	defaultReloadComplete = "/run/kitpro/ingress-reload.complete"
)

type ReloadBridge struct{}

func (b ReloadBridge) Request(ctx context.Context) error {
	return requestReload(ctx, defaultReloadRequest, defaultReloadComplete)
}

func requestReload(ctx context.Context, requestPath, completePath string) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return errors.New("create ingress reload request")
	}
	token := hex.EncodeToString(tokenBytes)
	if err := os.Remove(completePath); err != nil && !os.IsNotExist(err) {
		return errors.New("prepare ingress reload acknowledgement")
	}
	temporary := requestPath + ".new"
	if err := os.WriteFile(temporary, []byte(token+"\n"), 0600); err != nil {
		return errors.New("write ingress reload request")
	}
	if err := os.Rename(temporary, requestPath); err != nil {
		_ = os.Remove(temporary)
		return errors.New("publish ingress reload request")
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("ingress reload acknowledgement timed out")
		case <-ticker.C:
			acknowledgement, err := os.ReadFile(completePath)
			if err == nil && strings.TrimSpace(string(acknowledgement)) == token {
				return nil
			}
		}
	}
}
