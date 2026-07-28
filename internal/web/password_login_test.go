package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPasswordLoginDisabledBlocksForm(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)
	pp := s.cfg.Provider.(*PasswordProvider)
	pp.SetPasswordLogin(false)

	// GET login should still render (QR path).
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "disabled") && !strings.Contains(rec.Body.String(), "QR") {
		t.Fatalf("expected disabled messaging, body=%s", rec.Body.String()[:200])
	}
	if strings.Contains(rec.Body.String(), `name="username"`) {
		t.Fatal("username field should be hidden when password login off")
	}

	form := url.Values{"username": {"alice"}, "password": {"correct-horse"}}
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST password status=%d want 403/401", rec.Code)
	}
	// Must not set session cookie.
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" && c.MaxAge >= 0 {
			t.Fatal("must not issue session when password login disabled")
		}
	}
}
