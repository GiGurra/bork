package driver

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func testExecutionIdentity(t *testing.T, descriptor string) *executionCandidate {
	t.Helper()
	candidate, err := newExecutionCandidate(executionIdentityData{Namespace: sha256.Sum256([]byte("compiler")), Descriptor: []byte(descriptor), Program: []byte("program"), Output: executionOutputIdentity{Site: "main.bork:1", Type: "Int", Codec: 2, Policy: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

// Construct only identity evidence: these fixtures deliberately have no Go
// execution closure, and therefore cannot qualify through current/certify.
func testExecutionReceipt(candidate *executionCandidate, result string) *executionReceipt {
	receipt := &executionReceipt{input: bytes.Clone(candidate.input), inputKey: candidate.key, output: candidate.output, result: []byte(result)}
	receipt.valueKey = executionValueIdentity(receipt.output, receipt.result)
	receipt.key = executionDigest("receipt", receipt.inputKey[:], receipt.valueKey[:])
	return receipt
}

func TestExecutionIdentityOwnershipAndBoundaries(t *testing.T) {
	descriptor, program := []byte("ab"), []byte("c")
	data := executionIdentityData{Descriptor: descriptor, Program: program, Output: executionOutputIdentity{Site: "site", Type: "Int", Codec: 2, Policy: 1}}
	candidate, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatal(err)
	}
	key := candidate.identity()
	descriptor[0] = 'X'
	program[0] = 'Y'
	if candidate.identity() != key || bytes.Contains(candidate.input, []byte("XY")) {
		t.Fatal("candidate aliases caller data")
	}
	data.Descriptor = []byte("a")
	data.Program = []byte("bc")
	other, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatal(err)
	}
	if other.identity() == key {
		t.Fatal("field concatenation collision")
	}
	receipt := testExecutionReceipt(candidate, "42")
	different := testExecutionReceipt(candidate, "43")
	if receipt.identity() == different.identity() || receipt.inputKey != different.inputKey {
		t.Fatal("input/result identities conflated")
	}
	owned, err := receipt.clone()
	if err != nil {
		t.Fatal(err)
	}
	owned.input[0] ^= 1
	owned.result[0] ^= 1
	if !receipt.validIdentity() {
		t.Fatal("cloned receipt aliases source")
	}
	changed := *receipt
	changed.output.Type = "String"
	if changed.validIdentity() {
		t.Fatal("output type not bound to input")
	}
	data.Descriptor = make([]byte, executionIdentityMaxBytes)
	if _, err := newExecutionCandidate(data); err == nil {
		t.Fatal("accepted oversized identity")
	}
	data.Descriptor = nil
	data.Prior = make([]executionPriorIdentity, executionPriorMaxCount+1)
	if _, err := newExecutionCandidate(data); err == nil {
		t.Fatal("accepted oversized prior list")
	}
	oversized := *receipt
	oversized.result = make([]byte, (16<<20)+1)
	if _, err := oversized.clone(); err == nil {
		t.Fatal("cloned oversized result")
	}
}

func TestExecutionCandidatePriorReservesMetadataBudget(t *testing.T) {
	data := executionIdentityData{Output: executionOutputIdentity{Site: "site", Type: "Int", Codec: 2, Policy: 1}}
	base, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatal(err)
	}
	data.Descriptor = make([]byte, executionIdentityMaxBytes-len(base.input)-len(data.Output.Site)-len(data.Output.Type))
	boundary, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatalf("exact combined boundary rejected: %v", err)
	}
	if !testExecutionReceipt(boundary, "42").validIdentity() {
		t.Fatal("constructed boundary exceeds receipt budget")
	}
	data.Prior = []executionPriorIdentity{{Site: "prior", Type: "Int", Codec: 2, Policy: 1}}
	priorSize := 8 + len(data.Prior[0].Site) + 8 + len(data.Prior[0].Type) + 8 + 2*sha256.Size
	data.Descriptor = data.Descriptor[:len(data.Descriptor)-priorSize]
	withPrior, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatalf("exact prior boundary rejected: %v", err)
	}
	if !testExecutionReceipt(withPrior, "42").validIdentity() {
		t.Fatal("prior boundary exceeds receipt budget")
	}
	data.Descriptor = append(data.Descriptor, 0)
	if _, err := newExecutionCandidate(data); err == nil {
		t.Fatal("prior consumed reserved metadata budget")
	}
}

