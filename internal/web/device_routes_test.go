package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestDeviceAdminRoutesRejectPairedPhone(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenDeviceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, phoneID, err := store.Mint("phone-ua", "pairing", nil)
	if err != nil {
		t.Fatal(err)
	}

	s := testServer(t, &fakeSource{}, nil)
	s.cfg.Devices = store
	s.cfg.StateDir = dir
	// Rebuild routes with devices mounted.
	s.http.Handler = s.routes()

	// Issue a session as the paired phone.
	rr := httptest.NewRecorder()
	s.sessions.Issue(rr, phoneID)
	cookie := rr.Result().Cookies()[0]

	for _, path := range []string{
		"/api/devices",
		"/api/devices/revoke-all",
		"/api/auth/password",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if path != "/api/devices" {
			req = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: got %d body %s, want 403", path, rec.Code, rec.Body.String())
		}
	}

	// Operator (password identity) can list.
	rr2 := httptest.NewRecorder()
	s.sessions.Issue(rr2, Identity{Subject: "alice", Name: "alice", Provider: "password"})
	opCookie := rr2.Result().Cookies()[0]
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.AddCookie(opCookie)
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator list: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["activeCount"].(float64) != 1 {
		t.Fatalf("activeCount=%v", body["activeCount"])
	}
	_ = filepath.Join(dir, "devices.json")
}

func TestRevokedDeviceCookieRejected(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenDeviceStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, id, err := store.Mint("phone", "pairing", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, &fakeSource{}, nil)
	s.cfg.Devices = store
	s.cfg.StateDir = dir
	s.http.Handler = s.routes()

	rr := httptest.NewRecorder()
	s.sessions.Issue(rr, id)
	cookie := rr.Result().Cookies()[0]

	if _, err := store.Revoke(d.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 after revoke", rec.Code)
	}
}
