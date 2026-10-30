package main

import (
	"encoding/json"
	"net/http"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/buildinfo"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

func (a *app) hostOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		hostadmin.Overview
		ServerVersion string `json:"server_version"`
	}{hostadmin.Collect(), buildinfo.Version})
}
