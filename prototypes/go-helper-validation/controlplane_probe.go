// Disposable unprivileged control-plane-shaped probe; not production code.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"os"
	"time"
)

//go:embed probe.html probe.css probe.js
var probeAssets embed.FS

func controlPlaneServer() *http.Server {
	tmpl := template.Must(template.ParseFS(probeAssets, "probe.html"))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = tmpl.Execute(w, map[string]string{"Title": "KITPro probe"})
	})
	mux.HandleFunc("/api/v1/host", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "operation": "observation"})
	})
	mux.HandleFunc("/api/v1/operations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"operation_id": "probe-1", "status": "accepted"})
	})
	return &http.Server{Addr: "127.0.0.1:0", Handler: mux, ReadHeaderTimeout: 3 * time.Second}
}

func runControlPlaneProbe(ctx context.Context) error {
	s := controlPlaneServer()
	ln, e := netListen("tcp", s.Addr)
	if e != nil {
		return e
	}
	go s.Serve(ln)
	<-ctx.Done()
	return s.Shutdown(context.Background())
}

var netListen = func(network, address string) (net.Listener, error) { return net.Listen(network, address) }
var _ = os.ErrNotExist
