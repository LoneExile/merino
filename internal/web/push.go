package web

// Web Push notifications: the mechanism by which a browser learns an agent
// went blocked even with the dashboard closed, via the OS notification
// centre.
//
// Dependency justification (see AGENTS.md — no new dependency without one):
// Web Push requires implementing RFC 8291 (message encryption, aes128gcm
// over an ECDH-derived key) and RFC 8292 (VAPID JWT signing over ECDSA
// P-256). Both are exactly the kind of hand-rolled crypto this project
// should not write itself — a subtle error is a silent, unauditable failure
// to deliver, not a crash. github.com/SherClockHolmes/webpush-go implements
// both correctly and is the de facto standard Go library for this (used by
// ntfy, gotify and others); nothing else in this file's dependency tree is
// new. The frontend adds no dependency at all: the service worker's push and
// notificationclick listeners are ~15 lines of standard Push API calls, far
// short of justifying vite-plugin-pwa or any push client library.
import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/LoneExile/herdr-tunnel/internal/app"
)

const (
	vapidFileName         = "vapid-keys.json"
	subscriptionsFileName = "push-subscriptions.json"

	// pushTTLSeconds bounds how long a push service holds an undelivered
	// notification for an offline device. Long enough to reach a phone that
	// was briefly unreachable; short enough that a block resolved hours ago
	// does not resurface as a notification out of nowhere.
	pushTTLSeconds = 4 * 60 * 60

	// pushVAPIDSubject is the VAPID JWT "sub" claim (RFC 8292 §2.1): a
	// contact URI a push service can use to reach the sender. RFC 8292
	// marks it optional, but an empty Options.Subscriber becomes the
	// literal string "mailto:" (see webpush-go's vapid.go), and real push
	// services do not treat that as optional — Apple's rejects it outright
	// with "BadJwtToken", and standard libraries (web-push, pywebpush)
	// refuse to send at all without a real mailto: or https: value. This
	// project has no per-operator contact configured anywhere, so the
	// project's own repository is the stable, always-valid https: URI to
	// use instead of shipping a claim known to fail against a real push
	// service.
	pushVAPIDSubject = "https://github.com/LoneExile/herdr-tunnel"
)

// pushKeys are the two subscription secrets a browser hands back from
// PushSubscription.getKey(), base64url-encoded. Named to match the wire
// contract exactly ("p256dh", "auth"), which is also the shape webpush-go's
// own Keys type expects.
type pushKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// pushSubscription is one browser's registered endpoint, persisted alongside
// the Identity that registered it. The Identity travels with the
// subscription — not looked up at send time — so a notification can be
// policy-filtered exactly as if that browser were making a live request,
// even for an identity whose session has since expired.
type pushSubscription struct {
	Endpoint string   `json:"endpoint"`
	Keys     pushKeys `json:"keys"`
	Identity Identity `json:"identity"`
}

// pushPayload is what the service worker's "push" listener parses. Title
// names the agent and project so the notification is meaningful without
// opening the app; PaneID travels in Data so a notification click can
// deep-link straight to the pane that raised it.
type pushPayload struct {
	Title string          `json:"title"`
	Body  string          `json:"body"`
	Data  pushPayloadData `json:"data"`
}

type pushPayloadData struct {
	PaneID string `json:"paneId"`
}

// vapidKeypair is the on-disk shape of the VAPID keys, both base64url —
// exactly the form webpush-go produces and the form the browser's
// applicationServerKey expects, so no re-encoding happens on either side of
// the round trip through disk.
type vapidKeypair struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

// pushManager owns the VAPID keypair and the subscription store, and is the
// seam between an agent transitioning into blocked and a browser being woken
// with the app closed.
//
// A *Server with a nil push field is push-disabled and is a completely
// normal, fully-functional state: see New and newPushManager's doc comment
// for why generation/load failures must never be fatal to the dashboard.
type pushManager struct {
	log *slog.Logger

	publicKey  string
	privateKey string

	subsPath string

	mu   sync.Mutex
	subs map[string]pushSubscription

	// send performs delivery to one subscriber. A field rather than a direct
	// webpush-go call so tests can stub the transport instead of reaching a
	// real push service; sendReal is the production implementation, wired by
	// newPushManager.
	send func(sub pushSubscription, payload []byte) (status int, err error)
}

// newPushManager loads or creates the VAPID keypair and subscription store
// under dir. Any error here means push stays disabled for this run — the
// caller (New) logs it and leaves Server.push nil — because a notifications
// feature must never be able to take the whole dashboard down by failing to
// initialise.
func newPushManager(dir string, log *slog.Logger) (*pushManager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("push: create %s: %w", dir, err)
	}
	pub, priv, err := loadOrCreateVAPIDKeys(filepath.Join(dir, vapidFileName))
	if err != nil {
		return nil, err
	}
	subs, err := loadSubscriptions(filepath.Join(dir, subscriptionsFileName))
	if err != nil {
		return nil, err
	}
	pm := &pushManager{
		log:        log,
		publicKey:  pub,
		privateKey: priv,
		subsPath:   filepath.Join(dir, subscriptionsFileName),
		subs:       subs,
	}
	pm.send = pm.sendReal
	return pm, nil
}

