package web

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// pushTestServer builds a push-enabled Server the same way writeServer builds
// a write-enabled one (same fakes, same shape), but pointed at a fresh temp
// dir so tests never touch a real VAPID keypair or subscription file.
func pushTestServer(t *testing.T, policy Policy) (*Server, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	audit := app.NewAuditTo(nopCloser{buf})
	if policy == nil {
		policy = SingleOperator{}
	}
	s, err := New(&fakeSource{agents: []app.Agent{agent("p1")}}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   policy,
		Assets:   fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}},
		Logger:   slog.New(slog.DiscardHandler),
		Audit:    audit,
		PushDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	if s.push == nil {
		t.Fatal("push should have initialised against a fresh temp dir")
	}
	return s, buf
}

func getWithCookie(t *testing.T, s *Server, c *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)
	return rr
}

// --- auth ---

func TestPushSubscribeRequiresAuth(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	rr := post(t, s, nil, "/api/push/subscribe",
		`{"endpoint":"https://push.example/ep1","keys":{"p256dh":"p","auth":"a"}}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPushUnsubscribeRequiresAuth(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	rr := post(t, s, nil, "/api/push/unsubscribe", `{"endpoint":"https://push.example/ep1"}`)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPushKeyRequiresAuth(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	if rr := getWithCookie(t, s, nil, "/api/push/key"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// --- audit ---

func TestPushSubscribeIsAudited(t *testing.T) {
	s, auditBuf := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/push/subscribe",
		`{"endpoint":"https://push.example/ep1","keys":{"p256dh":"p256","auth":"auth1"}}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d: %s", rr.Code, rr.Body.String())
	}

	lines := strings.Split(strings.TrimSpace(auditBuf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit has %d lines, want 1: %s", len(lines), auditBuf.String())
	}
	var e app.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("audit line is not JSON: %v", err)
	}
	if e.Actor != "alice" || e.Action != "push_subscribe" || !e.Allowed {
		t.Errorf("audit entry = %+v", e)
	}
	if !strings.Contains(e.Detail, "https://push.example/ep1") {
		t.Errorf("audit did not record the endpoint: %q", e.Detail)
	}

	if len(s.push.subs) != 1 {
		t.Fatalf("subs = %d, want 1", len(s.push.subs))
	}
	if got := s.push.subs["https://push.example/ep1"].Identity.Name; got != "alice" {
		t.Errorf("stored identity name = %q, want alice", got)
	}
}

func TestPushUnsubscribeIsAudited(t *testing.T) {
	s, auditBuf := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	post(t, s, c, "/api/push/subscribe",
		`{"endpoint":"https://push.example/ep1","keys":{"p256dh":"p","auth":"a"}}`)
	auditBuf.Reset()

	rr := post(t, s, c, "/api/push/unsubscribe", `{"endpoint":"https://push.example/ep1"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d: %s", rr.Code, rr.Body.String())
	}
	if len(s.push.subs) != 0 {
		t.Fatalf("subs = %d after unsubscribe, want 0", len(s.push.subs))
	}

	lines := strings.Split(strings.TrimSpace(auditBuf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit has %d lines, want 1: %s", len(lines), auditBuf.String())
	}
	var e app.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("audit line is not JSON: %v", err)
	}
	if e.Actor != "alice" || e.Action != "push_unsubscribe" || !e.Allowed {
		t.Errorf("audit entry = %+v", e)
	}
}

func TestPushSubscribeIsIdempotentOnEndpoint(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	body := `{"endpoint":"https://push.example/ep1","keys":{"p256dh":"p256","auth":"auth1"}}`
	if rr := post(t, s, c, "/api/push/subscribe", body); rr.Code != http.StatusNoContent {
		t.Fatalf("first subscribe = %d", rr.Code)
	}
	if rr := post(t, s, c, "/api/push/subscribe", body); rr.Code != http.StatusNoContent {
		t.Fatalf("second subscribe = %d", rr.Code)
	}
	if len(s.push.subs) != 1 {
		t.Fatalf("subs = %d after subscribing the same endpoint twice, want 1", len(s.push.subs))
	}
}

func TestPushSubscribeRejectsMalformedBody(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	rr := post(t, s, c, "/api/push/subscribe", `{"endpoint":"","keys":{"p256dh":"","auth":""}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an incomplete subscription", rr.Code)
	}
}

// --- session / route presence ---

func TestPushKeyReturnsVAPIDPublicKey(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	rr := getWithCookie(t, s, c, "/api/push/key")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Key == "" || body.Key != s.push.publicKey {
		t.Errorf("key = %q, want %q", body.Key, s.push.publicKey)
	}
}

func TestPushSessionReportsEnabled(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	c := login(t, s, "alice", "correct-horse")

	rr := getWithCookie(t, s, c, "/api/session")
	var sess map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &sess); err != nil {
		t.Fatalf("session response is not JSON: %v", err)
	}
	if enabled, _ := sess["pushEnabled"].(bool); !enabled {
		t.Error("session should report pushEnabled: true once push has initialised")
	}
}

