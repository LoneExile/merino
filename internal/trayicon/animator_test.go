package trayicon

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// fastIvl is the tick interval used by tests: short enough for the suite to
// run quickly, long enough to stay well clear of scheduler jitter under
// -race.
const fastIvl = 4 * time.Millisecond

// pollTimeout bounds how long tests wait for calls that are expected to
// happen. Generous relative to fastIvl so scheduling delays under -race
// never cause a false failure; a genuine bug (e.g. a stuck loop) still
// fails the test, just after this ceiling instead of instantly.
const pollTimeout = 2 * time.Second

// recorder collects every icon set on the Animator, safe for concurrent use
// since set is invoked from the Animator's own goroutine while the test
// goroutine reads it.
type recorder struct {
	mu    sync.Mutex
	calls [][]byte
}

func (r *recorder) set(icon []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, icon)
}

func (r *recorder) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newTestAnimator(r *recorder) *Animator {
	return newAnimator(r.set, fastIvl, fastIvl)
}

// inCycle reports whether icon is byte-identical to one of cycle's frames.
func inCycle(icon []byte, cycle [][]byte) bool {
	for _, f := range cycle {
		if bytes.Equal(icon, f) {
			return true
		}
	}
	return false
}

// waitForCallCount polls until r has at least n calls, or fails the test
// after pollTimeout. Polling (rather than a fixed sleep) is what keeps
// these tests non-flaky under -race, where goroutine scheduling latency is
// unpredictable but a working Animator always gets there eventually.
func waitForCallCount(t *testing.T, r *recorder, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for {
		calls := r.snapshot()
		if len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d calls, got %d", n, len(calls))
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNewShowsIdleFrameImmediately(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)
	defer a.Stop()

	calls := r.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d calls right after New, want 1", len(calls))
	}
	if !bytes.Equal(calls[0], Frames.Idle) {
		t.Errorf("initial frame is not the idle frame")
	}
}

// TestIdleStopsAnimationEntirely is the battery-bug guard: an idle herd
// must not leave a ticker running that repeatedly resets the same frame.
func TestIdleStopsAnimationEntirely(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)
	defer a.Stop()

	// Re-affirm idle explicitly; a genuinely idle state must still result
	// in exactly the one set call New already made — no ticker, no churn.
	a.Update(app.Counts{Total: 0})

	// Give a spurious ticker plenty of chances to fire if one existed.
	time.Sleep(30 * fastIvl)

	if got := r.len(); got != 1 {
		t.Fatalf("got %d set calls while idle, want exactly 1 (idle must stop the ticker)", got)
	}
	calls := r.snapshot()
	if !bytes.Equal(calls[0], Frames.Idle) {
		t.Errorf("the one idle call is not the idle frame")
	}
}

func TestWorkingPlaysWalkCycle(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)

	a.Update(app.Counts{Total: 2, Working: 2})

	// idle (from New) + walk[0] + at least 2 further ticks.
	waitForCallCount(t, r, 4)
	a.Stop() // freeze the recording deterministically

	calls := r.snapshot()
	// calls[0] is always the idle frame New shows synchronously; the walk
	// cycle starts at calls[1].
	if !bytes.Equal(calls[0], Frames.Idle) {
		t.Fatalf("call 0 is not the initial idle frame")
	}
	walkCalls := calls[1:]
	if !bytes.Equal(walkCalls[0], Frames.Walk[0]) {
		t.Errorf("first walk frame is not Walk[0]")
	}
	for i, c := range walkCalls {
		if !inCycle(c, Frames.Walk) {
			t.Errorf("call %d is not a walk frame", i)
		}
		if inCycle(c, Frames.Jump) {
			t.Errorf("call %d unexpectedly matches a jump frame", i)
		}
	}
}

func TestBlockedPlaysJumpCycle(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)

	a.Update(app.Counts{Total: 1, Blocked: 1})

	waitForCallCount(t, r, 4)
	a.Stop()

	calls := r.snapshot()
	jumpCalls := calls[1:] // calls[0] is New's synchronous idle frame
	if !bytes.Equal(jumpCalls[0], Frames.Jump[0]) {
		t.Errorf("first jump frame is not Jump[0]")
	}
	for i, c := range jumpCalls {
		if !inCycle(c, Frames.Jump) {
			t.Errorf("call %d is not a jump frame", i)
		}
		if inCycle(c, Frames.Walk) {
			t.Errorf("call %d unexpectedly matches a walk frame", i)
		}
	}
}

// TestBlockedTakesPrecedenceOverWorking proves a herd with both blocked and
// working agents jumps, never walks — blocked needs the operator, so it
// must win regardless of how many agents are merely busy.
func TestBlockedTakesPrecedenceOverWorking(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)

	a.Update(app.Counts{Total: 5, Working: 4, Blocked: 1})

	waitForCallCount(t, r, 3)
	a.Stop()

	calls := r.snapshot()
	jumpCalls := calls[1:] // calls[0] is New's synchronous idle frame
	for i, c := range jumpCalls {
		if inCycle(c, Frames.Walk) {
			t.Errorf("call %d matches a walk frame; blocked should have taken precedence", i)
		}
	}
	if !bytes.Equal(jumpCalls[0], Frames.Jump[0]) {
		t.Errorf("first frame after idle is not Jump[0]")
	}
}

// TestStateChangeResetsFrameIndex proves switching cycles always starts the
// new cycle at its first frame, never mid-cycle.
func TestStateChangeResetsFrameIndex(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)
	defer a.Stop()

	a.Update(app.Counts{Total: 1, Working: 1})
	// Let the walk cycle advance well past frame 0 before switching.
	waitForCallCount(t, r, 3)
	before := r.len()

	a.Update(app.Counts{Total: 1, Blocked: 1})
	calls := waitForCallCount(t, r, before+1)

	if !bytes.Equal(calls[before], Frames.Jump[0]) {
		t.Errorf("first call after the state change is not Jump[0]; frame index was not reset")
	}
}

// TestStopHaltsFurtherCalls is the other half of the battery-bug guard:
// once Stop returns, nothing further should ever call set again. Stop is
// synchronous (it waits for the animation goroutine to exit), so the
// assertion needs no sleep of its own to be reliable.
func TestStopHaltsFurtherCalls(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)

	a.Update(app.Counts{Total: 1, Working: 1})
	waitForCallCount(t, r, 3)

	a.Stop()
	afterStop := r.len()

	// Update after Stop must be inert.
	a.Update(app.Counts{Total: 1, Blocked: 1})
	time.Sleep(20 * fastIvl)

	if got := r.len(); got != afterStop {
		t.Fatalf("got %d calls after Stop, want %d (no further calls once stopped)", got, afterStop)
	}

	// Stop must also be idempotent.
	a.Stop()
}

// TestUpdateConcurrentWithStop exercises the concurrency contract under
// -race: Update and Stop may be called from any goroutine, concurrently
// with each other, without a data race or panic.
func TestUpdateConcurrentWithStop(t *testing.T) {
	r := &recorder{}
	a := newTestAnimator(r)

	var wg sync.WaitGroup
	counts := []app.Counts{
		{Total: 1, Working: 1},
		{Total: 1, Blocked: 1},
		{},
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, c := range counts {
				a.Update(c)
			}
		}()
	}
	wg.Wait()
	a.Stop()
}
