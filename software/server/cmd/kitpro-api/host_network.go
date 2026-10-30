package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

func writeNetworkJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func writeNetworkStatusJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *app) collectNetwork(w http.ResponseWriter, r *http.Request) (hostadmin.NetworkInventory, bool) {
	ctx, cancel := hostContext(r)
	defer cancel()
	inventory, err := a.hostNetworkProvider.Collect(ctx)
	if err != nil {
		hostError(w, err)
		return hostadmin.NetworkInventory{}, false
	}
	return inventory, true
}

func (a *app) hostNetwork(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	inventory, ok := a.collectNetwork(w, r)
	if !ok {
		return
	}
	writeNetworkJSON(w, inventory)
}

func (a *app) hostNetworkSubresource(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/host/network/"), "/")
	if a.hostNetworkConfigurationRoute(w, r, path) {
		return
	}
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if !readOnly(w, r) {
		return
	}
	if !validQuery(r) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	inventory, ok := a.collectNetwork(w, r)
	if !ok {
		return
	}
	switch path {
	case "interfaces":
		writeNetworkJSON(w, struct {
			Items []hostadmin.NetworkInterface `json:"items"`
		}{inventory.Interfaces})
	case "routes":
		writeNetworkJSON(w, struct {
			Items      []hostadmin.NetworkRoute `json:"items"`
			Capability hostadmin.Status         `json:"capability"`
		}{inventory.Routes, inventory.Capabilities["policy_routing"]})
	case "dns":
		writeNetworkJSON(w, inventory.DNS)
	case "stats":
		type item struct {
			Name  string                 `json:"name"`
			Stats hostadmin.NetworkStats `json:"stats"`
		}
		items := make([]item, 0, len(inventory.Interfaces))
		for _, iface := range inventory.Interfaces {
			items = append(items, item{iface.Name, iface.Stats})
		}
		writeNetworkJSON(w, struct {
			Items []item `json:"items"`
			Scope string `json:"scope"`
		}{items, inventory.Summary.TrafficScope})
	default:
		if !strings.HasPrefix(path, "interfaces/") {
			http.NotFound(w, r)
			return
		}
		encoded := strings.TrimPrefix(path, "interfaces/")
		name, err := url.PathUnescape(encoded)
		if err != nil || !hostadmin.ValidInterfaceName(name) || strings.Contains(encoded, "/") {
			hostError(w, hostadmin.ErrInvalid)
			return
		}
		for _, iface := range inventory.Interfaces {
			if iface.Name == name {
				routes := []hostadmin.NetworkRoute{}
				for _, route := range inventory.Routes {
					if route.Interface == name {
						routes = append(routes, route)
					}
				}
				writeNetworkJSON(w, struct {
					Interface hostadmin.NetworkInterface `json:"interface"`
					Routes    []hostadmin.NetworkRoute   `json:"routes"`
					DNS       hostadmin.DNSConfiguration `json:"dns"`
				}{iface, routes, inventory.DNS})
				return
			}
		}
		http.NotFound(w, r)
	}
}

func (a *app) hostNetworkDiagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requirePermission(w, r, auth.PermissionHostRead); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.RawQuery != "" {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if a.hostDiagnosticProvider == nil {
		hostError(w, hostadmin.ErrUnavailable)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 4096)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var request hostadmin.DiagnosticRequest
	if err := decoder.Decode(&request); err != nil {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		hostError(w, hostadmin.ErrInvalid)
		return
	}
	ctx, cancel := hostContext(r)
	defer cancel()
	result, err := a.hostDiagnosticProvider.Run(ctx, request)
	if err != nil {
		log.Printf("host network diagnostic operation=%s outcome=rejected", request.Operation)
		hostError(w, err)
		return
	}
	log.Printf("host network diagnostic operation=%s outcome=%s", result.Operation, result.Category)
	writeNetworkJSON(w, result)
}