// Push must be pure absence when it is not configured — no routes, no
// misleading session flag — matching the convention Writer already
// established for the write endpoints.
//
// GET /api/push/key cannot be proven absent by status code alone: the
// catch-all "GET /" pattern is a subtree match (Go 1.22+ ServeMux), so an
// unmounted GET path falls through to the SPA shell with a 200, exactly
// like /api/sessions does with no Sessions configured (see
// TestSessionsRouteAbsentWithoutCapability in sessions_test.go). Proving
// absence means checking it did NOT answer as JSON, not checking for a 404.
func TestPushRoutesAbsentWhenDisabled(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil) // PushDir left empty
	if s.push != nil {
		t.Fatal("push should be disabled when PushDir is empty")
	}
	c := login(t, s, "alice", "correct-horse")

	if rr := getWithCookie(t, s, c, "/api/push/key"); strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json") {
		t.Errorf("GET /api/push/key answered as JSON with push disabled: %s", rr.Body.String())
	}

	for _, path := range []string{"/api/push/subscribe", "/api/push/unsubscribe"} {
		if rr := post(t, s, c, path, `{}`); rr.Code >= 200 && rr.Code < 300 {
			t.Errorf("POST %s succeeded with push disabled (%d)", path, rr.Code)
		}
	}

	rr := getWithCookie(t, s, c, "/api/session")
	var sess map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &sess); err != nil {
		t.Fatalf("session response is not JSON: %v", err)
	}
	if enabled, _ := sess["pushEnabled"].(bool); enabled {
		t.Error("session should report pushEnabled: false when push is disabled")
	}
}

// --- notification delivery: policy filtering, pruning, non-blocking ---

// permitOnly is a minimal Policy that permits exactly one identity by name —
// enough to prove notifyBlocked actually consults Policy.CanView per
// subscription instead of broadcasting to everyone who ever subscribed.
type permitOnly struct{ name string }

func (p permitOnly) CanView(id Identity, _ app.Agent) bool { return id.Name == p.name }
func (p permitOnly) CanControl(Identity, app.Agent) bool   { return false }

func TestNotifyBlockedRespectsPolicy(t *testing.T) {
	s, _ := pushTestServer(t, permitOnly{name: "alice"})

	if err := s.push.subscribe(pushSubscription{
		Endpoint: "https://push.example/alice",
		Keys:     pushKeys{P256dh: "p", Auth: "a"},
		Identity: Identity{Name: "alice"},
	}); err != nil {
		t.Fatalf("subscribe alice: %v", err)
	}
	if err := s.push.subscribe(pushSubscription{
		Endpoint: "https://push.example/bob",
		Keys:     pushKeys{P256dh: "p", Auth: "a"},
		Identity: Identity{Name: "bob"},
	}); err != nil {
		t.Fatalf("subscribe bob: %v", err)
	}

	var mu sync.Mutex
	var sentTo []string
	s.push.send = func(sub pushSubscription, _ []byte) (int, error) {
		mu.Lock()
		sentTo = append(sentTo, sub.Identity.Name)
		mu.Unlock()
		return http.StatusCreated, nil
	}

	s.push.notifyBlocked(agent("p1"), s.cfg.Policy)

	mu.Lock()
	defer mu.Unlock()
	if len(sentTo) != 1 || sentTo[0] != "alice" {
		t.Fatalf("sent to %v, want exactly one send to alice (bob's Policy.CanView denies p1)", sentTo)
	}
}

