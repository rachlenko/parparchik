// Package webhook fires event notifications — upload, relocate, a policy
// quarantine/deny decision (internal/policy, Task 28), a completed
// vulnerability scan (internal/scan, Task 25) — to configurable
// subscriber URLs, with retry-with-backoff delivery and a per-subscriber,
// per-attempt delivery status record.
//
// Security note, not a defense implemented here: a Subscriber.URL is
// caller-supplied configuration, and Dispatcher.Fire makes an outbound
// HTTP request to it. That is completely ordinary for a trusted operator
// registering their own internal or third-party endpoint — but if a
// future config-loading path ever lets an untrusted party register or
// influence a subscriber URL (e.g. a self-service webhook UI), an
// unconstrained outbound POST to a caller-supplied URL is a textbook SSRF
// vector (a malicious URL could target an internal service, a cloud
// metadata endpoint, etc.). This package does not validate, allowlist, or
// deny-list subscriber URLs — deliberately: it has no way to know which
// hosts are legitimate internal subscribers for a given deployment, and
// hard-blocking private/loopback ranges by default would break the
// completely normal case of subscribing an internal service. Whatever
// wires in a way for subscriber URLs to be registered by anyone other
// than a trusted operator with direct config access MUST add its own
// validation (allowlist, DNS-rebinding protection, etc.) before doing so.
//
// Implemented: EventType, Event, Subscriber, Registry (subscriber
// bookkeeping, filtered by event type), Dispatcher (HTTP delivery with
// exponential backoff retry, optional HMAC-SHA256 payload signing, and a
// DeliveryStatus per attempt).
//
// Not implemented (see docs/plans/ Task 33): wiring Dispatcher.Fire into
// any live code path (resolver.Relocate, internal/policy.Engine.Evaluate,
// internal/scan.Scanner) — same "standalone, tested primitive" scope
// several other packages built this session were left at, and for the
// same reason: there's no config surface yet for an operator to actually
// register a subscriber, so there's nothing real to fire events to.
package webhook

import (
	"sync"
	"time"
)

// EventType identifies what happened. A small, closed set matching what
// this service can actually observe or is explicitly planned to, not an
// open string a caller could typo.
type EventType string

const (
	// EventUpload fires when an object is newly registered in the
	// catalog. No httpapi route creates one yet (this service has no
	// upload endpoint — objects arrive in S3 externally), so nothing in
	// this codebase can raise this event today; it exists so a future
	// upload endpoint has an event to fire from day one.
	EventUpload EventType = "upload"

	// EventRelocate fires when resolver.Relocate moves a key to a
	// different bucket — the one event type with an existing live code
	// path (httpapi's POST /relocate) that could actually raise it, once
	// wired in.
	EventRelocate EventType = "relocate"

	// EventPolicyDecision fires on a internal/policy.Engine.Evaluate
	// outcome — specifically intended for a Quarantine or Deny Decision
	// (an Allow decision is the routine case; whether to also notify on
	// those is a call for whatever wires this in, not this package).
	EventPolicyDecision EventType = "policy_decision"

	// EventScanComplete fires when an internal/scan.Scanner finishes
	// scanning a package+version.
	EventScanComplete EventType = "scan_complete"
)

// Event is the notification payload delivered to a subscriber. Payload is
// opaque to this package — whatever event-specific data a caller wants to
// send (a policy.Verdict's fields, a scan.Result's findings, a
// RelocateResult) — this package only JSON-encodes and delivers it
// unchanged; it does not import internal/policy, internal/scan, or
// internal/resolver, keeping this a dependency-free leaf package.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Bucket    string    `json:"bucket,omitempty"`
	Key       string    `json:"key,omitempty"`
	Payload   any       `json:"payload,omitempty"`
}

// Subscriber is one registered destination for events.
type Subscriber struct {
	// URL is where events are POSTed. See the package doc comment's
	// security note before wiring in any way for this to be set from
	// anything other than trusted operator configuration.
	URL string

	// Events restricts delivery to these EventTypes. Empty means every
	// event type.
	Events []EventType

	// Secret, when non-empty, HMAC-SHA256-signs the JSON request body;
	// the signature is sent in the X-Webhook-Signature header as a hex
	// string, so a receiver can verify the request actually came from
	// this Dispatcher and the body wasn't tampered with in transit —
	// standard practice for webhook delivery. Empty sends no signature
	// header at all.
	Secret string
}

func (s Subscriber) wants(eventType EventType) bool {
	if len(s.Events) == 0 {
		return true
	}
	for _, e := range s.Events {
		if e == eventType {
			return true
		}
	}
	return false
}

// Registry is a concurrency-safe, mutex-guarded collection of
// Subscribers, following the same pattern as internal/catalog.Catalog and
// internal/policy.AuditLog. The zero value is not ready to use — construct
// with NewRegistry.
type Registry struct {
	mu          sync.Mutex
	subscribers []Subscriber
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds sub. Registering the same URL more than once adds a
// second, independent Subscriber entry (e.g. with different Events
// filters) rather than merging — this package doesn't treat URL as a
// unique key. sub.Events is deep-copied (not just the Subscriber struct),
// so a caller that mutates its own Events slice in place after calling
// Register — the same aliasing pitfall internal/authz's constructors were
// fixed for earlier this session — cannot change what this Registry
// already stored.
func (r *Registry) Register(sub Subscriber) {
	sub.Events = append([]EventType(nil), sub.Events...)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscribers = append(r.subscribers, sub)
}

// Subscribers returns a copy of every registered Subscriber that wants
// eventType (an empty Subscriber.Events means "every event type"), in
// registration order. Each returned Subscriber's Events is itself a
// fresh copy, not the Registry's stored slice — copying only the
// Subscriber struct here would leave its Events slice header pointing at
// the same backing array Register stored, so a caller mutating a
// returned Subscriber's Events in place (e.g. subs[0].Events[0] = ...)
// would silently corrupt this Registry's own filter for that subscriber,
// with no lock held and no call back into this package at all — the same
// aliasing bug class Register is already careful to avoid on the write
// path (see its doc comment).
func (r *Registry) Subscribers(eventType EventType) []Subscriber {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []Subscriber
	for _, sub := range r.subscribers {
		if sub.wants(eventType) {
			sub.Events = append([]EventType(nil), sub.Events...)
			result = append(result, sub)
		}
	}
	return result
}
