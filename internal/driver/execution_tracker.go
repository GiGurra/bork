package driver

import (
	"crypto/sha256"
	"errors"
)

type executionInvocation struct {
	owner *executionTracker
	index int
}
type executionInvocationState struct {
	expected        [sha256.Size]byte
	bound, complete bool
	receipt         *executionReceipt
}
type executionTracker struct {
	invocations []executionInvocationState
	overflow    bool
}

func (tracker *executionTracker) begin(candidate *executionCandidate) executionInvocation {
	if len(tracker.invocations) >= executionInvocationMaxCount {
		tracker.overflow = true
		return executionInvocation{}
	}
	token := executionInvocation{owner: tracker, index: len(tracker.invocations)}
	tracker.invocations = append(tracker.invocations, executionInvocationState{expected: candidate.identity(), bound: candidate != nil})
	return token
}
func (tracker *executionTracker) pending(token executionInvocation) (*executionInvocationState, error) {
	if token.owner != tracker || token.index < 0 || token.index >= len(tracker.invocations) {
		return nil, errors.New("foreign execution invocation")
	}
	state := &tracker.invocations[token.index]
	if state.complete {
		return nil, errors.New("completed execution invocation")
	}
	return state, nil
}
func (tracker *executionTracker) certify(token executionInvocation, receipt *executionReceipt) (err error) {
	defer func() {
		if err != nil {
			tracker.overflow = true
		}
	}()
	state, err := tracker.pending(token)
	if err != nil {
		return err
	}
	if !state.bound || receipt == nil || receipt.inputKey != state.expected {
		return errors.New("execution invocation input mismatch")
	}
	owned, err := receipt.clone()
	if err != nil {
		return err
	}
	// Identity accounting alone must never certify execution. Until the collector
	// supplies current closure evidence, every receipt remains ineligible.
	if !owned.current() {
		return errors.New(string(executionClosureUnavailable))
	}
	state.receipt = owned
	state.complete = true
	return nil
}
func (tracker *executionTracker) decline(token executionInvocation, _ executionDecline) {
	state, err := tracker.pending(token)
	if err != nil {
		tracker.overflow = true
		return
	}
	state.complete = true
}
func (tracker *executionTracker) receipts() ([]*executionReceipt, bool) {
	if tracker.overflow {
		return nil, false
	}
	// Validate all evidence before allocating returned receipt copies.
	total := 0
	for _, state := range tracker.invocations {
		if !state.complete || state.receipt == nil || !state.receipt.validIdentity() || !state.receipt.current() {
			return nil, false
		}
		size := len(state.receipt.input) + len(state.receipt.result)
		if size > executionIdentityMaxBytes-total {
			return nil, false
		}
		total += size
	}
	out := make([]*executionReceipt, 0, len(tracker.invocations))
	for _, state := range tracker.invocations {
		owned, err := state.receipt.clone()
		if err != nil {
			return nil, false
		}
		out = append(out, owned)
	}
	return out, true
}