func TestExecutionReceiptBoundsMutatedMetadataBeforeHashing(t *testing.T) {
	candidate := testExecutionIdentity(t, "query")
	for _, field := range []string{"site", "type", "combined"} {
		t.Run(field, func(t *testing.T) {
			receipt := testExecutionReceipt(candidate, "true")
			switch field {
			case "site":
				receipt.output.Site = strings.Repeat("S", executionIdentityMaxBytes+1)
			case "type":
				receipt.output.Type = strings.Repeat("T", executionIdentityMaxBytes+1)
			case "combined":
				receipt.output.Site = strings.Repeat("S", executionIdentityMaxBytes/2)
				receipt.output.Type = strings.Repeat("T", executionIdentityMaxBytes/2)
			}
			// Recompute internally consistent digests so rejection specifically
			// exercises metadata bounds rather than a stale identity checksum.
			receipt.inputKey = executionInputIdentity(receipt.input, receipt.output)
			receipt.valueKey = executionValueIdentity(receipt.output, receipt.result)
			receipt.key = executionDigest("receipt", receipt.inputKey[:], receipt.valueKey[:])
			if receipt.validIdentity() {
				t.Fatal("over-budget metadata passed identity validation")
			}
			if _, err := receipt.clone(); err == nil {
				t.Fatal("cloned over-budget metadata")
			}
		})
	}
}

func TestExecutionPriorResultBindingAndOrder(t *testing.T) {
	candidate := testExecutionIdentity(t, "prior")
	receipt := testExecutionReceipt(candidate, "42")
	prior, err := receipt.prior(candidate.output.Site, candidate.output.Type, 2, 1, receipt.valueKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		site, typ     string
		codec, policy uint32
		value         [sha256.Size]byte
	}{
		{"wrong", candidate.output.Type, 2, 1, receipt.valueKey},
		{candidate.output.Site, "String", 2, 1, receipt.valueKey},
		{candidate.output.Site, candidate.output.Type, 3, 1, receipt.valueKey},
		{candidate.output.Site, candidate.output.Type, 2, 2, receipt.valueKey},
		{candidate.output.Site, candidate.output.Type, 2, 1, testExecutionReceipt(candidate, "43").valueKey},
	} {
		if _, err := receipt.prior(mutation.site, mutation.typ, mutation.codec, mutation.policy, mutation.value); err == nil {
			t.Fatal("accepted mismatched prior")
		}
	}
	second, err := testExecutionReceipt(candidate, "43").prior(candidate.output.Site, candidate.output.Type, 2, 1, testExecutionReceipt(candidate, "43").valueKey)
	if err != nil {
		t.Fatal(err)
	}
	data := executionIdentityData{Output: candidate.output, Prior: []executionPriorIdentity{prior, second}}
	firstOrder, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatal(err)
	}
	data.Prior[0], data.Prior[1] = data.Prior[1], data.Prior[0]
	secondOrder, err := newExecutionCandidate(data)
	if err != nil {
		t.Fatal(err)
	}
	if firstOrder.identity() == secondOrder.identity() {
		t.Fatal("lost prior evaluation order")
	}
}

func TestExecutionFoundationDeclinesUncertifiedClosure(t *testing.T) {
	candidate := testExecutionIdentity(t, "recipe")
	receipt := testExecutionReceipt(candidate, "42")
	if !receipt.validIdentity() {
		t.Fatal("fixture identity invalid")
	}
	if candidate.current() || receipt.current() {
		t.Fatal("identity alone certifies execution")
	}
	if _, err := candidate.certify([]byte("42")); err == nil {
		t.Fatal("certified without closure")
	}
	if got, reason := prepareExecution(executionRequest{}); got != nil || reason != executionClosureUnavailable {
		t.Fatalf("unexpected prepare: %v %q", got, reason)
	}
	tracker := &executionTracker{}
	token := tracker.begin(candidate)
	if err := tracker.certify(token, receipt); err == nil || !strings.Contains(err.Error(), "closure") {
		t.Fatalf("certified identity without closure: %v", err)
	}
	if _, ok := tracker.receipts(); ok {
		t.Fatal("incomplete collector qualified")
	}
}
