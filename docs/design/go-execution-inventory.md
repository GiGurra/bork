# Native Go execution inventory

Status: private collector foundation. No execution integration, result hits,
evaluator-marker removal or candidate/receipt certification. This implements
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
Capture and future execution must use the same envelope. BuildArgs currently
records the fixed supported template; integration must bind the actual executable
output argument and reject differing hooks/flags rather than certify a command
which merely resembles that template. Staging layout is opaque to the collector.
This inventory neither authorizes a build nor changes today's evaluator commands.

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
support/import initialization, native/value/effective-limit policy, actual build
invocation and decoded results. All-invocation certification and current Facts
remain mandatory before Session or disk result eligibility can change.

In-place GOROOT edits are unsupported, same as go build; run go clean -cache.
