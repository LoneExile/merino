package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The session has two clocks: an idle window that activity pushes out, and an
// absolute cap measured from sign-in that nothing extends. These tests drive a
// fake clock across each boundary, because the real ones are 12 hours and 7
// days apart and a sleeping test proves nothing anyone will wait for.

// atTime builds a Sessions whose clock the test controls.
func atTime(t *testing.T, now *time.Time) *Sessions {
	t.Helper()
	s, err := NewSessions(false)
	if err != nil {
		t.Fatalf("NewSessions: %v", err)
	}
	s.now = func() time.Time { return *now }
	return s
}

// carry moves the Set-Cookie from a response onto a fresh request, the way a
// browser would. Returns false when the response set no cookie at all.
func carry(t *testing.T, rec *httptest.ResponseRecorder) (*http.Request, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			req.AddCookie(c)
			return req, true
		}
	}
	return req, false
}

func issueAt(t *testing.T, s *Sessions, id Identity) *http.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Issue(rec, id)
	req, ok := carry(t, rec)
	if !ok {
		t.Fatal("Issue set no session cookie")
	}
	return req
}

var testID = Identity{Subject: "device:abc", Name: "iPhone", Provider: "pairing", Roles: []string{"view", "control"}}

func TestSessionReadableWithinIdleWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	now = now.Add(idleTTL - time.Minute)
	got, ok := s.Read(req)
	if !ok {
		t.Fatal("session rejected one minute before the idle deadline")
	}
	if got.Subject != testID.Subject || strings.Join(got.Roles, ",") != "view,control" {
		t.Fatalf("identity did not survive the round trip: %+v", got)
	}
}

func TestSessionExpiresWhenIdlePastDeadline(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// Never touched, so nothing renewed it.
	now = now.Add(idleTTL + time.Second)
	if _, ok := s.Read(req); ok {
		t.Fatal("session survived past the idle deadline without activity")
	}
}

func TestActivityPastHalfwayExtendsTheIdleWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// Use it just past the halfway mark: the cookie should be re-issued.
	now = now.Add(renewAfter + time.Minute)
	sess, ok := s.ReadSession(req)
	if !ok {
		t.Fatal("session unreadable at the halfway mark")
	}
	rec := httptest.NewRecorder()
	if !s.Renew(rec, sess) {
		t.Fatal("Renew declined past the halfway mark")
	}
	renewed, ok := carry(t, rec)
	if !ok {
		t.Fatal("Renew reported success but set no cookie")
	}

	// Now go past the ORIGINAL deadline. The renewed cookie must outlive it.
	now = now.Add(idleTTL - time.Minute)
	if _, ok := s.Read(req); ok {
		t.Fatal("the original cookie should have expired")
	}
	if _, ok := s.Read(renewed); !ok {
		t.Fatal("renewal did not extend the session past the original deadline")
	}
}

func TestRenewDeclinesEarlyInTheIdleWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// A polling client must not rotate Set-Cookie on every request.
	now = now.Add(time.Minute)
	sess, _ := s.ReadSession(req)
	rec := httptest.NewRecorder()
	if s.Renew(rec, sess) {
		t.Fatal("Renew fired one minute into a twelve hour window")
	}
	if _, ok := carry(t, rec); ok {
		t.Fatal("Renew declined but still set a cookie")
	}
}

func TestAbsoluteCapEndsAnActivelyUsedSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// Simulate a client that keeps the session alive by using it constantly:
	// renew every half window for well past the cap.
	deadline := now.Add(absoluteTTL + 2*idleTTL)
	for now.Before(deadline) {
		now = now.Add(renewAfter + time.Minute)
		sess, ok := s.ReadSession(req)
		if !ok {
			break // the cap caught it, which is the point
		}
		rec := httptest.NewRecorder()
		if s.Renew(rec, sess) {
			if next, ok := carry(t, rec); ok {
				req = next
			}
		}
	}

	if _, ok := s.Read(req); ok {
		t.Fatal("constant activity kept a session alive past the absolute cap")
	}
}

func TestActiveSessionSurvivesRightUpToTheCap(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	now := start
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// The cap is a promise in both directions: it ends the session, and it
	// does not end it sooner. A renewal rule that only fires on a "meaningful"
	// gain stops short of the ceiling and logs the user out early — this is
	// the regression, and it is invisible to a test that only checks the
	// session is dead afterwards.
	target := start.Add(absoluteTTL - time.Minute)
	for now.Before(target) {
		if step := target.Sub(now); step > renewAfter+time.Minute {
			now = now.Add(renewAfter + time.Minute)
		} else {
			now = target
		}
		sess, ok := s.ReadSession(req)
		if !ok {
			t.Fatalf("session died %v short of the cap despite constant use",
				start.Add(absoluteTTL).Sub(now))
		}
		rec := httptest.NewRecorder()
		if s.Renew(rec, sess) {
			if next, ok := carry(t, rec); ok {
				req = next
			}
		}
	}

	if _, ok := s.Read(req); !ok {
		t.Fatal("session should still be alive one minute before the cap")
	}
}

