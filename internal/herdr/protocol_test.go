package herdr_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/LoneExile/merino/internal/herdr"
)

// mixedCaps is the capability object herdr 0.9 sends: values are not all
// flags, and endpoint_protocol_generation carries a number.
var mixedCaps = map[string]any{"health_check": true, "endpoint_protocol_generation": 1}

// pingSocket is a herdr socket that answers ping with one protocol number and
// nothing else. scriptedPane (paneoutput_test.go) cannot stand in: its default
// branch answers every unknown method with {"type":"ok"}, which decodes as
// protocol 0 and would make every assertion here pass for the wrong reason.
func pingSocket(t *testing.T, protocol int) string {
	t.Helper()
	return pingSocketCaps(t, protocol, mixedCaps)
}

// pingSocketCaps is pingSocket with the advertised capability object under the
// test's control, so a case can pin a wire shape that no longer matches the
// newest server — herdr 0.8.2's all-boolean capabilities, for instance.
func pingSocketCaps(t *testing.T, protocol int, caps map[string]any) string {
	t.Helper()
	// macOS caps AF_UNIX paths at ~104 bytes and t.TempDir() embeds the whole
	// test name, which overflows it here; the short "sp" prefix mirrors
	// newScriptedPane (paneoutput_test.go), which has the same constraint.
	dir, err := os.MkdirTemp("", "sp")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	path := filepath.Join(dir, "p.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.RemoveAll(dir) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					var req struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
						return
					}
					b, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{
						"type": "pong", "version": "test", "protocol": protocol,
						"capabilities": caps,
					}})
					if _, err := conn.Write(append(b, '\n')); err != nil {
						return
					}
				}
			}()
		}
	}()
	return path
}

// Every version in the set must connect. A single-version pin cannot express
// a fleet: one machine on 0.8.2 and another on 0.9.1 is a supported herdr
// configuration, and herdr itself negotiates rather than requiring equality.
//
// The list is a literal, not herdr.AcceptedProtocols: looping the value under
// test would drop a version's case the moment someone removed it from the set,
// leaving this test green over the exact regression it exists to catch.
// TestAcceptedProtocolsPinsTheSupportedRange pins the set's contents.
func TestCheckCompatibleAcceptsEverySupportedProtocol(t *testing.T) {
	for _, v := range []int{20, 22} {
		r, err := herdr.New(pingSocket(t, v)).CheckCompatible(context.Background())
		if err != nil {
			t.Fatalf("protocol %d must be accepted: %v", v, err)
		}
		if r.Protocol != v {
			t.Fatalf("ping reported protocol %d, want %d", r.Protocol, v)
		}
	}
}

// The floor: herdr 0.8.2 advertises only boolean capabilities, and an install
// running it must keep working whatever the newer servers do. Guarding this
// with the 0.9-shaped fixture alone proves nothing — herdr 0.8.2 is the one
// version this client may never stop speaking, and its ping payload is a
// different shape from its successors'.
func TestCheckCompatibleAcceptsHerdr082BooleanCapabilities(t *testing.T) {
	caps := map[string]any{"live_handoff": true, "surface_interest": true, "health_check": true}
	r, err := herdr.New(pingSocketCaps(t, 20, caps)).CheckCompatible(context.Background())
	if err != nil {
		t.Fatalf("herdr 0.8.2 (protocol 20) must stay accepted: %v", err)
	}
	for k, want := range caps {
		got, ok := r.Capabilities[k].(bool)
		if !ok || got != want {
			t.Fatalf("capabilities[%q] = %v (bool: %t), want %v — the 0.8.2 ping no longer decodes",
				k, r.Capabilities[k], ok, want)
		}
	}
}

// An unknown protocol must fail loudly AND say what would have worked: the
// operator's next action is to align versions, and an error that names only
// the server's number does not tell them which target to hit.
func TestCheckCompatibleRefusesUnknownProtocolAndNamesTheSet(t *testing.T) {
	_, err := herdr.New(pingSocket(t, 21)).CheckCompatible(context.Background())
	if !errors.Is(err, herdr.ErrProtocolMismatch) {
		t.Fatalf("want ErrProtocolMismatch, got %v", err)
	}
	for _, v := range herdr.AcceptedProtocols {
		if !strings.Contains(err.Error(), strconv.Itoa(v)) {
			t.Fatalf("error must name accepted protocol %d: %s", v, err)
		}
	}
}

// Pins the maintainer's decision, not an incidental default: this set decides
// which herdr versions every install may run. Same reason
// internal/app/agentkinds_pin_test.go pins its list.
func TestAcceptedProtocolsPinsTheSupportedRange(t *testing.T) {
	want := []int{20, 22} // 20 = herdr 0.8.2, 22 = herdr 0.9.1
	if !slices.Equal(herdr.AcceptedProtocols, want) {
		t.Fatalf("accepted set is %v, want %v — moving this moves the herdr floor or ceiling for every user", herdr.AcceptedProtocols, want)
	}
}
