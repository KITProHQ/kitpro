package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/auth"
	"github.com/kitpro/kitpro/software/server/internal/hostadmin"
	"github.com/kitpro/kitpro/software/server/internal/state"
)

type apiStorageRunner struct{}

func (apiStorageRunner) Run(_ context.Context, name string) ([]byte, error) {
	switch name {
	case "lsblk":
		return []byte(`{"blockdevices":[]}`), nil
	case "lvm", "zpool":
		return nil, hostadmin.ErrUnsupported
	default:
		return nil, hostadmin.ErrUnsupported
	}
}

func apiStorageProvider() hostadmin.StorageProvider {
	return hostadmin.StorageProvider{Runner: apiStorageRunner{}, ReadMountInfo: func() ([]byte, error) { return []byte("1 0 0:1 / / rw - tmpfs tmpfs rw\n"), nil }, ReadSwaps: func() ([]byte, error) { return nil, nil }, StatFS: func(string) (hostadmin.StorageStat, error) {
		return hostadmin.StorageStat{TotalBytes: 100, FreeBytes: 20, AvailableBytes: 10}, nil
	}}
}

func TestStorageRequiresSessionAndReturnsBoundedInventory(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = auth.Init(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = auth.Setup(context.Background(), db, "admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.NewSession(context.Background(), db, 1)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, authCfg: auth.DefaultConfig, allowedHosts: map[string]bool{"localhost:8080": true}, hostStorageProvider: apiStorageProvider()}
	route := a.guard(a.hostStorage)
	request := func() *http.Request {
		r := httptest.NewRequest("GET", "http://localhost:8080/api/v1/host/storage", nil)
		r.Host = "localhost:8080"
		return r
	}
	unauthorized := httptest.NewRecorder()
	route(unauthorized, request())
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized: %d", unauthorized.Code)
	}
	authorizedRequest := request()
	authorizedRequest.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	authorized := httptest.NewRecorder()
	route(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || !strings.Contains(authorized.Body.String(), `"capacity_scope"`) {
		t.Fatalf("authorized: %d %s", authorized.Code, authorized.Body.String())
	}
	if authorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache policy: %q", authorized.Header().Get("Cache-Control"))
	}
}

func TestStorageRejectsMethodsAndQueryParameters(t *testing.T) {
	a := &app{hostStorageProvider: apiStorageProvider()}
	for _, request := range []*http.Request{httptest.NewRequest("POST", "/api/v1/host/storage", nil), httptest.NewRequest("GET", "/api/v1/host/storage?path=/etc", nil), httptest.NewRequest("GET", "/api/v1/host/storage?x=1&x=2", nil)} {
		response := httptest.NewRecorder()
		a.hostStorage(response, request)
		if response.Code < 400 {
			t.Fatalf("accepted %s: %d", request.URL.String(), response.Code)
		}
	}
}
