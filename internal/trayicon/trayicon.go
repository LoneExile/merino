// Package trayicon renders the sheep mascot that reflects herd activity in
// the menubar tray icon: it jumps while any agent is blocked (it needs the
// operator), walks while agents are working, and stands still otherwise.
//
// The package is split in two: this file embeds and validates the frame
// assets, and animator.go turns herd counts into a sequence of icon bytes
// via a small, Wails-independent state machine.
package trayicon

import (
	"embed"
	"fmt"
)

//go:embed frames/*.png
var frameFS embed.FS

// FrameSet holds the decoded PNG bytes for every animation frame, embedded
// at build time from frames/*.png. Idle is a single static frame; Walk and
// Jump are ordered, looping cycles.
type FrameSet struct {
	Idle []byte
	Walk [][]byte
	Jump [][]byte
}

// Frames is populated once at package init from the embedded PNGs.
//
// It panics on a missing or empty frame rather than degrading quietly. A
// go:embed pattern that matched nothing (or a frame that failed to read)
// would otherwise leave the tray with a zero-length icon, which makes the
// mascot silently vanish from the menu bar at runtime — a much worse
// failure than refusing to start.
var Frames = mustLoadFrames()

func mustLoadFrames() FrameSet {
	return FrameSet{
		Idle: mustFrame("idle.png"),
		Walk: mustFrameCycle("walk-%d.png", 6),
		Jump: mustFrameCycle("jump-%d.png", 5),
	}
}

func mustFrame(name string) []byte {
	b, err := frameFS.ReadFile("frames/" + name)
	if err != nil {
		panic(fmt.Sprintf("trayicon: embedded frame %q missing: %v", name, err))
	}
	if len(b) == 0 {
		panic(fmt.Sprintf("trayicon: embedded frame %q is empty", name))
	}
	return b
}

func mustFrameCycle(pattern string, n int) [][]byte {
	cycle := make([][]byte, n)
	for i := range n {
		cycle[i] = mustFrame(fmt.Sprintf(pattern, i))
	}
	return cycle
}
