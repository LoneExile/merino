package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LoneExile/merino/internal/herdr"
)

// errorFrame is herdr's failure envelope, captured from a live server: a pane
// it cannot answer for gets {"id":…,"error":{"code":…,"message":…}}.
func errorFrame(id, code, message string) map[string]any {
	return map[string]any{"id": id, "error": map[string]any{
		"code": code, "message": message,
	}}
}

// paneRevision is the revision the fake's pane.get starts at.
//
// It is deliberately NOT zero, and deliberately not equal to what the fake's
// pane.read reports. Measured against herdr 0.9.0 with a live pane:
// pane.get answers {"type":"pane_info","pane":{…,"revision":264817,…}} while
// pane.read answers {"type":"pane_read","read":{…,"revision":0,…}} for that
// same pane, at every read source and line window tried, idle or working. A
// fixture that started the two at the same value would let a baseline taken
// from the read pass here while the gate never skipped once against a real
// server — which is how this was found.
const paneRevision = 264817

// paneReadRevision is what the wire carries in a pane.read response. It is a
// decoy: the key is present and its value is always 0.
const paneReadRevision = 0

// scriptedPane is a herdr socket that answers pane.read with a scripted
// sequence of screens, one per call, repeating the last forever.
//
// It exists to cover the contract that a live probe caught and the previous
// fake could not: the earlier implementation subscribed to
// pane.output_matched, which delivers exactly ONE event per subscription
// against a real herdr and then goes silent. Its test scripted a stream of
// events onto the wire, so it passed while the feature was dead in practice.
// This fake models what herdr actually offers — a readable screen — so a
// regression to any once-only mechanism fails here.
type scriptedPane struct {
	path string

	mu      sync.Mutex
	screens []string
	reads   int
	// gets counts pane.get calls — the cheap question the poll loop now asks
	// on every tick instead of re-reading the screen.
	gets int
	// revision is what pane.get reports. It stands still unless a test moves
	// it, which is how the loop's gate is exercised: a real herdr moves it
	// when the screen changes.
	revision int64
	// getErr makes pane.get answer with herdr's pane_not_found failure while
	// pane.read keeps serving the screen.
	getErr bool
	// failNextRead makes the next pane.read call fail.
	failNextRead bool
	// stageFailure arms the sequence stageReadFailure describes; staged
	// records that it has fired.
	stageFailure bool
	staged       bool
	// params records each pane.read call's raw request params, in call
	// order, so a test can assert on the wire shape (format, strip_ansi)
	// rather than only on the text a call returns.
	params []json.RawMessage
}

func newScriptedPane(t *testing.T, screens ...string) *scriptedPane {
	t.Helper()
	dir, err := os.MkdirTemp("", "sp")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	p := &scriptedPane{path: filepath.Join(dir, "s.sock"), screens: screens, revision: paneRevision}

	ln, err := net.Listen("unix", p.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close(); os.RemoveAll(dir) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *scriptedPane) next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.reads
	p.reads++
	if i >= len(p.screens) {
		i = len(p.screens) - 1
	}
	return p.screens[i]
}

func (p *scriptedPane) readCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}

func (p *scriptedPane) getCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets
}

// bumpRevision is how a test says "the screen changed" — the same signal a
// real herdr gives, rather than the test reaching into the poll loop.
func (p *scriptedPane) bumpRevision() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revision++
}

// untrackedRevision makes the fake report revision 0, which is what a live
// herdr answers for a pane it is not tracking — a plain shell, for instance.
// See TestStreamKeepsReadingAPaneWithNoTrackedRevision.
func (p *scriptedPane) untrackedRevision() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revision = 0
}

// failGets makes pane.get fail while pane.read keeps answering.
func (p *scriptedPane) failGets() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.getErr = true
}

// stageReadFailure sets up the sequence TestStreamRereadsAfterAFailedRead
// needs. Every step is settled by call order rather than by racing the ticker:
// the first read is served, the screen then changes, and the read for that
// change fails.
//
// The order matters. A failure on the very first read would prove nothing: the
// loop cannot skip before it has primed, so both a baseline committed early and
// one committed late would read again on the next tick.
func (p *scriptedPane) stageReadFailure() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stageFailure = true
}

