# Native Go execution inventory

Status: private collector with tracked comptime execution integration. No result
hits, evaluator-marker removal or candidate/receipt certification. This implements
only the Go-input portion of evaluation-cache.md and execution-api.md.

The first supported envelope is Go 1.26/1.27 on native Linux amd64 at the baseline
v1 CPU level, standard-library dependencies, no cgo/foreign objects, no custom
package driver, workspace, overlays, module hooks, Go experiments or external
cache/linker helpers. Other modes decline conservatively. A generated package
contains exactly main.go with frozen go.mod/go.sum. Expansion to more platforms
and generated support inputs requires explicit policy and test coverage.

```go
type goExecutionStage struct {
    Root, Mode string // actual staging directory, predicate or comptime
    Output string // exact absolute executable path; empty for discovery only
    Program []byte
    Module *goModuleInputs
}
func captureGoExecution(ctx *goContext, stage goExecutionStage) (*goExecutionInventory, executionDecline)
func (inventory *goExecutionInventory) current() bool
func (inventory *goExecutionInventory) identity() [sha256.Size]byte
```

The owned inventory contains schema, staged root and mode, exact effective
launcher/argv/environment, generated/module content witnesses, selected SDK root,
tool directory/version, canonical ordered go-list package descriptors, tool/file
content digests and directory membership/resolution evidence. No AST/checker,
callbacks, os.FileInfo or caller-owned mutable byte slices survive capture. A seal
rejects accidental mutation of these owned fields before validation. This seal is
an internal ownership check, not an authentication or persistence format.

The build envelope pins toolchain selection local, cgo off, internal linking,
Go environment/workspace off, module reads readonly and proxy/sumdb offline.
Capture and execution use the same envelope. BuildArgs binds the actual executable
output argument, which must be outside the frozen stage. Staging layout is opaque
to the collector.

Tracked comptime recipes first pass the static selection audit. Requests whose
captured context already disables cgo then attempt this supported capture against
their published stage. A successful capture builds with its owned launcher, argv
and environment, executes the fresh binary under the existing comptime deadline,
and validates the inventory afterward while holding the stage lock. Unsupported
or declined captures build with the existing command and effective published
module bytes. One-shot callers keep their existing execution path. Predicate
execution integration remains separate.

Request-owned counters and the last Go inventory identity describe collection
and endpoint validation; no inventory or semantic graph is retained in a Session
artifact. The execution tracker records each recipe attempt before execution and
declines it on every outcome. A validated Go inventory does not qualify a result:
the remaining execution closure, policies and Facts are still mandatory. Empty
phases do not allocate a tracker or collect SDK inputs. Unsupported selections
decline before discovery wherever their captured context/static audit suffices.

Capture inventories launcher and SDK tools, VERSION/go.env, assembler include
support, all package-level files, selected embedded files and assembly include
closure. Assembly search records positive header contents and the membership
needed to detect newly appearing higher-priority files. The compiler-generated
go_asm.h is derived from pinned compiler bytes and selected source; other unknown
or unresolved includes decline. Headers such as runtime/cgo/abi_amd64.h are inputs
even when cgo is disabled. Foreign objects and external cache/linker execution
remain unsupported. Nonblocking Unix opens prevent regular inputs replaced by
FIFOs from hanging validation.

Discovery uses bounded go list -deps JSON under the pinned envelope. Capture then
validates contents/membership, repeats dependency discovery and compares the full
selected package/file/search descriptors, and validates contents/membership again.
This catches persistent edits adding an import between the first discovery and
content capture. Validation has no mtime-only shortcut and follows the design's
accidental-staleness endpoint assumption, excluding adversarial change-and-restore.
The native helper current method validates only this filesystem/tool selection
inventory; executionReceipt.current and prepareExecution remain ineligible.

Limits are versioned: at most 4,096 packages, 100,000 files/directories, 16,384
entries in a directory, 512 MiB aggregate hashed content, 8 MiB environment metadata and 8 MiB path/membership
metadata and 16 MiB subprocess output. Assembly parsing has an 8 MiB per-file and
16 MiB aggregate and 1,000,000-token bound. Subprocess discovery has a 30-second deadline and bounded
stderr. Data is rejected before retaining over-budget payloads. Initial content
capture, repeated validation and go-list rediscovery are real costs to measure,
not a reason to substitute stats or existing Go-name metadata receipts.

Remaining integration must bind the checked source/module snapshot, exact ordered
query/comptime selections and prior values, compiler/intrinsic namespace, generated
support/import initialization, native/value/effective-limit policy and decoded
results, plus actual build invocation binding for predicate executions.
All-invocation certification and current Facts
remain mandatory before Session or disk result eligibility can change.

In-place GOROOT edits are unsupported, same as go build; run go clean -cache.
