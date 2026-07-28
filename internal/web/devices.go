package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"encoding/hex"
)

// Device is a paired browser/phone that redeemed a QR (or future OAuth link).
//
// Devices are the durable identity unit for the public-release ladder: a
// stolen phone session is one row to revoke, not the master password.
type Device struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Provider  string     `json:"provider"`
	Roles     []string   `json:"roles"`
	CreatedAt time.Time  `json:"createdAt"`
	LastSeen  time.Time  `json:"lastSeen"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// DeviceStore persists paired devices next to other local secrets (VAPID, audit).
type DeviceStore struct {
	mu   sync.Mutex
	path string
	byID map[string]*Device
}

// OpenDeviceStore loads or creates devices.json under dir (0700 parent).
func OpenDeviceStore(dir string) (*DeviceStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("devices: empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("devices: mkdir: %w", err)
	}
	path := filepath.Join(dir, "devices.json")
	s := &DeviceStore{path: path, byID: make(map[string]*Device)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("devices: read: %w", err)
	}
	var list []Device
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("devices: parse: %w", err)
	}
	for i := range list {
		d := list[i]
		cp := d
		s.byID[d.ID] = &cp
	}
	return s, nil
}

func (s *DeviceStore) persistLocked() error {
	list := make([]Device, 0, len(s.byID))
	for _, d := range s.byID {
		list = append(list, *d)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Mint creates an active device and returns the identity to put on the session.
func (s *DeviceStore) Mint(name, provider string, roles []string) (Device, Identity, error) {
	if s == nil {
		return Device{}, Identity{}, fmt.Errorf("devices: nil store")
	}
	if name == "" {
		name = "Phone"
	}
	if provider == "" {
		provider = "pairing"
	}
	if len(roles) == 0 {
		// First-ship ladder: paired phones get full operator access (same as
		// today's QR→master-user behaviour). Narrower scopes can land later
		// without changing the store shape.
		roles = []string{"view", "control"}
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return Device{}, Identity{}, fmt.Errorf("devices: id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := time.Now().UTC()
	d := &Device{
		ID:        id,
		Name:      name,
		Provider:  provider,
		Roles:     append([]string(nil), roles...),
		CreatedAt: now,
		LastSeen:  now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[id] = d
	if err := s.persistLocked(); err != nil {
		delete(s.byID, id)
		return Device{}, Identity{}, err
	}
	return *d, d.Identity(), nil
}

// Identity builds the session identity for this device.
func (d Device) Identity() Identity {
	return Identity{
		Subject:  "device:" + d.ID,
		Name:     d.Name,
		Provider: d.Provider,
		Roles:    append([]string(nil), d.Roles...),
	}
}

// SubjectID strips the device: prefix; empty if not a device subject.
func SubjectID(subject string) string {
	return strings.TrimPrefix(subject, "device:")
}

// IsDeviceSubject reports whether subject names a paired device.
func IsDeviceSubject(subject string) bool {
	return strings.HasPrefix(subject, "device:")
}

// Active reports whether the device subject may still use the dashboard.
func (s *DeviceStore) Active(subject string) bool {
	if s == nil || !IsDeviceSubject(subject) {
		return true // non-device identities are not governed here
	}
	id := SubjectID(subject)
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	if !ok || d.RevokedAt != nil {
		return false
	}
	d.LastSeen = time.Now().UTC()
	// Best-effort touch; ignore persist errors so a full disk cannot lock out
	// every request mid-flight.
	_ = s.persistLocked()
	return true
}

// List returns a snapshot of all devices (including revoked).
func (s *DeviceStore) List() []Device {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.byID))
	for _, d := range s.byID {
		out = append(out, *d)
	}
	return out
}

// Revoke marks a device revoked. Returns false if unknown.
func (s *DeviceStore) Revoke(id string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("devices: nil store")
	}
	id = strings.TrimPrefix(id, "device:")
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	if !ok {
		return false, nil
	}
	if d.RevokedAt == nil {
		now := time.Now().UTC()
		d.RevokedAt = &now
		if err := s.persistLocked(); err != nil {
			return false, err
		}
	}
	return true, nil
}

// RevokeAll revokes every active non-desktop (device) grant. Returns count newly revoked.
func (s *DeviceStore) RevokeAll() (int, error) {
	if s == nil {
		return 0, fmt.Errorf("devices: nil store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	now := time.Now().UTC()
	for _, d := range s.byID {
		if d.RevokedAt == nil {
			t := now
			d.RevokedAt = &t
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.persistLocked(); err != nil {
		return 0, err
	}
	return n, nil
}

// CountActive returns how many non-revoked devices exist.
func (s *DeviceStore) CountActive() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.byID {
		if d.RevokedAt == nil {
			n++
		}
	}
	return n
}