// allParams returns every pane.read call's raw request params, in call
// order.
func (p *scriptedPane) allParams() []json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]json.RawMessage(nil), p.params...)
}

func (p *scriptedPane) serve(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		var resp any
		switch req.Method {
		case "pane.read":
			p.mu.Lock()
			p.params = append(p.params, req.Params)
			fail := p.failNextRead
			p.failNextRead = false
			p.mu.Unlock()
			if fail {
				resp = errorFrame(req.ID, "pane_not_found", "pane w1:p1 not found")
			} else {
				text := p.next()
				resp = map[string]any{"id": req.ID, "result": map[string]any{
					"read": map[string]any{
						"type": "pane_read", "text": text,
						// What the real server sends, and what a baseline must
						// not be taken from. See paneReadRevision.
						"revision": paneReadRevision,
					},
				}}
				p.mu.Lock()
				if p.stageFailure && !p.staged {
					// The screen changed again the moment this read
					// returned, and the read for that change will fail.
					p.staged = true
					p.revision++
					p.failNextRead = true
				}
				p.mu.Unlock()
			}
		case "pane.get":
			p.mu.Lock()
			p.gets++
			rev := p.revision
			bad := p.getErr
			p.mu.Unlock()
			if bad {
				resp = errorFrame(req.ID, "pane_not_found", "pane w1:p1 not found")
			} else {
				resp = map[string]any{"id": req.ID, "result": paneGetEnvelope("w1:p1", rev)}
			}
		default:
			resp = map[string]any{"id": req.ID, "result": map[string]any{"type": "ok"}}
		}
		b, _ := json.Marshal(resp)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

// collect runs StreamPaneOutput until it has seen want payloads or ctx expires.
func collect(t *testing.T, sock string, want int, budget time.Duration) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = herdr.New(sock).StreamPaneOutput(ctx, "w1:p1", 200, func(s string) {
			mu.Lock()
			got = append(got, s)
			n := len(got)
			mu.Unlock()
			if n >= want {
				cancel()
			}
		})
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

// collectANSI is collect but drives StreamPaneOutputANSI instead of
// StreamPaneOutput — the suppression and delivery contracts below must hold
// on the ANSI path too, and it is a genuinely separate method, not a detail
// only visible by reading the source.
func collectANSI(t *testing.T, sock string, want int, budget time.Duration) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = herdr.New(sock).StreamPaneOutputANSI(ctx, "w1:p1", 200, func(s string) {
			mu.Lock()
			got = append(got, s)
			n := len(got)
			mu.Unlock()
			if n >= want {
				cancel()
			}
		})
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

// collectWith is collect with the client exposed, so a test can shorten the
// poll interval instead of waiting 300ms per tick.
func collectWith(t *testing.T, sock string, want int, budget time.Duration, tune func(*herdr.Client)) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	c := herdr.New(sock)
	tune(c)

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.StreamPaneOutput(ctx, "w1:p1", 200, func(s string) {
			mu.Lock()
			got = append(got, s)
			n := len(got)
			mu.Unlock()
			if n >= want {
				cancel()
			}
		})
	}()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

// observeFor streams for a fixed window and returns what arrived. It never
// cancels early, because the assertion it serves is about calls the loop did
// NOT make: a collector that stops at the first payload leaves the loop no
// ticks in which to poll, so every call count it reports is zero whether the
// gate exists or not.
func observeFor(t *testing.T, sock string, window time.Duration, tune func(*herdr.Client)) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()

	c := herdr.New(sock)
	tune(c)

	var mu sync.Mutex
	var got []string
	_ = c.StreamPaneOutput(ctx, "w1:p1", 200, func(s string) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), got...)
}

