package driver

// executionRequest is deliberately empty in the identity/accounting slice.
// The exhaustive collector will add ephemeral selections and captured inputs;
// none of their semantic pointers may escape into candidates or receipts.
type executionRequest struct{}

// Preparation deliberately declines until exhaustive static eligibility and
// endpoint-coherent Go execution inventories are implemented. Source/name/Go
// configuration receipts and identity primitives alone cannot certify execution.
func prepareExecution(_ executionRequest) (*executionCandidate, executionDecline) {
	return nil, executionClosureUnavailable
}