// loadOrCreateVAPIDKeys reads the keypair at path, generating and persisting
// a fresh one on first run. The private key lets anyone forge push messages
// as this server to every subscribed browser, so the file is created 0600.
func loadOrCreateVAPIDKeys(path string) (public, private string, err error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var kp vapidKeypair
		if jsonErr := json.Unmarshal(data, &kp); jsonErr != nil {
			return "", "", fmt.Errorf("push: parse VAPID keys: %w", jsonErr)
		}
		if kp.PublicKey == "" || kp.PrivateKey == "" {
			return "", "", errors.New("push: VAPID key file is missing a key")
		}
		return kp.PublicKey, kp.PrivateKey, nil

	case errors.Is(err, os.ErrNotExist):
		priv, pub, genErr := webpush.GenerateVAPIDKeys()
		if genErr != nil {
			return "", "", fmt.Errorf("push: generate VAPID keys: %w", genErr)
		}
		encoded, marshalErr := json.Marshal(vapidKeypair{PublicKey: pub, PrivateKey: priv})
		if marshalErr != nil {
			return "", "", fmt.Errorf("push: encode VAPID keys: %w", marshalErr)
		}
		if writeErr := atomicWriteFile(path, encoded, 0o600); writeErr != nil {
			return "", "", fmt.Errorf("push: persist VAPID keys: %w", writeErr)
		}
		return pub, priv, nil

	default:
		return "", "", fmt.Errorf("push: read VAPID keys: %w", err)
	}
}

// loadSubscriptions reads the persisted subscription list. A missing file is
// the expected first-run state, not an error.
func loadSubscriptions(path string) (map[string]pushSubscription, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]pushSubscription{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("push: read subscriptions: %w", err)
	}
	var list []pushSubscription
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("push: parse subscriptions: %w", err)
	}
	subs := make(map[string]pushSubscription, len(list))
	for _, sub := range list {
		subs[sub.Endpoint] = sub
	}
	return subs, nil
}

// atomicWriteFile writes data to a temp file in the same directory as path
// and renames it into place, so a crash or concurrent read can never observe
// a half-written file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// snapshotLocked returns the current subscriptions as a slice for
// marshalling. Caller must hold mu.
func (pm *pushManager) snapshotLocked() []pushSubscription {
	list := make([]pushSubscription, 0, len(pm.subs))
	for _, sub := range pm.subs {
		list = append(list, sub)
	}
	return list
}

func (pm *pushManager) persist(list []pushSubscription) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("push: encode subscriptions: %w", err)
	}
	// 0600: subscriptions carry the identity that registered them.
	return atomicWriteFile(pm.subsPath, data, 0o600)
}

// subscribe records sub, keyed by endpoint. A second subscribe for an
// endpoint already known — the browser re-registering, or simply retrying —
// overwrites the previous entry rather than erroring or duplicating it.
func (pm *pushManager) subscribe(sub pushSubscription) error {
	pm.mu.Lock()
	pm.subs[sub.Endpoint] = sub
	list := pm.snapshotLocked()
	pm.mu.Unlock()
	return pm.persist(list)
}

// unsubscribe removes an endpoint. A no-op, not an error, if it was never
// known — both an API caller giving up on a subscription it lost track of
// and notifyBlocked pruning a rejected endpoint hit this same path.
func (pm *pushManager) unsubscribe(endpoint string) error {
	pm.mu.Lock()
	delete(pm.subs, endpoint)
	list := pm.snapshotLocked()
	pm.mu.Unlock()
	return pm.persist(list)
}