// The defect this fixes: the loop re-read the whole screen every tick whether
// or not anything changed. Measured live against herdr 0.9.0 (protocol 22) on
// an idle agent pane at the dashboard's 800-line ANSI window, the ungated loop
// made 29 reads in 9 seconds — 11,080,514 bytes, ~1.2 MiB/s per watched pane.
// Gated, the same window and the same delivered screen cost 2 reads and
// 787,775 bytes (~85 KiB/s). GetPane's doc comment carries the rate model
// behind those figures.
//
// 500ms at 20ms a tick is ~25 ticks. With the gate: one priming read, one
// belt read at tick 20, and a pane.get on every other tick. Without it: ~25
// reads. The read count is the whole assertion.
func TestStreamSkipsTheReadWhileRevisionIsUnchanged(t *testing.T) {
	p := newScriptedPane(t, "one", "two")

	got := observeFor(t, p.path, 500*time.Millisecond, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) == 0 || got[0] != "one" {
		t.Fatalf("first payload = %v, want it to start with [one]", got)
	}
	reads, gets := p.readCount(), p.getCount()
	if gets < 10 {
		t.Fatalf("pane.get called %d times in ~25 ticks — the loop is not asking whether anything changed", gets)
	}
	if reads > 3 {
		t.Fatalf("pane.read called %d times against an unchanging revision (pane.get: %d) — the gate is absent", reads, gets)
	}
}

// The gate must not cost liveness: when the revision moves, the new screen
// arrives on the next tick. The bump lands at ~tick 3, well before the belt
// at tick 20, so a pass here is the gate working and not the belt covering —
// and the tick count below is what enforces that second half.
func TestStreamDeliversWhenRevisionMoves(t *testing.T) {
	p := newScriptedPane(t, "one", "two")
	go func() {
		time.Sleep(60 * time.Millisecond)
		p.bumpRevision()
	}()
	got := collectWith(t, p.path, 2, 3*time.Second, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) < 2 || got[1] != "two" {
		t.Fatalf("payloads = %v, want the second screen after the revision moved", got)
	}
	// Delivery by the belt needs 20 ticks, so 15 is a margin on both sides:
	// the gate delivers in ~3 and the belt cannot deliver in fewer than 20.
	// The read count cannot carry this assertion — a belt delivery costs only
	// two reads, the priming one and the belt's own, because every tick
	// between them skips before it reaches the read. Ticks are the thing that
	// differs, so ticks are what this counts.
	if gets := p.getCount(); gets >= 15 {
		t.Fatalf("pane.get called %d times before the second screen arrived — the belt (tick 20) "+
			"delivered it, not the gate", gets)
	}
}

// The belt. This test never bumps the revision, so the gate alone would keep
// the second screen invisible forever. If a real herdr ever fails to move a
// revision on a real change, this is what recovers the view.
func TestStreamForcesAFullReadPeriodicallyEvenWithAFrozenRevision(t *testing.T) {
	p := newScriptedPane(t, "one", "two")
	got := collectWith(t, p.path, 2, 3*time.Second, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) < 2 || got[1] != "two" {
		t.Fatalf("payloads = %v — a frozen revision must still recover via the periodic full read", got)
	}
}

// The gate must not reach a pane herdr is not tracking.
//
// A live herdr leaves a plain shell pane's revision at 0 through real screen
// changes — measured in a throwaway session, three writes grew the visible text
// 616 -> 747 -> 878 bytes (the later of two runs, the same figures the loop's
// comment cites) while pane.get answered 0 every time. Skipping on an
// unchanged zero would hand every shell pane to the belt: one read every 20
// ticks, i.e. a six-second-stale terminal at the shipped 300ms cadence, for
// most panes in a session.
//
// The read count is the assertion. Delivery alone would not distinguish the
// two behaviours: the belt delivers the second screen either way inside this
// window, just 400ms later instead of 40ms.
func TestStreamKeepsReadingAPaneWithNoTrackedRevision(t *testing.T) {
	p := newScriptedPane(t, "one", "two")
	p.untrackedRevision()

	got := observeFor(t, p.path, 500*time.Millisecond, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) < 2 || got[1] != "two" {
		t.Fatalf("payloads = %v, want the second screen", got)
	}
	if reads := p.readCount(); reads < 10 {
		t.Fatalf("pane.read called %d times in ~25 ticks — a pane with no tracked revision must read "+
			"every tick, or its view waits for the belt", reads)
	}
}

