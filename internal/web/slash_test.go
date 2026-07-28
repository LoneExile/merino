package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LoneExile/merino/internal/app"
)

func TestSlashEndpointReturnsCatalog(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)
	c := login(t, s, "alice", "correct-horse")
	if c == nil {
		t.Fatal("login failed")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/slash?agent=claude&q=hel", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var hits []app.SlashCommand
	if err := json.Unmarshal(rr.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected slash hits")
	}
	found := false
	for _, h := range hits {
		if h.Name == "help" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("help missing: %+v", hits)
	}
}
