package trayicon

import (
	"sync"
	"time"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

// Frame cadence.
//
// walkInterval targets roughly 10fps (100ms/frame): fast enough that the
// 6-frame walk cycle reads as continuous motion rather than a slideshow of
// still poses, slow enough that it doesn't flood SetTemplateIcon calls or
// keep a laptop's CPU busier than a menu-bar icon has any business being.
//
// jumpInterval is deliberately slower — about 7fps — so the jump's 5-frame
// arc (crouch, launch, apex, descend, land) reads as a distinct gesture
// with each pose actually visible, instead of blurring into a faster, less
// legible wobble. A jump is a rarer, more attention-worthy event than a
// walk, so it earns a more deliberate pace rather than a snappier one.
const (
	walkInterval = 100 * time.Millisecond
	jumpInterval = 140 * time.Millisecond
)

// animState is which cycle (if any) the Animator is currently playing.
type animState int

const (
	stateIdle animState = iota
	stateWalk
	stateJump
)

// Animator turns herd activity into a sequence of tray icon frames. It owns
// a single background goroutine and ticker; the ticker is stopped entirely
// whenever the herd goes idle, so an idle app never wakes the CPU for icon
// churn.
//
// It is intentionally decoupled from Wails: callers supply a set callback
// that receives raw icon bytes, so the whole thing is unit-testable without
// a running application. set is always invoked from the Animator's own
// goroutine, one call at a time, in animation order.
type Animator struct {
	set func(icon []byte)

	updateCh chan animState
	stopCh   chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// New creates an Animator, synchronously shows the idle frame (the state an
// empty app.Counts would select), and starts its background loop.
func New(set func(icon []byte)) *Animator {
	return newAnimator(set, walkInterval, jumpInterval)
}

// newAnimator is the real constructor; it takes explicit intervals so tests
// can run the loop on a fast, deterministic clock instead of production
// timings.
func newAnimator(set func(icon []byte), walkIvl, jumpIvl time.Duration) *Animator {
	a := &Animator{
		set:      set,
		updateCh: make(chan animState, 1),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Shown synchronously so callers observe the idle frame immediately,
	// before New even returns, rather than racing the loop goroutine's
	// startup.
	a.set(Frames.Idle)
	go a.run(walkIvl, jumpIvl)
	return a
}

// Update recomputes the animation state from the latest herd counts. Any
// blocked agent takes priority over working ones — a herd that needs the
// operator's attention outranks one that's merely busy.
//
// Safe to call from any goroutine, including concurrently, and safe to call
// after Stop (it becomes a no-op).
func (a *Animator) Update(c app.Counts) {
	next := stateIdle
	switch {
	case c.Blocked > 0:
		next = stateJump
	case c.Working > 0:
		next = stateWalk
	}

	// Non-blocking latest-wins send: if a previous update is still pending
	// (the loop goroutine hasn't drained it yet), replace it rather than
	// blocking the caller — onCounts fires from the herd's publish path and
	// must never stall on tray bookkeeping. Only the most recent desired
	// state matters; intermediate ones are moot.
	for {
		select {
		case a.updateCh <- next:
			return
		case <-a.stopCh:
			return
		default:
		}
		select {
		case <-a.updateCh:
		default:
		}
	}
}

// Stop halts the animation loop for good: no further set calls occur after
// it returns... except for one already in flight, which is allowed to
// finish. Safe to call multiple times and from any goroutine.
func (a *Animator) Stop() {
	a.stopOnce.Do(func() { close(a.stopCh) })
	<-a.done
}

// run is the Animator's sole goroutine. It owns state, frame and the
// ticker outright, so none of them need synchronization beyond the
// channels used to talk to it.
func (a *Animator) run(walkIvl, jumpIvl time.Duration) {
	defer close(a.done)

	state := stateIdle // matches the idle frame New already set
	frame := 0
	var cycle [][]byte
	var ticker *time.Ticker
	var tick <-chan time.Time

	stopTicker := func() {
		if ticker != nil {
			ticker.Stop()
			ticker = nil
			tick = nil
		}
	}
	defer stopTicker()

	for {
		select {
		case <-a.stopCh:
			return

		case next := <-a.updateCh:
			if next == state {
				continue // already playing this cycle; don't restart it
			}
			state = next
			frame = 0
			stopTicker()

			switch state {
			case stateIdle:
				cycle = nil
				a.set(Frames.Idle)
			case stateWalk:
				cycle = Frames.Walk
			case stateJump:
				cycle = Frames.Jump
			}
			if cycle != nil {
				// Show frame 0 immediately rather than waiting a full
				// interval for the first tick, so a state change is felt
				// right away.
				a.set(cycle[frame])
				ivl := walkIvl
				if state == stateJump {
					ivl = jumpIvl
				}
				ticker = time.NewTicker(ivl)
				tick = ticker.C
			}

		case <-tick:
			frame = (frame + 1) % len(cycle)
			a.set(cycle[frame])
		}
	}
}