// A failing pane.get must not freeze the view.
//
// The cheap question is the only thing standing between the loop and the read,
// so treating its failure as "nothing to do" stops the pane updating for as
// long as the failure lasts — and the belt cannot rescue it, because the error
// returns before the belt is consulted. A pane that has genuinely gone fails
// both calls and the tick is lost either way; a pane whose pane.get alone is
// failing is exactly the case this covers.
func TestStreamKeepsReadingWhenPaneGetFails(t *testing.T) {
	p := newScriptedPane(t, "one", "two")
	p.failGets()

	got := collectWith(t, p.path, 2, 2*time.Second, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) < 2 || got[1] != "two" {
		t.Fatalf("payloads = %v, want the second screen even though pane.get fails", got)
	}
}

// A read that failed must not advance the baseline.
//
// lastRev means "the revision whose screen we actually got". Committing the
// revision before the read delivered leaves the next tick comparing against a
// revision it never fetched: it skips, and the screen that arrived at that
// revision stays invisible until the belt fires — up to 20 ticks, ~6s at the
// shipped cadence, where a baseline committed late recovers on the very next
// tick.
//
// The fixture stages exactly that sequence (see stageReadFailure): a screen is
// served, the screen then changes, and the read for the change fails. The
// second screen can only arrive here if the tick after the failure read again.
//
// 250ms is 12 ticks at 20ms — inside the belt's 20, so nothing this test
// observes can have come from the belt.
func TestStreamRereadsAfterAFailedRead(t *testing.T) {
	p := newScriptedPane(t, "one", "two")
	p.stageReadFailure()

	got := observeFor(t, p.path, 250*time.Millisecond, func(c *herdr.Client) {
		c.PollInterval = 20 * time.Millisecond
	})
	if len(got) < 2 || got[1] != "two" {
		t.Fatalf("payloads = %v — the screen that arrived while the read was failing must still "+
			"reach the caller, which needs the next tick to read again", got)
	}
}

// The whole point of the feature: a watcher keeps receiving as the screen keeps
// changing. A once-only mechanism delivers the first screen and then nothing,
// which is exactly the bug this replaces.
func TestStreamPaneOutputKeepsDeliveringAsScreenChanges(t *testing.T) {
	p := newScriptedPane(t, "screen one", "screen two", "screen three")

	// A real herdr moves the pane's revision whenever its screen changes, and
	// the poll loop now takes that at its word: a fixture that advanced its
	// screens without moving the revision would be scripting a server that
	// does not exist, and the loop would rightly skip every read until the
	// periodic full read. So the screen churn goes on the revision too.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				p.bumpRevision()
			}
		}
	}()

	got := collect(t, p.path, 3, 5*time.Second)

	if len(got) < 3 {
		t.Fatalf("delivered %d screens (%q), want 3 — a stream that stops after "+
			"the first change is the one-shot bug", len(got), got)
	}
	for i, want := range []string{"screen one", "screen two", "screen three"} {
		if got[i] != want {
			t.Errorf("screen %d = %q, want %q", i, got[i], want)
		}
	}
}

// An unchanged screen must not be pushed. On a phone over a tunnel, re-sending
// an identical terminal three times a second is the difference between an idle
// dashboard and a data bill.
func TestStreamPaneOutputSuppressesUnchangedScreens(t *testing.T) {
	p := newScriptedPane(t, "same", "same", "same", "same", "same")

	got := collect(t, p.path, 2, 2500*time.Millisecond)

	if len(got) != 1 {
		t.Errorf("delivered %d payloads for an unchanging screen (%q), want 1", len(got), got)
	}
	// Count every poll, not just reads. The loop now asks pane.get whether
	// the revision moved and only reads the screen when it did, so counting
	// reads alone would report a loop that ticked eight times as one that
	// barely polled, and this non-vacuity check would fail for the wrong
	// reason. The assertion above — exactly one payload — is unchanged.
	if polls := p.readCount() + p.getCount(); polls < 3 {
		t.Errorf("polled only %d times in the window — the test is not exercising "+
			"the suppression path", polls)
	}
}

