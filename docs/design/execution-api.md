# Shared execution collector API draft

Status: identity/tracker primitives and static auditing are implemented, but
execution candidates and receipts remain ineligible. Request-owned observation
uses shared installed-SDK identity and ordinary commands; cross-request proof and
comptime value reuse are deferred after the measured payoff checkpoint in
[go-execution-inventory.md](go-execution-inventory.md). The boundary below is the
remaining certification design, not a callable reuse API.

The proposed private driver boundary is:

```go
func prepareExecution(request executionRequest) (*executionCandidate, executionDecline)
func (candidate *executionCandidate) current() bool
func (candidate *executionCandidate) identity() [sha256.Size]byte
func (candidate *executionCandidate) certify(canonicalResult []byte) (*executionReceipt, error)
func (receipt *executionReceipt) current() bool
func (receipt *executionReceipt) identity() [sha256.Size]byte
```

A decline is a normal no-reuse result with a stable private reason, not a compiler
diagnostic. Tool/discovery/read errors are declines. Nothing in this API skips
ordinary execution, cycle checks, preflight or Facts. `prepareExecution` first
performs cheap native/policy/static eligibility checks before Go input inventories.

`executionRequest` is ephemeral and may hold AST/checker pointers:

```go
type executionRequest struct {
    files []*syntax.File
    info *check.Info
    queries []check.Query             // exact ordered batch, or
    computation *check.Comptime      // exactly one selection kind
    inputs *sourceSnapshot
    module *goModuleInputs
    context *goContext
    generated []byte                 // exact bytes used by this invocation
    mode executionMode              // proof or comptime + checking/test overlay
    roots []executionRoot           // resolved identities and source locations
    prior []executionPrior          // in dependency/evaluation order
    policy executionPolicy
}
```

The collector derives owned query descriptors from resolved queries, preserving
predicate/type/dictionary identities, canonical closed arguments and And/Or shape.
It derives the computation descriptor from its concrete checked result type and
selection. Callers cannot authorize eligibility by supplying text or a digest.
`executionPrior` contains a validated owned receipt, its canonical typed-value
identity, and the declaration/site identity. The collector clones and validates
all priors and preserves their order. Each prior's site, concrete type and typed
value identity must match the receipt's canonical result under its declared codec
and policy, not just independently validate. Missing/uncertified prior execution,
site/type/result mismatches and swapped prior values decline.
The request policy records protocol/closure/intrinsic/argument/value-policy
versions, effective per-context deadline and native target. Unsupported modes or
policies decline.

Candidates and receipts contain canonical owned data only: compiler-image
namespace, complete checked-source/module identities, descriptors, generated
bytes/digest, mode/roots/locations, ordered validated prior identities, intrinsic
contracts, selected Go context/tool/package/file/membership/search evidence and
policy. They contain no AST, checker graph, caller-owned byte slices or callbacks.
Candidate validation retains no successful result. Receipt certification adds
canonical result bytes/digest and validates endpoints after successful decoding,
current-type reconstruction and evaluation-level result/type/field obligations.
A false decoded proof remains a valid evaluation result and can be certified;
its consuming Facts may subsequently reject the compilation. Comptime recipe
and result-type obligations are distinct from narrower consuming-context Facts.
Certification does not wait for the entire surrounding Facts pass. Result decoding remains
separate: the value/proof layer must validate output before calling `certify`.
An invalid/corrupt persisted result is a miss, while invalid fresh output remains
an ordinary compilation failure.

`candidate.identity()` is the canonical execution-input lookup key.
`receipt.identity()` identifies the certified execution plus canonical result,
so prior computations and invocation summaries depend on the actual value.
Both use tagged length-delimited SHA-256 encoding with independent version tags;
no Go build ID, Query.Text, pointer identity or stat-only tuple is a key.
The candidate input identity remains available in the receipt for lookup.
The result digest never substitutes for validating source or execution closure.

Candidate/prior/result receipt cloning and decoding are bounded before allocation,
using versioned manifest/result limits as well as value node/depth limits. Reject
over-budget caller data before copying it. Certification retains owned receipt
copies; summary methods return independent bounded clones. No caller mutation of
an input candidate/prior or a returned receipt may change tracker certification.
Tests cover mismatched candidate receipts, duplicate/foreign/unbound tokens,
prior site/type/result swaps, budgets and mutations of returned data.

The tracker boundary is separate from `goUsage.evaluator`:

```go
func (tracker *executionTracker) begin(candidate *executionCandidate) executionInvocation
func (tracker *executionTracker) certify(invocation executionInvocation, receipt *executionReceipt) error
func (tracker *executionTracker) decline(invocation executionInvocation, reason executionDecline)
func (tracker *executionTracker) receipts() ([]*executionReceipt, bool)
```

Tokens are owned by one tracker and issued before every logical evaluator call,
including future cache hits. `begin` stores an owned expected input identity from
the candidate. Nil candidates remain unbound and can never qualify; certification
requires receipt input identity to equal the token's expected identity. Tokens
start unresolved and are completed once. The summary preserves invocation order and returns
false for unresolved, declined, duplicate, foreign or invalid tokens/receipts.
It revalidates every receipt; one eligible invocation never certifies a phase.
The foundation does not integrate hits or clear `usage.evaluator`. Later enclosing
artifact accounting may qualify only after all invocations are certified, current
enclosing Facts/checking succeeds and no unrelated bypass remains.

Roll out in reviewable slices: owned identity/tracker primitives with no eligible
candidates; exhaustive typed/static eligibility and audited intrinsic contracts;
endpoint-coherent Go execution closure discovery/content/membership/search
validation; then actual invocation integration without hits. Each incomplete
collector stage declines safely. Source/name/config receipts alone never make a
candidate eligible. Only afterward add Session proof/value lookup through perf's
store interface and current-graph reconstruction; persistence stays separately
versioned through perf's bounded envelope.

## Static audit boundary

`check.AuditExecutionQueries` and `check.AuditComptimeExecution` produce owned
static reports without retaining checked graph pointers. They follow all query
leaves, implementations, defaults, constraints, dictionary methods and closed
captures. Required prior sites are an inventory; the eventual driver receipt must
bind their actual ordered site/type/value identities. Unknown nodes, variable
kinds and targets decline. Traversal and expanded type display have independent
bounds; repeated structural edges consume rendering budget even when graph
traversal can memoize them.

The initial policy deliberately declines generic implementations, unresolved
higher-order callbacks, generated class implementations, ambient/runtime memos,
lazy cells, assets and build reads. Dictionary methods and field decoders retain
the checked declaration order. The first intrinsic is resolved prelude
`String.byteLength`, with an exact signature/body contract and implementation
hash. That static match still requires the compiler/intrinsic namespace and
endpoint-coherent tool/package closure; a static report cannot certify execution
or enable a hit. `prepareExecution`, candidate/receipt current checks and tracker
certification remain ineligible throughout this slice.