// notifyBlocked sends a push notification to every subscription permitted to
// view a, which has just transitioned into the blocked status.
//
// This is deliberately dumb about *when* to fire: edge-vs-level is entirely
// Store's job (see Store.SetOnBlocked) — by the time a became-blocked event
// reaches here it is real, so this method only has to answer "who may see
// this pane, and can I still reach them".
//
// Synchronous and network-bound; callers on a hot path (AgentsService.OnBlocked,
// via Server.NotifyBlocked) must run it off their own goroutine.
func (pm *pushManager) notifyBlocked(a app.Agent, policy Policy) {
	title := a.Agent
	if title == "" {
		title = "An agent"
	}
	title += " needs you"
	if a.Project != "" {
		title += " — " + a.Project
	}
	payload, err := json.Marshal(pushPayload{
		Title: title,
		Body:  "Blocked, waiting for your input.",
		Data:  pushPayloadData{PaneID: a.PaneID},
	})
	if err != nil {
		pm.log.Warn("push: encode notification", "err", err)
		return
	}

	pm.mu.Lock()
	targets := pm.snapshotLocked()
	pm.mu.Unlock()

	var gone []string
	for _, sub := range targets {
		// The whole point: a push about a pane this identity cannot view is
		// a data leak, subscription or no subscription.
		if !policy.CanView(sub.Identity, a) {
			continue
		}
		status, sendErr := pm.send(sub, payload)
		switch {
		case sendErr != nil:
			pm.log.Warn("push: send failed", "err", sendErr)
		case status == http.StatusNotFound || status == http.StatusGone:
			// The push service itself says this endpoint is permanently
			// gone (uninstalled, key rotated, subscription revoked
			// browser-side without us hearing about it) — prune it so every
			// future blocked transition doesn't pay for a delivery that can
			// never succeed.
			gone = append(gone, sub.Endpoint)
		case status >= 300:
			pm.log.Warn("push: send rejected", "status", status)
		}
	}
	for _, endpoint := range gone {
		if err := pm.unsubscribe(endpoint); err != nil {
			pm.log.Warn("push: persist pruned subscription", "err", err)
			continue
		}
		pm.log.Info("push: pruned expired subscription")
	}
}

// sendReal is the production transport: an actual RFC 8291/8292 push send.
// Kept as a plain method (rather than the only implementation) purely so
// tests can point pushManager.send at a stub instead of reaching a real push
// service.
func (pm *pushManager) sendReal(sub pushSubscription, payload []byte) (int, error) {
	resp, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.Keys.P256dh,
			Auth:   sub.Keys.Auth,
		},
	}, &webpush.Options{
		VAPIDPublicKey:  pm.publicKey,
		VAPIDPrivateKey: pm.privateKey,
		Subscriber:      pushVAPIDSubject,
		TTL:             pushTTLSeconds,
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// --- Server wiring ---

// NotifyBlocked sends a push notification for a became-blocked transition.
// This is the hook AgentsService.OnBlocked is wired to from main.go.
//
// A no-op when push is disabled (s.push == nil), so callers may wire it
// unconditionally. Hands off to its own goroutine: pushing to a possibly
// slow or unreachable push service must never stall the status-observation
// goroutine that triggered this call.
func (s *Server) NotifyBlocked(a app.Agent) {
	if s.push == nil {
		return
	}
	go s.push.notifyBlocked(a, s.cfg.Policy)
}

// mountPush registers the push routes. Called only when push initialised
// successfully — see New — so, like Writer, the absence of push is the
// absence of these routes rather than a runtime refusal.
func (s *Server) mountPush(mux *http.ServeMux) {
	mux.Handle("GET /api/push/key", s.authed(s.handlePushKey))
	mux.Handle("POST /api/push/subscribe", s.authed(s.handlePushSubscribe))
	mux.Handle("POST /api/push/unsubscribe", s.authed(s.handlePushUnsubscribe))
}

func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request, _ Identity) {
	writeJSON(w, http.StatusOK, map[string]string{"key": s.push.publicKey})
}

type pushSubscribeBody struct {
	Endpoint string   `json:"endpoint"`
	Keys     pushKeys `json:"keys"`
}

// handlePushSubscribe records a browser's push subscription against the
// signed-in identity. Idempotent on endpoint (see pushManager.subscribe) and
// audited exactly like a pane write: this is a state change by an
// authenticated user, and "who subscribed what" is the useful record.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request, id Identity) {
	body, ok := decode[pushSubscribeBody](w, r)
	if !ok {
		return
	}
	if body.Endpoint == "" || body.Keys.P256dh == "" || body.Keys.Auth == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed subscription"})
		return
	}
	sub := pushSubscription{Endpoint: body.Endpoint, Keys: body.Keys, Identity: id}
	if err := s.push.subscribe(sub); err != nil {
		s.log.Warn("push subscribe failed", "err", err)
		s.audit(r, id, "push_subscribe", "", body.Endpoint, false, err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save subscription"})
		return
	}
	s.audit(r, id, "push_subscribe", "", body.Endpoint, true, "")
	w.WriteHeader(http.StatusNoContent)
}

type pushUnsubscribeBody struct {
	Endpoint string `json:"endpoint"`
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request, id Identity) {
	body, ok := decode[pushUnsubscribeBody](w, r)
	if !ok {
		return
	}
	if err := s.push.unsubscribe(body.Endpoint); err != nil {
		s.log.Warn("push unsubscribe failed", "err", err)
		s.audit(r, id, "push_unsubscribe", "", body.Endpoint, false, err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove subscription"})
		return
	}
	s.audit(r, id, "push_unsubscribe", "", body.Endpoint, true, "")
	w.WriteHeader(http.StatusNoContent)
}