// Cancelling must stop the poll loop, or every closed browser tab leaks a
// goroutine reading a socket forever.
func TestStreamPaneOutputStopsOnCancel(t *testing.T) {
	p := newScriptedPane(t, "a", "b", "c")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- herdr.New(p.path).StreamPaneOutput(ctx, "w1:p1", 200, func(string) {}) }()

	time.Sleep(700 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("cancel returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamPaneOutput did not return within 2s of cancel")
	}

	before := p.readCount()
	time.Sleep(700 * time.Millisecond)
	if after := p.readCount(); after != before {
		t.Errorf("kept polling after cancel: %d -> %d reads", before, after)
	}
}

// StreamPaneOutputANSI's poll must carry the same format:"ansi" /
// strip_ansi:false contract as the one-shot ReadPaneANSI read below — it is
// the path the web dashboard actually receives its live updates from, so a
// poll that silently reverted to stripped plain text would still look
// correct from the one-shot read alone.
func TestStreamPaneOutputANSIRequestsANSIFormatWithoutStripping(t *testing.T) {
	p := newScriptedPane(t, "screen one", "screen two")

	_ = collectANSI(t, p.path, 2, 3*time.Second)

	params := p.allParams()
	if len(params) == 0 {
		t.Fatal("StreamPaneOutputANSI made no pane.read calls")
	}
	for i, raw := range params {
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("call %d params: %v", i, err)
		}
		if got["format"] != "ansi" {
			t.Errorf("call %d format = %v, want %q", i, got["format"], "ansi")
		}
		if got["strip_ansi"] != false {
			t.Errorf("call %d strip_ansi = %v, want false — sending true would strip "+
				"the very escapes the web terminal needs to render colour", i, got["strip_ansi"])
		}
	}
}

// An unchanged ANSI-styled screen must be suppressed exactly like an
// unchanged plain-text one (TestStreamPaneOutputSuppressesUnchangedScreens
// above): with escapes included the payload is roughly 2.4x larger and
// churns more in practice, which is exactly the shape of change that could
// silently defeat a suppression check exercised only against plain ASCII.
func TestStreamPaneOutputANSISuppressesUnchangedScreens(t *testing.T) {
	screen := "\x1b[1;31mBOLD RED\x1b[0m plain \x1b[38;5;208morange\x1b[0m\r\n"
	p := newScriptedPane(t, screen, screen, screen, screen, screen)

	got := collectANSI(t, p.path, 2, 2500*time.Millisecond)

	if len(got) != 1 {
		t.Errorf("delivered %d payloads for an unchanging ANSI screen (%q), want 1", len(got), got)
	}
	// Count every poll, not just reads. The loop now asks pane.get whether
	// the revision moved and only reads the screen when it did, so counting
	// reads alone would report a loop that ticked eight times as one that
	// barely polled, and this non-vacuity check would fail for the wrong
	// reason. The assertion above — exactly one payload — is unchanged.
	if polls := p.readCount() + p.getCount(); polls < 3 {
		t.Errorf("polled only %d times in the window — the test is not exercising "+
			"the suppression path", polls)
	}
}

