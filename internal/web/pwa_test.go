package web

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LoneExile/merino/internal/app"
	"testing/fstest"
)

// The service worker and manifest are new fetch destinations that must be
// served with their own explicit content type — and, critically, must never
// fall through handleStatic's SPA fallback and silently come back as
// index.html, which would make the browser refuse to register the worker
// (wrong MIME type) or install the manifest at all.
func TestServiceWorkerAndManifestServedDirectly(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<head></head>")},
		"sw.js":                &fstest.MapFile{Data: []byte(`self.addEventListener("install", () => {});`)},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte(`{"name":"Merino"}`)},
	}
	s, err := New(&fakeSource{}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   assets,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	c := login(t, s, "alice", "correct-horse")

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		s.routes().ServeHTTP(rr, req)
		return rr
	}

	sw := get("/sw.js")
	if ct := sw.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("GET /sw.js Content-Type = %q, want text/javascript", ct)
	}
	swBody := sw.Body.String()
	if !strings.Contains(swBody, "addEventListener") {
		t.Errorf("GET /sw.js body = %q, want the service worker source", swBody)
	}
	if strings.Contains(swBody, "<head>") {
		t.Error("GET /sw.js was swallowed by the SPA fallback and returned index.html instead of the worker script")
	}
	if cc := sw.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("GET /sw.js Cache-Control = %q, want no-cache — a cached stale worker can never be updated", cc)
	}

	manifest := get("/manifest.webmanifest")
	if ct := manifest.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("GET /manifest.webmanifest Content-Type = %q, want application/manifest+json", ct)
	}
	manifestBody := manifest.Body.String()
	if !strings.Contains(manifestBody, `"name"`) {
		t.Errorf("GET /manifest.webmanifest body = %q, want the manifest JSON", manifestBody)
	}
	if strings.Contains(manifestBody, "<head>") {
		t.Error("GET /manifest.webmanifest was swallowed by the SPA fallback and returned index.html instead of the manifest")
	}
}

// The PWA entry points are deliberately NOT behind the session.
//
// An earlier version of this file asserted the opposite — that /sw.js and
// /manifest.webmanifest redirect to login like every other GET. That was
// wrong, and consistent-looking wrongness is why it went unnoticed: the
// browser fetches the manifest, its icons and service-worker updates outside
// any page and without our cookie, so gating them made the app silently
// uninstallable. The full contract is asserted in
// TestPWAAssetsAreReachableWithoutSession, with
// TestNonPWAPathsStillRequireSession guarding the other side.

// The manifest and service worker are new same-origin fetch destinations a
// strict default-src 'self' CSP does not implicitly cover on every browser;
// both directives must be present explicitly rather than relying on a
// default-src fallback.
func TestCSPAllowsManifestAndServiceWorker(t *testing.T) {
	s := testServer(t, &fakeSource{}, nil)
	c := login(t, s, "alice", "correct-horse")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	rr := httptest.NewRecorder()
	s.routes().ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "manifest-src 'self'") {
		t.Errorf("CSP %q missing manifest-src 'self'", csp)
	}
	if !strings.Contains(csp, "worker-src 'self'") {
		t.Errorf("CSP %q missing worker-src 'self'", csp)
	}
}

