package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestRegistry(urls ...string) *Registry {
	r := NewRegistry()
	for _, u := range urls {
		r.Register(Subscriber{URL: u})
	}
	return r
}

func TestDispatcher_Fire_SucceedsFirstAttempt(t *testing.T) {
	// Arrange
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := newTestRegistry(srv.URL)

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if !statuses[0].Delivered || statuses[0].Attempt != 1 {
		t.Errorf("statuses[0] = %+v, want Delivered=true Attempt=1", statuses[0])
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server received %d requests, want 1 (no retry after success)", got)
	}
}

func TestDispatcher_Fire_RetriesThenSucceeds(t *testing.T) {
	// Arrange
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var sleeps []time.Duration
	d := &Dispatcher{
		MaxAttempts: 5,
		BackoffBase: time.Millisecond,
		Sleep:       func(delay time.Duration) { sleeps = append(sleeps, delay) },
	}
	registry := newTestRegistry(srv.URL)

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 3 {
		t.Fatalf("len(statuses) = %d, want 3 (2 failures + 1 success)", len(statuses))
	}
	if statuses[0].Delivered || statuses[1].Delivered {
		t.Errorf("statuses[0:2] = %+v, want both Delivered=false", statuses[:2])
	}
	if !statuses[2].Delivered {
		t.Errorf("statuses[2] = %+v, want Delivered=true", statuses[2])
	}
	if len(sleeps) != 2 || sleeps[1] != 2*sleeps[0] {
		t.Errorf("sleeps = %v, want 2 entries with the second double the first (exponential backoff)", sleeps)
	}
}

func TestDispatcher_Fire_DefaultClientAndSleepAndMaxBodyBytes(t *testing.T) {
	// Arrange: exercises the zero-value defaults (nil Client, nil Sleep,
	// zero MaxBodyBytes) — every other test explicitly sets these fields.
	// BackoffBase is kept tiny so the real time.Sleep this exercises
	// doesn't slow the test suite down.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{MaxAttempts: 2, BackoffBase: time.Millisecond}
	registry := newTestRegistry(srv.URL)

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 2 || !statuses[1].Delivered {
		t.Errorf("statuses = %+v, want 2 entries with the second Delivered=true", statuses)
	}
}

func TestDispatcher_Fire_ExhaustsMaxAttemptsAndGivesUp(t *testing.T) {
	// Arrange
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := &Dispatcher{MaxAttempts: 3, Sleep: func(time.Duration) {}}
	registry := newTestRegistry(srv.URL)

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 3 {
		t.Fatalf("len(statuses) = %d, want 3 (MaxAttempts)", len(statuses))
	}
	for _, s := range statuses {
		if s.Delivered {
			t.Errorf("status %+v has Delivered=true, want every attempt to have failed", s)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("server received %d requests, want exactly MaxAttempts=3", got)
	}
}

func TestDispatcher_Fire_TransportFailureIsRecordedWithNoStatusCode(t *testing.T) {
	// Arrange: a URL nothing listens on.
	d := &Dispatcher{MaxAttempts: 1, Sleep: func(time.Duration) {}}
	registry := newTestRegistry("http://127.0.0.1:1/unreachable")

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if statuses[0].Delivered || statuses[0].Err == nil || statuses[0].StatusCode != 0 {
		t.Errorf("statuses[0] = %+v, want Delivered=false, a non-nil Err, and StatusCode=0", statuses[0])
	}
}

func TestDispatcher_Fire_NoMatchingSubscribersReturnsNil(t *testing.T) {
	// Arrange
	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := NewRegistry()
	registry.Register(Subscriber{URL: "http://sub.example/x", Events: []EventType{EventScanComplete}})

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if statuses != nil {
		t.Errorf("Fire() = %v, want nil (no subscriber wants this event type)", statuses)
	}
}

func TestDispatcher_Fire_DeliversToMultipleSubscribersIndependently(t *testing.T) {
	// Arrange
	var okCalls, failCalls int32
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&okCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&failCalls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	d := &Dispatcher{MaxAttempts: 2, Sleep: func(time.Duration) {}}
	registry := newTestRegistry(okServer.URL, failServer.URL)

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) != 1+2 { // ok subscriber: 1 attempt; fail subscriber: MaxAttempts=2 attempts
		t.Fatalf("len(statuses) = %d, want 3", len(statuses))
	}
	if atomic.LoadInt32(&okCalls) != 1 {
		t.Errorf("okServer received %d calls, want 1", okCalls)
	}
	if atomic.LoadInt32(&failCalls) != 2 {
		t.Errorf("failServer received %d calls, want 2", failCalls)
	}
}

func TestDispatcher_Fire_SignsPayloadWhenSecretIsSet(t *testing.T) {
	// Arrange
	var gotSignature string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get(signatureHeader)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := NewRegistry()
	registry.Register(Subscriber{URL: srv.URL, Secret: "top-secret"})

	// Act
	d.Fire(context.Background(), registry, Event{Type: EventRelocate, Key: "a.txt"})

	// Assert
	mac := hmac.New(sha256.New, []byte("top-secret"))
	mac.Write(gotBody)
	want := hex.EncodeToString(mac.Sum(nil))
	if gotSignature != want {
		t.Errorf("X-Webhook-Signature = %q, want %q", gotSignature, want)
	}
}

func TestDispatcher_Fire_NoSignatureHeaderWithoutSecret(t *testing.T) {
	// Arrange
	var gotSignature string
	sawHeader := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get(signatureHeader)
		sawHeader = gotSignature != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := newTestRegistry(srv.URL)

	// Act
	d.Fire(context.Background(), registry, Event{Type: EventRelocate})

	// Assert
	if sawHeader {
		t.Errorf("X-Webhook-Signature = %q, want no header sent when Subscriber.Secret is empty", gotSignature)
	}
}

func TestDispatcher_Fire_ContextCancellationStopsRetriesEarly(t *testing.T) {
	// Arrange
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		MaxAttempts: 5,
		Sleep: func(time.Duration) {
			cancel() // cancel between the 1st and 2nd attempt
		},
	}
	registry := newTestRegistry(srv.URL)

	// Act
	statuses := d.Fire(ctx, registry, Event{Type: EventRelocate})

	// Assert
	if len(statuses) >= 5 {
		t.Errorf("len(statuses) = %d, want fewer than MaxAttempts (context canceled mid-retry)", len(statuses))
	}
}

func TestDispatcher_Fire_UnmarshalableEventPayloadProducesFailedStatusNoPanic(t *testing.T) {
	// Arrange
	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := newTestRegistry("http://sub.example/x")

	// Act
	statuses := d.Fire(context.Background(), registry, Event{Type: EventRelocate, Payload: make(chan int)})

	// Assert
	if len(statuses) != 1 || statuses[0].Err == nil || statuses[0].Delivered {
		t.Errorf("statuses = %+v, want a single failed status with a non-nil Err", statuses)
	}
}

func TestDispatcher_Fire_EventBodyIsValidJSON(t *testing.T) {
	// Arrange
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{Sleep: func(time.Duration) {}}
	registry := newTestRegistry(srv.URL)

	// Act
	d.Fire(context.Background(), registry, Event{Type: EventRelocate, Bucket: "b", Key: "k"})

	// Assert
	var decoded map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, gotBody)
	}
	if decoded["type"] != string(EventRelocate) || decoded["bucket"] != "b" || decoded["key"] != "k" {
		t.Errorf("decoded body = %+v, want type=relocate bucket=b key=k", decoded)
	}
}
