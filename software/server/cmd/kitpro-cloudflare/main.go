package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/kitpro/kitpro/software/server/internal/cloudflare"
)

func main() {
	socket := environment("KITPRO_CLOUDFLARE_SOCKET", "/run/kitpro/cloudflare.sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0750); err != nil {
		fatal(err)
	}
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		fatal(err)
	}
	store := cloudflare.CredentialStore{}
	runtime := &cloudflare.Supervisor{Store: store}
	manager := cloudflare.Manager{Store: store, API: cloudflare.NewClient(), Runtime: runtime}
	if err = manager.Recover(); err != nil {
		fatal(err)
	}
	worker := &cloudflare.Worker{Manager: manager}
	if err = worker.Serve(listener); err != nil {
		fatal(err)
	}
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "kitpro-cloudflare:", err)
	os.Exit(1)
}
