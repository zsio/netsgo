package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"netsgo/pkg/updater"
)

func TestAPI_VersionCheck(t *testing.T) {
	orig := checkVersionUpdate
	t.Cleanup(func() { checkVersionUpdate = orig })
	checkVersionUpdate = func(currentVersion string) (*updater.Result, bool, error) {
		if currentVersion != "v0.1.0-beta.16" {
			t.Fatalf("current version: want v0.1.0-beta.16, got %q", currentVersion)
		}
		return &updater.Result{
			OldVersion: currentVersion,
			NewVersion: "v0.1.0-beta.17",
		}, true, nil
	}

	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/version/check?version=v0.1.0-beta.16", nil)
	w := httptest.NewRecorder()
	s.handleAPIVersionCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if result["current_version"] != "v0.1.0-beta.16" {
		t.Fatalf("current_version mismatch: %v", result["current_version"])
	}
	if result["latest_version"] != "v0.1.0-beta.17" {
		t.Fatalf("latest_version mismatch: %v", result["latest_version"])
	}
	if result["update_available"] != true {
		t.Fatalf("update_available mismatch: %v", result["update_available"])
	}
}

func TestAPI_VersionCheckRequiresVersion(t *testing.T) {
	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/version/check", nil)
	w := httptest.NewRecorder()
	s.handleAPIVersionCheck(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status code: want 400, got %d", w.Code)
	}
}

func TestAPI_VersionCheckReportsLookupFailure(t *testing.T) {
	orig := checkVersionUpdate
	t.Cleanup(func() { checkVersionUpdate = orig })
	checkVersionUpdate = func(currentVersion string) (*updater.Result, bool, error) {
		return nil, false, errors.New("network unavailable")
	}

	s := New(8080)
	req := httptest.NewRequest(http.MethodGet, "/api/version/check?version=v0.1.0-beta.16", nil)
	w := httptest.NewRecorder()
	s.handleAPIVersionCheck(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status code: want 502, got %d", w.Code)
	}
}
