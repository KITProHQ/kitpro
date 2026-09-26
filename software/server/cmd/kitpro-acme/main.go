package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	acmelifecycle "github.com/kitpro/kitpro/software/server/internal/acme"
)

func main() {
	clientSocket := environment("KITPRO_ACME_SOCKET", "/run/kitpro/acme-client.sock")
	challengeSocket := environment("KITPRO_ACME_CHALLENGE_SOCKET", "/run/kitpro-ingress/acme.sock")
	if err := os.MkdirAll(filepath.Dir(clientSocket), 0750); err != nil {
		fatal(err)
	}
	_ = os.Remove(clientSocket)
	clientListener, err := net.Listen("unix", clientSocket)
	if err != nil {
		fatal(err)
	}
	defer clientListener.Close()
	if err = os.Chmod(clientSocket, 0600); err != nil {
		fatal(err)
	}
	account, err := user.Lookup("kitpro-ingress")
	if err != nil {
		fatal(fmt.Errorf("resolve ingress service account: %w", err))
	}
	groupID, err := strconv.Atoi(account.Gid)
	if err != nil {
		fatal(fmt.Errorf("invalid ingress group identity"))
	}
	responder := acmelifecycle.NewChallengeResponder()
	challengeListener, err := responder.Listen(challengeSocket, groupID)
	if err != nil {
		fatal(err)
	}
	defer challengeListener.Close()
	worker := acmelifecycle.Worker{
		Accounts:  acmelifecycle.AccountStore{},
		Manager:   acmelifecycle.Manager{Resolver: net.DefaultResolver, Authority: acmelifecycle.XCryptoAuthority{}},
		Publisher: responder,
	}
	if err = worker.Serve(clientListener); err != nil {
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
	fmt.Fprintln(os.Stderr, "kitpro-acme:", err)
	os.Exit(1)
}
