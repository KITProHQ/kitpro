package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/kitpro/kitpro/software/server/internal/ingress"
)

func main() {
	socket := "/run/kitpro/ingress-validator.sock"
	if value := os.Getenv("KITPRO_INGRESS_VALIDATOR_SOCKET"); value != "" {
		socket = value
	}
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
	if err = (ingress.ValidatorWorker{}).Serve(listener); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "kitpro-ingress-validator:", err)
	os.Exit(1)
}