func TestNotifyBlockedPrunesGoneSubscription(t *testing.T) {
	s, _ := pushTestServer(t, nil) // SingleOperator: permits everyone

	if err := s.push.subscribe(pushSubscription{
		Endpoint: "https://push.example/gone",
		Keys:     pushKeys{P256dh: "p", Auth: "a"},
		Identity: Identity{Name: "alice"},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var calls int
	s.push.send = func(pushSubscription, []byte) (int, error) {
		calls++
		return http.StatusGone, nil
	}

	s.push.notifyBlocked(agent("p1"), s.cfg.Policy)
	if calls != 1 {
		t.Fatalf("send called %d times, want 1", calls)
	}
	if len(s.push.subs) != 0 {
		t.Fatalf("subs = %d after a 410, want 0 (pruned)", len(s.push.subs))
	}

	// Pruning must be durable, not just in-memory: reload straight from disk.
	reloaded, err := loadSubscriptions(s.push.subsPath)
	if err != nil {
		t.Fatalf("reload subscriptions: %v", err)
	}
	if len(reloaded) != 0 {
		t.Fatalf("reloaded subscriptions = %v, want empty after pruning", reloaded)
	}

	// A second notification must not even attempt the now-pruned endpoint.
	s.push.notifyBlocked(agent("p1"), s.cfg.Policy)
	if calls != 1 {
		t.Fatalf("send called %d times after pruning, want still 1", calls)
	}
}

// TestNotifyBlockedDoesNotBlockCaller proves Server.NotifyBlocked hands
// delivery off to its own goroutine rather than running it inline, per the
// hard requirement that sending must never stall the status-observation
// path that triggers it.
func TestNotifyBlockedDoesNotBlockCaller(t *testing.T) {
	s, _ := pushTestServer(t, nil)
	if err := s.push.subscribe(pushSubscription{
		Endpoint: "https://push.example/slow",
		Keys:     pushKeys{P256dh: "p", Auth: "a"},
		Identity: Identity{Name: "alice"},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	release := make(chan struct{})
	defer close(release)
	s.push.send = func(pushSubscription, []byte) (int, error) {
		<-release // never fires during this test
		return http.StatusCreated, nil
	}

	returned := make(chan struct{})
	go func() {
		s.NotifyBlocked(agent("p1"))
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("NotifyBlocked blocked on a slow send; it must hand delivery off to its own goroutine")
	}
}

// --- persistence ---

func TestPushVAPIDKeysArePersistedAndReloaded(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	pm1, err := newPushManager(dir, log)
	if err != nil {
		t.Fatalf("newPushManager: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, vapidFileName))
	if err != nil {
		t.Fatalf("stat vapid file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("vapid file mode = %o, want 0600", perm)
	}

	// A second manager over the same directory must load the same keypair —
	// regenerating it would silently invalidate every existing browser
	// subscription, whose validity depends on the public key it was created
	// against matching the one future sends are signed with.
	pm2, err := newPushManager(dir, log)
	if err != nil {
		t.Fatalf("second newPushManager: %v", err)
	}
	if pm1.publicKey != pm2.publicKey || pm1.privateKey != pm2.privateKey {
		t.Error("VAPID keys were regenerated instead of loaded from disk")
	}
}

func TestPushSubscriptionsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	pm1, err := newPushManager(dir, log)
	if err != nil {
		t.Fatalf("newPushManager: %v", err)
	}
	sub := pushSubscription{
		Endpoint: "https://push.example/ep1",
		Keys:     pushKeys{P256dh: "p", Auth: "a"},
		Identity: Identity{Name: "alice", Subject: "sub-1", Provider: "password"},
	}
	if err := pm1.subscribe(sub); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	info, err := os.Stat(pm1.subsPath)
	if err != nil {
		t.Fatalf("stat subscriptions file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("subscriptions file mode = %o, want 0600", perm)
	}

	pm2, err := newPushManager(dir, log)
	if err != nil {
		t.Fatalf("second newPushManager: %v", err)
	}
	got, ok := pm2.subs[sub.Endpoint]
	if !ok {
		t.Fatal("subscription did not survive a fresh newPushManager against the same directory")
	}
	if got.Identity.Name != sub.Identity.Name ||
		got.Identity.Subject != sub.Identity.Subject ||
		got.Identity.Provider != sub.Identity.Provider {
		t.Errorf("reloaded identity = %+v, want %+v", got.Identity, sub.Identity)
	}
}

// TestPushSendRealSignsAValidVAPIDRequest exercises pushManager.sendReal —
// the ONE piece of this feature every other test in this file deliberately
// stubs out — against a real HTTP endpoint, and independently decodes what
// it actually sent. This is the check a stub cannot substitute for: it is
// exactly the wire format a real push service inspects, including the RFC
// 8291 encryption (proving the payload is not sent in the clear) and the
// RFC 8292 VAPID JWT claims (proving, in particular, that the "sub" claim
// is a real contact URI rather than the empty-string default that some
// real push services — Apple's, notably — reject outright).
func TestPushSendRealSignsAValidVAPIDRequest(t *testing.T) {
	dir := t.TempDir()
	pm, err := newPushManager(dir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newPushManager: %v", err)
	}

	var gotAuth, gotEncoding, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEncoding = r.Header.Get("Content-Encoding")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// A realistic subscriber keypair: an uncompressed P-256 public key and a
	// 16-byte auth secret, exactly the shape PushSubscription.getKey()
	// returns in a real browser — so webpush-go's real encryption runs end
	// to end instead of being bypassed.
	subPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscriber key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	sub := pushSubscription{
		Endpoint: srv.URL + "/push/abc123",
		Keys: pushKeys{
			P256dh: base64.RawURLEncoding.EncodeToString(subPriv.PublicKey().Bytes()),
			Auth:   base64.RawURLEncoding.EncodeToString(authSecret),
		},
	}

	plaintext := []byte(`{"title":"omp needs you","body":"secret pane detail"}`)
	status, err := pm.sendReal(sub, plaintext)
	if err != nil {
		t.Fatalf("sendReal: %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}

	if gotEncoding != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", gotEncoding)
	}
	if gotContentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotContentType)
	}
	if bytes.Contains(gotBody, plaintext) || bytes.Contains(gotBody, []byte("secret pane detail")) {
		t.Error("request body contains the plaintext payload verbatim — it was not encrypted")
	}

	claims := decodeVAPIDClaims(t, gotAuth)
	if got, _ := claims["sub"].(string); got != pushVAPIDSubject {
		t.Errorf("VAPID sub claim = %q, want %q (empty/bare mailto: is rejected by some real push services)",
			got, pushVAPIDSubject)
	}
	if got, _ := claims["aud"].(string); got != srv.URL {
		t.Errorf("VAPID aud claim = %q, want %q", got, srv.URL)
	}
	exp, _ := claims["exp"].(float64)
	now := float64(time.Now().Unix())
	if exp <= now || exp > now+13*3600 {
		t.Errorf("VAPID exp claim = %v, want a timestamp within the next ~12h of %v", exp, now)
	}
}

// decodeVAPIDClaims parses an `Authorization: vapid t=<jwt>, k=<key>` header
// and returns the JWT's claim set. It does not verify the signature — this
// test cares about what was asserted, which is what a push service reads
// before it ever gets to checking who signed it.
func decodeVAPIDClaims(t *testing.T, authHeader string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(authHeader, "vapid ") {
		t.Fatalf("Authorization header = %q, want a vapid scheme", authHeader)
	}
	var jwtToken string
	for _, part := range strings.Split(strings.TrimPrefix(authHeader, "vapid "), ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "t="); ok {
			jwtToken = v
		}
	}
	segments := strings.Split(jwtToken, ".")
	if len(segments) != 3 {
		t.Fatalf("Authorization header %q has no well-formed t=<jwt>", authHeader)
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("JWT payload is not JSON: %v", err)
	}
	return claims
}