func TestRenewNearTheCapDoesNotRewriteAnUnchangedCookie(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	now := start
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// Keep it alive by using it, up to the final stretch before the cap. A
	// session cannot reach the cap by sitting idle — it dies of idle first —
	// so getting there at all requires the renewals under test.
	target := start.Add(absoluteTTL - idleTTL/4)
	for now.Before(target) {
		if step := target.Sub(now); step > renewAfter+time.Minute {
			now = now.Add(renewAfter + time.Minute)
		} else {
			now = target
		}
		sess, ok := s.ReadSession(req)
		if !ok {
			t.Fatalf("session died while being used, %v after sign-in", now.Sub(start))
		}
		rec := httptest.NewRecorder()
		if s.Renew(rec, sess) {
			if next, ok := carry(t, rec); ok {
				req = next
			}
		}
	}

	// Now inside the last idle window before the cap: the clamp has already
	// fixed this cookie's expiry at the ceiling, so a rewrite would carry the
	// identical value. Deriving the last write from ExpiresAt-idle mispredicts
	// here and would rotate Set-Cookie on every request; the guard must not.
	sess, ok := s.ReadSession(req)
	if !ok {
		t.Fatal("session should still be valid just under the cap")
	}
	if want := start.Add(absoluteTTL); !sess.ExpiresAt.Equal(want) {
		t.Fatalf("expiry should be clamped to the cap %v, got %v", want, sess.ExpiresAt)
	}
	rec := httptest.NewRecorder()
	if s.Renew(rec, sess) {
		t.Fatal("Renew rewrote a cookie whose expiry the cap had already fixed")
	}
}

func TestReadEnforcesTheCapIndependentlyOfTheClamp(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)

	// Read checks the cap as well as the idle deadline, and that check has to
	// stand on its own. In normal operation the clamp in deadline() already
	// keeps expiry under the ceiling, so removing Read's check breaks nothing
	// a round-trip test would notice — it is defence in depth against a future
	// change to the clamp, and untested defence in depth rots.
	//
	// So: forge the state the clamp cannot produce. A validly signed cookie,
	// comfortably inside its idle window, whose issuedAt is older than the cap.
	payload := strings.Join([]string{
		b64(testID.Subject),
		b64(testID.Name),
		b64(testID.Provider),
		b64(strings.Join(testID.Roles, ",")),
		strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
		strconv.FormatInt(now.Add(-absoluteTTL-time.Hour).Unix(), 10),
	}, ".")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: payload + "~" + s.sign(payload)})

	if _, ok := s.Read(req); ok {
		t.Fatal("a cookie past the absolute cap was accepted on its idle deadline alone")
	}
}

func TestReadRejectsPreIssuedAtCookieFormat(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)

	// A correctly signed cookie in the old five-field layout: what a browser
	// still holds across the upgrade. It must fail closed, not parse into a
	// session with no absolute cap.
	expiry := now.Add(idleTTL)
	payload := strings.Join([]string{
		b64(testID.Subject),
		b64(testID.Name),
		b64(testID.Provider),
		b64(strings.Join(testID.Roles, ",")),
		strconv.FormatInt(expiry.Unix(), 10),
	}, ".")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: payload + "~" + s.sign(payload)})

	if _, ok := s.Read(req); ok {
		t.Fatal("a pre-issuedAt cookie was accepted")
	}
}

func TestReadRejectsTamperedIssuedAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	s := atTime(t, &now)
	req := issueAt(t, s, testID)

	// Push issuedAt far into the future to escape the cap. The field is inside
	// the signed payload, so editing it must break the signature.
	c, err := req.Cookie(sessionCookie)
	if err != nil {
		t.Fatalf("cookie: %v", err)
	}
	payload, _, _ := strings.Cut(c.Value, "~")
	parts := strings.Split(payload, ".")
	parts[5] = strconv.FormatInt(now.Add(365*24*time.Hour).Unix(), 10)
	forged := strings.Join(parts, ".")

	tampered := httptest.NewRequest(http.MethodGet, "/", nil)
	tampered.AddCookie(&http.Cookie{Name: sessionCookie, Value: forged + "~" + strings.SplitN(c.Value, "~", 2)[1]})

	if _, ok := s.Read(tampered); ok {
		t.Fatal("a forged issuedAt was accepted")
	}
}
