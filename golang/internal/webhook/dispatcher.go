package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultTimeout      = 10 * time.Second
	defaultMaxAttempts  = 3
	defaultBackoffBase  = 1 * time.Second
	defaultMaxBodyBytes = 1 << 20 // 1 MiB — a webhook receiver's response body is never expected to be large; this only bounds what Dispatcher reads before discarding it.

	signatureHeader = "X-Webhook-Signature"
)

// DeliveryStatus records the outcome of one delivery attempt to one
// Subscriber.
type DeliveryStatus struct {
	SubscriberURL string
	Attempt       int
	StatusCode    int // zero when Err is a transport-level failure (no response was received at all)
	Err           error
	Delivered     bool // true only for a 2xx response
	Timestamp     time.Time
}

// Dispatcher delivers Events to Subscribers over HTTP, retrying a failed
// attempt with exponential backoff.
type Dispatcher struct {
	// Client performs each HTTP request. Nil uses a default client with
	// defaultTimeout.
	Client *http.Client

	// MaxAttempts bounds how many times one Subscriber delivery is tried
	// before giving up. <= 0 uses defaultMaxAttempts.
	MaxAttempts int

	// BackoffBase is the delay before the second attempt; each
	// subsequent attempt doubles it (1x, 2x, 4x, ...). <= 0 uses
	// defaultBackoffBase.
	BackoffBase time.Duration

	// Sleep performs the backoff wait between attempts. Nil uses
	// time.Sleep. Tests override this to run deterministically and
	// instantly instead of waiting on real wall-clock time.
	Sleep func(time.Duration)

	// MaxBodyBytes bounds how much of a subscriber's response body this
	// reads before discarding it. <= 0 uses defaultMaxBodyBytes.
	MaxBodyBytes int64
}

func (d *Dispatcher) httpClient() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (d *Dispatcher) maxAttempts() int {
	if d.MaxAttempts > 0 {
		return d.MaxAttempts
	}
	return defaultMaxAttempts
}

func (d *Dispatcher) backoffBase() time.Duration {
	if d.BackoffBase > 0 {
		return d.BackoffBase
	}
	return defaultBackoffBase
}

func (d *Dispatcher) sleep() func(time.Duration) {
	if d.Sleep != nil {
		return d.Sleep
	}
	return time.Sleep
}

func (d *Dispatcher) maxBodyBytes() int64 {
	if d.MaxBodyBytes > 0 {
		return d.MaxBodyBytes
	}
	return defaultMaxBodyBytes
}

// Fire delivers event to every Subscriber in registry that wants
// event.Type, concurrently, and returns every attempt's DeliveryStatus
// from every subscriber (not just the final attempt per subscriber) —
// the full history is what "per-subscriber delivery status" means for
// this package. A subscriber that never accepts the delivery contributes
// MaxAttempts entries, all with Delivered == false; one that succeeds on
// attempt 2 contributes 2 entries, the second with Delivered == true.
func (d *Dispatcher) Fire(ctx context.Context, registry *Registry, event Event) []DeliveryStatus {
	subs := registry.Subscribers(event.Type)
	if len(subs) == 0 {
		return nil
	}

	body, err := json.Marshal(event)
	if err != nil {
		// Every Event is this package's own struct with a json-marshalable
		// Payload by contract (documented on Event.Payload); a caller
		// violating that (e.g. a Payload containing a channel or func) gets
		// one failed status per subscriber rather than a panic or a
		// swallowed error.
		now := time.Now()
		statuses := make([]DeliveryStatus, len(subs))
		for i, sub := range subs {
			statuses[i] = DeliveryStatus{SubscriberURL: sub.URL, Attempt: 1, Err: fmt.Errorf("webhook: encode event: %w", err), Timestamp: now}
		}
		return statuses
	}

	results := make(chan []DeliveryStatus, len(subs))
	for _, sub := range subs {
		go func(sub Subscriber) {
			results <- d.deliver(ctx, sub, body)
		}(sub)
	}

	var all []DeliveryStatus
	for range subs {
		all = append(all, (<-results)...)
	}
	return all
}

// deliver attempts to POST body to sub.URL up to d.maxAttempts() times,
// with exponential backoff between attempts, stopping early on the first
// 2xx response or on ctx cancellation. The cancellation check only runs
// between attempts, never mid-request — a ctx already canceled before
// deliver is even called still results in exactly one real attempt (which
// then fails via the HTTP client's own context check inside attempt) before
// deliver returns; it does not skip delivery entirely.
func (d *Dispatcher) deliver(ctx context.Context, sub Subscriber, body []byte) []DeliveryStatus {
	var statuses []DeliveryStatus
	client := d.httpClient()
	maxAttempts := d.maxAttempts()
	backoff := d.backoffBase()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status := d.attempt(ctx, client, sub, body, attempt)
		statuses = append(statuses, status)
		if status.Delivered {
			return statuses
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return statuses
		default:
		}
		d.sleep()(backoff)
		backoff *= 2
	}
	return statuses
}

func (d *Dispatcher) attempt(ctx context.Context, client *http.Client, sub Subscriber, body []byte, attemptNum int) DeliveryStatus {
	now := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		return DeliveryStatus{SubscriberURL: sub.URL, Attempt: attemptNum, Err: fmt.Errorf("webhook: build request: %w", err), Timestamp: now}
	}
	req.Header.Set("Content-Type", "application/json")
	if sub.Secret != "" {
		req.Header.Set(signatureHeader, sign(sub.Secret, body))
	}

	resp, err := client.Do(req)
	if err != nil {
		return DeliveryStatus{SubscriberURL: sub.URL, Attempt: attemptNum, Err: fmt.Errorf("webhook: deliver: %w", err), Timestamp: now}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.CopyN(io.Discard, resp.Body, d.maxBodyBytes())

	delivered := resp.StatusCode >= 200 && resp.StatusCode < 300
	status := DeliveryStatus{SubscriberURL: sub.URL, Attempt: attemptNum, StatusCode: resp.StatusCode, Delivered: delivered, Timestamp: now}
	if !delivered {
		status.Err = fmt.Errorf("webhook: deliver: unexpected status %d", resp.StatusCode)
	}
	return status
}

// sign returns the hex-encoded HMAC-SHA256 of body keyed by secret.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