// The rename wire field is "label", not "name".
//
// This is a regression test for a bug that shipped green: the params were
// written as {"pane_id":…,"name":…} by analogy with the other calls. Against a
// real herdr, tab.rename and workspace.rename reject that with `missing field
// label`, while pane.rename — where label is optional — returns success and
// renames nothing. A test asserting "the call was made" would have passed on
// all three; only asserting the actual field catches it.
func TestRenameSendsLabelNotName(t *testing.T) {
	f := newFakeHerdr(t, "")
	c := f.client(t)
	ctx := context.Background()

	if err := c.RenamePane(ctx, "w1:p1", "alpha"); err != nil {
		t.Fatalf("rename pane: %v", err)
	}
	if err := c.RenameTab(ctx, "w1:t1", "beta"); err != nil {
		t.Fatalf("rename tab: %v", err)
	}
	if err := c.RenameWorkspace(ctx, "w1", "gamma"); err != nil {
		t.Fatalf("rename workspace: %v", err)
	}

	want := []struct {
		method string
		idKey  string
		id     string
		label  string
	}{
		{"pane.rename", "pane_id", "w1:p1", "alpha"},
		{"tab.rename", "tab_id", "w1:t1", "beta"},
		{"workspace.rename", "workspace_id", "w1", "gamma"},
	}
	if len(f.calls) != len(want) {
		t.Fatalf("made %d calls, want %d", len(f.calls), len(want))
	}
	for i, w := range want {
		got := f.calls[i]
		if got.Method != w.method {
			t.Errorf("call %d method = %q, want %q", i, got.Method, w.method)
		}
		var params map[string]any
		if err := json.Unmarshal(got.Params, &params); err != nil {
			t.Fatalf("call %d params: %v", i, err)
		}
		if _, bad := params["name"]; bad {
			t.Errorf("%s sent a \"name\" field — herdr wants \"label\"", w.method)
		}
		if params["label"] != w.label {
			t.Errorf("%s label = %v, want %q", w.method, params["label"], w.label)
		}
		if params[w.idKey] != w.id {
			t.Errorf("%s %s = %v, want %q", w.method, w.idKey, params[w.idKey], w.id)
		}
	}
}

// The web dashboard needs ANSI/SGR escapes preserved so it can render colour
// and style, so its read path must ask herdr for format:"ansi" AND turn off
// strip_ansi — asking for the escapes while leaving strip_ansi at its
// plain-text default would just have herdr strip them right back out. A test
// asserting only "ReadPaneANSI made a pane.read call" would pass even with
// strip_ansi hardcoded to true, exactly the class of wire bug
// TestRenameSendsLabelNotName above exists to catch.
func TestReadPaneANSIRequestsANSIFormatWithoutStripping(t *testing.T) {
	f := newFakeHerdr(t, "")
	c := f.client(t)
	ctx := context.Background()

	if _, err := c.ReadPaneANSI(ctx, "w1:p1", 50); err != nil {
		t.Fatalf("ReadPaneANSI: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("made %d calls, want 1", len(f.calls))
	}
	if f.calls[0].Method != "pane.read" {
		t.Fatalf("method = %q, want pane.read", f.calls[0].Method)
	}

	var params map[string]any
	if err := json.Unmarshal(f.calls[0].Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["format"] != "ansi" {
		t.Errorf("format = %v, want %q", params["format"], "ansi")
	}
	if params["strip_ansi"] != false {
		t.Errorf("strip_ansi = %v, want false", params["strip_ansi"])
	}
	if params["pane_id"] != "w1:p1" {
		t.Errorf("pane_id = %v, want w1:p1", params["pane_id"])
	}
}

// ReadPane — the plain-text path every other caller uses (the desktop panel,
// via AgentsService.Read) — must keep asking herdr to strip escapes exactly
// as it did before this field existed. Regressing this would recolour every
// desktop terminal by accident.
func TestReadPaneStillStripsANSI(t *testing.T) {
	f := newFakeHerdr(t, "")
	c := f.client(t)
	ctx := context.Background()

	if _, err := c.ReadPane(ctx, "w1:p1", 50); err != nil {
		t.Fatalf("ReadPane: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("made %d calls, want 1", len(f.calls))
	}

	var params map[string]any
	if err := json.Unmarshal(f.calls[0].Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["format"] != "text" {
		t.Errorf("format = %v, want %q", params["format"], "text")
	}
	if params["strip_ansi"] != true {
		t.Errorf("strip_ansi = %v, want true", params["strip_ansi"])
	}
}
