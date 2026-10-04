package driver

import "testing"

func TestExecutionTrackerRejectsForeignDuplicateUnboundAndMismatched(t *testing.T) {
	candidate := testExecutionIdentity(t, "actual query")
	for _, kind := range []string{"foreign", "duplicate", "unbound", "mismatched", "unresolved", "declined"} {
		t.Run(kind, func(t *testing.T) {
			tracker := &executionTracker{}
			token := tracker.begin(candidate)
			switch kind {
			case "foreign":
				foreign := (&executionTracker{}).begin(candidate)
				if err := tracker.certify(foreign, testExecutionReceipt(candidate, "true")); err == nil {
					t.Fatal("accepted foreign token")
				}
			case "duplicate":
				tracker.decline(token, "ineligible")
				if err := tracker.certify(token, testExecutionReceipt(candidate, "true")); err == nil {
					t.Fatal("accepted duplicate token")
				}
			case "unbound":
				token = tracker.begin(nil)
				if err := tracker.certify(token, testExecutionReceipt(candidate, "true")); err == nil {
					t.Fatal("accepted unbound token")
				}
			case "mismatched":
				other := testExecutionIdentity(t, "other query")
				if err := tracker.certify(token, testExecutionReceipt(other, "true")); err == nil {
					t.Fatal("accepted another query receipt")
				}
			case "declined":
				tracker.decline(token, "ineligible")
			}
			if _, ok := tracker.receipts(); ok {
				t.Fatal("invalid invocation summary qualified")
			}
		})
	}
}

func TestExecutionTrackerOwnsExpectedInputAndBoundsInvocations(t *testing.T) {
	candidate := testExecutionIdentity(t, "ordered query")
	tracker := &executionTracker{}
	token := tracker.begin(candidate)
	expected := tracker.invocations[token.index].expected
	candidate.key[0] ^= 1
	if tracker.invocations[token.index].expected != expected {
		t.Fatal("tracker aliases expected input key")
	}
	for range executionInvocationMaxCount {
		tracker.begin(nil)
	}
	if len(tracker.invocations) != executionInvocationMaxCount || !tracker.overflow {
		t.Fatal("unbounded invocation inventory")
	}
	if _, ok := tracker.receipts(); ok {
		t.Fatal("overflow qualified")
	}
	empty := &executionTracker{}
	receipts, ok := empty.receipts()
	if !ok || len(receipts) != 0 {
		t.Fatal("empty tracker claims an invocation")
	}
}
