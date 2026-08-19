package webhook

import "testing"

func TestRegistry_Subscribers_EmptyEventsMatchesEverything(t *testing.T) {
	// Arrange
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/all"})

	// Act / Assert
	for _, et := range []EventType{EventUpload, EventRelocate, EventPolicyDecision, EventScanComplete} {
		got := r.Subscribers(et)
		if len(got) != 1 || got[0].URL != "http://sub.example/all" {
			t.Errorf("Subscribers(%v) = %v, want the wildcard subscriber", et, got)
		}
	}
}

func TestRegistry_Subscribers_FiltersByEventType(t *testing.T) {
	// Arrange
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/relocate-only", Events: []EventType{EventRelocate}})
	r.Register(Subscriber{URL: "http://sub.example/scan-only", Events: []EventType{EventScanComplete}})

	// Act
	relocateSubs := r.Subscribers(EventRelocate)
	scanSubs := r.Subscribers(EventScanComplete)
	uploadSubs := r.Subscribers(EventUpload)

	// Assert
	if len(relocateSubs) != 1 || relocateSubs[0].URL != "http://sub.example/relocate-only" {
		t.Errorf("Subscribers(relocate) = %v, want only the relocate subscriber", relocateSubs)
	}
	if len(scanSubs) != 1 || scanSubs[0].URL != "http://sub.example/scan-only" {
		t.Errorf("Subscribers(scan_complete) = %v, want only the scan subscriber", scanSubs)
	}
	if len(uploadSubs) != 0 {
		t.Errorf("Subscribers(upload) = %v, want none (neither subscriber opted in)", uploadSubs)
	}
}

func TestRegistry_Subscribers_MultipleEventTypesOnOneSubscriber(t *testing.T) {
	// Arrange
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/mixed", Events: []EventType{EventRelocate, EventPolicyDecision}})

	// Act / Assert
	if got := r.Subscribers(EventRelocate); len(got) != 1 {
		t.Errorf("Subscribers(relocate) = %v, want 1", got)
	}
	if got := r.Subscribers(EventPolicyDecision); len(got) != 1 {
		t.Errorf("Subscribers(policy_decision) = %v, want 1", got)
	}
	if got := r.Subscribers(EventScanComplete); len(got) != 0 {
		t.Errorf("Subscribers(scan_complete) = %v, want 0", got)
	}
}

func TestRegistry_Register_DuplicateURLAddsIndependentEntries(t *testing.T) {
	// Arrange
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/x", Events: []EventType{EventRelocate}})
	r.Register(Subscriber{URL: "http://sub.example/x", Events: []EventType{EventScanComplete}})

	// Act
	got := r.Subscribers(EventRelocate)

	// Assert
	if len(got) != 1 {
		t.Fatalf("Subscribers(relocate) = %v, want exactly 1 (only the first registration matches)", got)
	}
	all := append(r.Subscribers(EventRelocate), r.Subscribers(EventScanComplete)...)
	if len(all) != 2 {
		t.Errorf("total matched entries across both event types = %d, want 2 (both registrations kept independently)", len(all))
	}
}

func TestRegistry_Subscribers_ReturnedEventsDoNotAliasRegistryStorage(t *testing.T) {
	// Arrange: regression test — mutating a returned Subscriber's Events
	// slice in place must not corrupt the Registry's own stored filter for
	// that subscriber (a read-path variant of the same aliasing bug class
	// Register's own doc comment already guards against on the write path).
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/x", Events: []EventType{EventRelocate}})

	// Act
	subs := r.Subscribers(EventRelocate)
	subs[0].Events[0] = EventUpload

	// Assert
	if got := r.Subscribers(EventRelocate); len(got) != 1 {
		t.Errorf("Subscribers(relocate) = %v after mutating a previously-returned Subscriber's Events, want it still matched", got)
	}
	if got := r.Subscribers(EventUpload); len(got) != 0 {
		t.Errorf("Subscribers(upload) = %v after mutating a previously-returned Subscriber's Events, want it NOT matched", got)
	}
}

func TestRegistry_Register_DoesNotAliasTheEventsSlice(t *testing.T) {
	// Arrange: regression test — mutating the caller's Events slice in
	// place after Register must not change what the Registry stored, the
	// same aliasing pitfall internal/authz's constructors were fixed for
	// earlier this session.
	events := []EventType{EventRelocate}
	r := NewRegistry()
	r.Register(Subscriber{URL: "http://sub.example/x", Events: events})

	// Act
	events[0] = EventUpload

	// Assert
	if got := r.Subscribers(EventRelocate); len(got) != 1 {
		t.Errorf("Subscribers(relocate) = %v after mutating the caller's original Events slice, want it still matched", got)
	}
	if got := r.Subscribers(EventUpload); len(got) != 0 {
		t.Errorf("Subscribers(upload) = %v after mutating the caller's original Events slice, want it NOT matched", got)
	}
}
