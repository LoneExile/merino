package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogoutPOSTClearsSession(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)
	rr := httptest.NewRecorder()
	s.sessions.Issue(rr, Identity{Subject: "alice", Name: "alice", Provider: "password"})
	cookie := rr.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("no session cookie issued")
	}

	// GET must not clear (no route / not CSRF-friendly).
	reqGet := httptest.NewRequest(http.MethodGet, "/logout", nil)
	reqGet.AddCookie(cookie[0])
	recGet := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(recGet, reqGet)
	if recGet.Code == http.StatusSeeOther && recGet.Header().Get("Location") == "/login" {
		// Would mean GET still logs out.
		for _, c := range recGet.Result().Cookies() {
			if c.Name == sessionCookie && (c.MaxAge < 0 || c.Value == "") {
				t.Fatal("GET /logout cleared session — CSRF risk")
			}
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookie[0])
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status=%d want 303, body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location=%q want /login", loc)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && (c.MaxAge < 0 || c.Value == "") {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("session cookie not cleared: %#v", rec.Result().Cookies())
	}
}