// PWA assets must be reachable WITHOUT a session.
//
// Regression test for a defect that every status-code check would have passed.
// The browser fetches the manifest, its icons, and service-worker updates on
// its own, with no cookie. Behind the auth wrapper those requests fell through
// to the SPA handler, which answered 200 with the login page — so Chrome
// reported "Manifest: Line: 1, column: 1, Syntax error", the icons decoded as
// HTML, and the app silently was not installable.
//
// Asserting the status is therefore useless here: the bug WAS a 200. This
// asserts the content type and the bytes.
func TestPWAAssetsAreReachableWithoutSession(t *testing.T) {
	// Own asset FS: the shared testServer fixture carries only index.html, and
	// this test is specifically about the OTHER files being reachable.
	assets := fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<head></head>")},
		"manifest.webmanifest":  &fstest.MapFile{Data: []byte(`{"name":"Merino"}`)},
		"sw.js":                 &fstest.MapFile{Data: []byte("// service worker\n")},
		"icon-192.png":          &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n192")},
		"icon-512.png":          &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\n512")},
		"icon-512-maskable.png": &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\nmask")},
		"apple-touch-icon.png":  &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\napple")},
		"favicon-32.png":        &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\nf32")},
		"favicon-64.png":        &fstest.MapFile{Data: []byte("\x89PNG\r\n\x1a\nf64")},
	}
	s, err := New(&fakeSource{}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   assets,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	// A client with no cookie jar and no redirect following: exactly what the
	// browser's manifest/icon fetcher looks like.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, tc := range []struct {
		path    string
		wantCT  string
		notHTML bool
	}{
		{"/manifest.webmanifest", "application/manifest+json", true},
		{"/sw.js", "text/javascript", true},
		{"/icon-192.png", "image/png", true},
		{"/icon-512.png", "image/png", true},
		{"/icon-512-maskable.png", "image/png", true},
		{"/apple-touch-icon.png", "image/png", true},
		{"/favicon-32.png", "image/png", true},
		{"/favicon-64.png", "image/png", true},
	} {
		resp, err := client.Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s = %d, want 200 without a session", tc.path, resp.StatusCode)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.wantCT) {
			t.Errorf("%s content-type = %q, want %q", tc.path, ct, tc.wantCT)
		}
		// The actual failure mode: the login page, served with a 200.
		if tc.notHTML && bytes.Contains(body, []byte("<!DOCTYPE html")) {
			t.Errorf("%s served HTML (the login page) instead of the asset", tc.path)
		}
		if len(body) == 0 {
			t.Errorf("%s served an empty body", tc.path)
		}
	}
}

// Icons must revalidate, not stick in a heuristic cache forever.
//
// The favicon used to ship with no Cache-Control and no ETag. Browsers and
// Cloudflare then kept the previous tiled sheep for days after a bare-sheep
// rebuild, so the tab strip never updated. The contract is: first GET returns
// an ETag + Cache-Control: no-cache + the body; a second GET with that ETag
// returns 304 and no body.
func TestIconAssetRevalidatesWithETag(t *testing.T) {
	const payload = "\x89PNG\r\n\x1a\nbare-sheep"
	assets := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<head></head>")},
		"favicon-64.png": &fstest.MapFile{Data: []byte(payload)},
	}
	s, err := New(&fakeSource{}, Config{
		Provider: NewPasswordProvider("alice", "correct-horse", DirectIP, false),
		Policy:   SingleOperator{},
		Assets:   assets,
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	first, err := http.Get(srv.URL + "/favicon-64.png")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	body, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first GET status = %d, want 200", first.StatusCode)
	}
	if got := string(body); got != payload {
		t.Fatalf("first GET body = %q, want %q", got, payload)
	}
	if cc := first.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("first GET missing ETag")
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag = %q, want a quoted strong validator", etag)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/favicon-64.png", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revalidate GET: %v", err)
	}
	reBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Errorf("revalidate status = %d, want 304", second.StatusCode)
	}
	if len(reBody) != 0 {
		t.Errorf("revalidate body = %q, want empty on 304", reBody)
	}
	if got := second.Header.Get("ETag"); got != etag {
		t.Errorf("revalidate ETag = %q, want %q", got, etag)
	}
}

// The unauthenticated surface must stay exactly that list. Anything carrying
// user data must still demand a session.
func TestNonPWAPathsStillRequireSession(t *testing.T) {
	s := testServer(t, &fakeSource{agents: []app.Agent{agent("p1")}}, nil)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, path := range []string{"/", "/api/agents", "/api/session", "/api/panes/p1/output"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s served 200 with no session — the public list has widened", path)
		}
	}
}
