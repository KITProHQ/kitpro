package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostOverviewReadOnly(t *testing.T) {
	a := &app{}
	rejected := httptest.NewRecorder()
	a.hostOverview(rejected, httptest.NewRequest(http.MethodPost, "/api/v1/host/overview", nil))
	if rejected.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status: %d", rejected.Code)
	}
	received := httptest.NewRecorder()
	a.hostOverview(received, httptest.NewRequest(http.MethodGet, "/api/v1/host/overview", nil))
	if received.Code != http.StatusOK || received.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET response: %d %v", received.Code, received.Header())
	}
	if len(received.Body.Bytes()) > 64*1024 {
		t.Fatal("overview response exceeds expected bound")
	}
}
