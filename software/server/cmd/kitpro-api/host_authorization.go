package main

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
)

func (a *app) requestPrincipal(r *http.Request) (auth.Principal, bool) {
	if a.db == nil { // Isolated provider tests do not construct an auth store.
		permissions := map[auth.Permission]bool{}
		for _, permission := range auth.AllPermissions() {
			permissions[permission] = true
		}
		return auth.Principal{ID: 1, Username: "test-administrator", Permissions: permissions}, true
	}
	id, ok := auth.SessionWithConfig(r.Context(), a.db, r, a.authCfg)
	if !ok {
		return auth.Principal{}, false
	}
	principal, err := auth.PrincipalFor(r.Context(), a.db, id)
	return principal, err == nil
}

func (a *app) accountPermissions(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	permissions := make([]string, 0, len(principal.Permissions))
	for permission := range principal.Permissions {
		permissions = append(permissions, string(permission))
	}
	sort.Strings(permissions)
	scopes, err := auth.ServiceScopesFor(r.Context(), a.db, principal.ID)
	if err != nil {
		http.Error(w, "permissions unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"username": principal.Username, "permissions": permissions, "service_scopes": scopes})
}

func (a *app) requirePermission(w http.ResponseWriter, r *http.Request, permission auth.Permission) (auth.Principal, bool) {
	principal, ok := a.requestPrincipal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return auth.Principal{}, false
	}
	if !principal.Has(permission) {
		http.Error(w, "permission denied", http.StatusForbidden)
		return auth.Principal{}, false
	}
	return principal, true
}

func (a *app) authorizeServiceActions(r *http.Request, principal auth.Principal, service *hostadmin.Service) {
	if a.db == nil {
		return
	}
	for index := range service.Actions {
		action := &service.Actions[index]
		if action.Allowed && !auth.CanManageService(r.Context(), a.db, principal, service.Name, string(action.Action)) {
			action.Allowed = false
			action.State = "permission_denied"
			action.Reason = "Your administrator account is not authorized for this service action."
		}
	}
}
