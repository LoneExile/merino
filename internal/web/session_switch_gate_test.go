package web

import "testing"

type stubSwitcher struct{}

func (stubSwitcher) SwitchSession(string) error { return nil }

func TestSessionSwitchAllowedFromConfig(t *testing.T) {
	s := &Server{cfg: Config{Switcher: stubSwitcher{}}, switchOn: true}
	if !s.SessionSwitchAllowed() {
		t.Fatal("want allowed")
	}
	if err := s.SetSessionSwitch(false); err != nil {
		t.Fatal(err)
	}
	if s.SessionSwitchAllowed() {
		t.Fatal("want off")
	}
	if err := s.SetSessionSwitch(true); err != nil {
		t.Fatal(err)
	}
	if !s.SessionSwitchAllowed() {
		t.Fatal("want on again")
	}
}
