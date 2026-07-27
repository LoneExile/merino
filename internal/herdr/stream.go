package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// subscriptionStarted is the ack the server sends before streaming begins.
const subscriptionStarted = "subscription_started"

// ErrSubscribeRejected means the server refused the subscription set, usually
// because a per-pane kind was sent without a pane_id.
var ErrSubscribeRejected = errors.New("herdr: subscription rejected")

// Subscribe opens a streaming connection and delivers events to onEvent until
// ctx is cancelled or the connection ends.
//
// Unlike ordinary calls this connection is long-lived: the connection *is* the
// subscription, and the subscription set is fixed at subscribe time. To change
// what you are subscribed to, close this connection and open a new one — see
// app.SubManager, which does exactly that.
//
// onReady, if non-nil, is invoked once after the server acks and before any
// event is delivered. Use it to reconcile state, since events that occurred
// while the connection was down were not buffered anywhere.
//
// onEvent is called synchronously from the read loop. Keep it cheap; hand off
// to a channel or store rather than blocking.
func (c *Client) Subscribe(
	ctx context.Context,
	subs []Subscription,
	onReady func(),
	onEvent func(Event),
) error {
	if len(subs) == 0 {
		return fmt.Errorf("%w: empty subscription set", ErrSubscribeRejected)
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Unblock the read loop promptly when the caller cancels.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	req := request{ID: c.nextID(), Method: "events.subscribe", Params: subscribeParams{Subscriptions: subs}}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("herdr: write events.subscribe: %w", err)
	}

	sc := newLineReader(conn)

	// First line is the ack.
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return fmt.Errorf("herdr: read subscribe ack: %w", err)
		}
		return fmt.Errorf("herdr: connection closed before subscribe ack")
	}
	var ack response
	if err := json.Unmarshal(sc.Bytes(), &ack); err != nil {
		return fmt.Errorf("herdr: decode subscribe ack: %w", err)
	}
	if ack.Error != nil {
		return fmt.Errorf("%w: %s", ErrSubscribeRejected, ack.Error.Message)
	}
	var started struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(ack.Result, &started); err != nil || started.Type != subscriptionStarted {
		return fmt.Errorf("%w: unexpected ack %s", ErrSubscribeRejected, string(ack.Result))
	}

	if onReady != nil {
		onReady()
	}

	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			// A malformed line should not tear down a healthy stream.
			continue
		}
		onEvent(ev)
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("herdr: stream read: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil // server closed the stream cleanly
}

// StreamOptions configures a self-healing subscription.
type StreamOptions struct {
	// Subscriptions is the fixed set to (re)subscribe on every attempt.
	Subscriptions []Subscription
	// OnEvent receives every decoded event.
	OnEvent func(Event)
	// OnReady fires after each successful subscribe, including reconnects.
	// Reconcile authoritative state here: the gap between a drop and a
	// resubscribe is unbuffered and its events are lost.
	OnReady func()
	// OnError observes each attempt failure. Optional.
	OnError func(error)
	// MinBackoff defaults to 250ms, MaxBackoff to 30s.
	MinBackoff, MaxBackoff time.Duration
}

// Stream maintains a subscription across server restarts, reconnecting with
// capped exponential backoff until ctx is cancelled.
//
// herdr restarts routinely (`herdr update`, `server.stop`), so treat a dropped
// stream as normal operation rather than a fatal error.
func (c *Client) Stream(ctx context.Context, opt StreamOptions) {
	minB := opt.MinBackoff
	if minB <= 0 {
		minB = 250 * time.Millisecond
	}
	maxB := opt.MaxBackoff
	if maxB <= 0 {
		maxB = 30 * time.Second
	}

	attempt := 0
	for ctx.Err() == nil {
		err := c.Subscribe(ctx, opt.Subscriptions, opt.OnReady, opt.OnEvent)
		if ctx.Err() != nil {
			return
		}
		if err != nil && opt.OnError != nil {
			opt.OnError(err)
		}

		// A rejected subscription set will never succeed on retry; backing off
		// forever would busy-loop on a programming error. Surface and stop.
		if errors.Is(err, ErrSubscribeRejected) {
			return
		}

		attempt++
		delay := time.Duration(float64(minB) * math.Pow(2, float64(attempt-1)))
		if delay > maxB {
			delay = maxB
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}
