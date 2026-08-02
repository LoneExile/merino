package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Minting a pairing ticket creates a durable grant: whoever scans the QR gets
// their own device session. That is device management, and /api/session has
// always told a paired phone canManageDevices=false — but the mint route was
// only wrapped in authed(), which proves nothing beyond "signed in", and a
// phone is signed in.
//
// So a phone could mint its own successors. Revoking a lost one would not
// have stopped it, which is the entire point of being able to revoke one.
// This predates the dashboard having a button for it: any authenticated
// client could POST the route directly.
//
// TestDeviceAdminRoutesRejectPairedPhone covers the sibling routes; this one
// exists because the mint route was missing from that list. It deliberately
// asserts a positive control too — 404 "pairing disabled" would otherwise
// look identical to a refusal and pass while enforcing nothing.
func TestPairingMintRejectsPairedPhone(t *testing.T) {
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
	// Pairing must be live, or the route answers 404 and the refusal below
	// would be vacuous.
	s.pairing = NewPairing("https://merino.example")
	s.cfg.Pairing = s.pairing
	s.http.Handler = s.routes()

	mint := func(id Identity) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.sessions.Issue(rr, id)
		req := httptest.NewRequest(http.MethodPost, "/api/pairing/mint", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(rr.Result().Cookies()[0])
		rec := httptest.NewRecorder()
		s.http.Handler.ServeHTTP(rec, req)
		return rec
	}

	// Positive control: an operator must still be able to mint, or this test
	// would pass just as well against a route that refuses everyone.
	if rec := mint(Identity{Subject: "alice", Name: "alice", Provider: "password"}); rec.Code != http.StatusOK {
		t.Fatalf("operator mint: got %d body %s, want 200", rec.Code, rec.Body.String())
	}

	// The defect: a paired phone minting its own successor.
	rec := mint(phoneID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("paired phone mint: got %d body %s, want 403", rec.Code, rec.Body.String())
	}
	// A ticket must not leak in the refusal body.
	if strings.Contains(rec.Body.String(), "token") || strings.Contains(rec.Body.String(), "data:image") {
		t.Fatalf("refusal leaked a ticket: %s", rec.Body.String())
	}
}
