# Library dependencies

Status: proposal for review (bork-hxtpmo). Implementation waits for approval.
Service documentation and terms checked on 2026-10-04.

## Decision

A bork library is a Go module containing a root `bork.mod` and `.bork` sources.
The Go command downloads and verifies it. Bork loads and checks its source just
like local packages, then compiles the whole program to Go. There is no separate
registry, binary library format, package-kind flag, or dependency solver.

Use `bork deps get <module>@<version>` for Go and bork libraries. For new
projects, use native `go.mod` and `go.sum` beside `bork.mod` as the single
dependency manifest and checksum file. Keep reading and maintaining existing
`go-deps.mod` / `go-deps.sum` projects for compatibility. Do not add `require`
to `bork.mod`; it continues to name the bork module and its unsafe packages.

The two module declarations must match for new native-manifest projects and
published libraries. A library's `unsafe` entries authorize only its own
packages; consumers do not repeat those entries. Requiring and importing a
library is a decision to trust its implementation, including its unsafe code.

## User workflow

Publish: create `bork.mod`, run `bork deps init` to create `go.mod` and `go.sum`,
write and test library packages, commit the manifests and sources to a public
repository with a redistributable license, and push a version tag.

Depend: run `bork deps get github.com/acme/greeting@v1.0.0`, then
`import "github.com/acme/greeting/text"`. Commit `go.mod` and `go.sum`.

Upgrade: run `bork deps get github.com/acme/greeting@v1.1.0` (or `@latest`),
review the manifest/checksum diff, and run the consumer's checks and tests.
`@none` removes a requirement; `bork deps download` restores downloaded content
and missing checksums. Ordinary compilation never changes these files.

These commands describe the proposed behavior, not the current release.

## Manifests and publication

Native `go.mod` avoids a second published dependency graph. Extending
`go-deps.mod` alone would require publishing a synchronized `go.mod` too:
Go cannot discover transitive requirements in a file named `go-deps.mod`.
Adding requirements to `bork.mod` has the same problem and adds another parser
and synchronization rule. Native Go files already describe both dependency
kinds; bork does not need to distinguish them in a requirement.

`bork deps init/get/download` retain their current meanings and transactional
resolution: run Go in a temporary directory, validate the result, stage both
output files, and publish checksums before requirements. Resolution and
validation failures leave source manifests untouched. A filesystem failure
during the final replacements can leave extra checksums with the old manifest,
as today; this is not a promise of an atomic two-file transaction.

For a new project the helper creates `go.mod` / `go.sum`. For a legacy project
with only `go-deps.mod`, it continues to use that pair. Migration before
publication is a one-time rename of the pair and, if needed, correction of
the descriptive legacy module line to match `bork.mod`. If both manifest names
exist at the bork root, fail with instructions to keep one pair; do not silently
merge or overwrite an existing Go project's manifest. A parent repository's
`go.mod` is not a bork project's manifest. Empty dependency sets may have an
empty checksum file. Embedded std manifests keep their existing names.

For native bork manifests retain the supported `module`, `go`, and pinned
`require` directives. Permit `retract` in an author's native `go.mod` for
publication; do not propagate it to generated programs. `replace`, `exclude`,
`toolchain`, `godebug`, `tool`, and `ignore` remain unsupported as bork project
configuration initially. Go controls the semantics of arbitrary Go
dependencies' own manifests; their replacements are ignored by Go as usual.
Published bork libraries must obey the native bork manifest subset. A helper may
discard Go's automatically added toolchain suggestion as it does today.

A downloaded module is a bork library when it has a valid root `bork.mod`
whose module path exactly equals the selected Go module path. Absence means
Go-only; presence with malformed syntax or a mismatch is an error, not a
fallback to Go. A mixed module can supply both kinds of package. No dummy
`.go` file is required for a bork-only library. Dependencies' standalone
scripts are never importable packages. Nested modules remain separate releases.

`deps get` resolves the query with Go, downloads the selected module, and
validates the bork marker when present. A bare module query works for both
kinds. Existing Go package queries remain supported; bork users must specify
the module path, because Go does not recognize a directory with only `.bork`
files as a Go package. Diagnose that failure with guidance to use the library's
module path. Successful classification does not require executing library code.

Do not run `go mod tidy` on a bork library: it cannot see bork imports and may
remove their requirements. `bork deps` owns dependency updates. Libraries
declare all their Go and bork requirements in their published `go.mod`, including
the Go requirements of imported bork std packages. `deps get/download` must
inspect the project's package import closure and merge those std requirements
before finishing the graph. `deps init` still needs no valid source. Before
tagging, the author runs `deps download` followed by `bork check` and tests;
compilation diagnoses a missing publication requirement rather than silently
repairing the author's manifest. Libraries are source releases checked by the
consumer's bork compiler; compiler-version negotiation is deferred.

## One selected graph

Go minimum version selection (MVS) selects one version per module path. In a
diamond, requirements on `example.com/common` at `v1.2.0` and `v1.4.0` select
`v1.4.0`. `/v2` identifies a different module and can coexist with v1; the
existing checker and generator must preserve their distinct package identities.
There is no version-range solver or exact-version override. An incompatible
minor release is an upstream compatibility bug; diagnostics identify the
selected version so the consumer can pin a working graph or upgrade callers.
Use Go's version-query, pseudo-version and retraction behavior.
[Go modules reference](https://go.dev/ref/mod#minimal-version-selection).

Build a temporary resolution module from the root dependency manifest and
the imported std manifests, with `GOWORK=off`, `GO111MODULE=on`, and empty
`GOFLAGS`, as today. Preserve Go's proxy, private-module, credentials, cache,
and toolchain configuration. Use Go's selected build list (`go list -m -json
all`) and download metadata (`go mod download -json`); do not reproduce MVS
by taking the maximum of only the requirements immediately visible to bork.
Walk the complete required graph so source-only modules' transitive dependencies
are available even though no Go import reaches them.

Resolve bork imports against that selected graph. A root-local package stays
local. An external package comes from a selected module whose path is a
component-boundary prefix of the import path, whose bork marker validates,
and whose package directory contains `.bork` files. Multiple actual providers
are an ambiguity error rather than silently choosing the longest prefix.
Do not fetch a new module merely because an import names it: an unresolved
import suggests `bork deps get <module>@<version>`. Package loading tracks the
owning module of every file, rather than applying the root's module metadata
and unsafe grants to every dependency. Detect ordinary package import cycles
across module boundaries too. Validate directory containment and never resolve
paths by concatenating unchecked import text.

Use exactly the same selected graph for bork source loading, Go bindings,
compile-time evaluation, generated builds, and editor checks. If loading sources
reveals a previously unseen imported std requirement, recompute the graph and
reload affected sources before checking. Continue until the graph and loaded
closure agree, with a bounded convergence check and an error if they do not.
Never compile sources from one version while linking Go bindings from another.
For published bork dependencies, a required std version missing from the
published graph is an actionable library-publication error, not an excuse to
select a private second graph for that library.

Root checksums must cover every selected external module's content and
`go.mod`, including bork and transitive dependencies. The helper downloads the
complete selected graph and commits those hashes. Compilation resolves in an
isolated temporary module, permits downloading missing cache content under
existing Go policy, but rejects missing root hashes with guidance to run
`bork deps download`. A dependency's own `go.sum` is not the consumer's lock
file and cannot authorize a hash absent from the root's pinned input. Conflicting
hashes and failed Go verification fail the compilation. A warm cache supports
`GOPROXY=off`; an empty offline cache reports the missing module.

## Unsafe and guarantees

Check unsafe code against its owning library's root `bork.mod`. Reject grants
for packages outside that module; a dependency cannot authorize unsafe in the
consumer, another dependency, or a reserved `bork/` package. The consumer's
existing grants still apply to its own bodies and bindings. The standard
library retains its compiler-owned authorization.

Consumers do not approve a second list of foreign grants, and dependency
updates do not trigger a new interactive approval flow. Repeating every
transitive package grant would turn ordinary use of a library into maintaining
its implementation inventory. This is the same trust decision as using Go
dependencies or std's unsafe implementations. It must be described plainly:
checksums prove source integrity, not correctness or effect honesty. Unsafe
dependencies can violate language guarantees or execute code during compiler
evaluation; their code is not sandboxed. Their bork signatures and bodies still
undergo the existing effect, lifetime and binding checks. Foreign unsafe code
does not become an audited intrinsic or qualify for proof execution reuse
merely because it came from a checksum-verified module.

## Cache, formatting, editor, scripts, and cleaning

Keep dependency sources in `GOMODCACHE`, using the paths returned by Go, not
hand-built cache paths. Identify a selected release by module path, canonical
version, and verified content checksum. Capture the selected graph, root
manifest/checksum bytes, dependency `bork.mod` bytes, source membership, source
bytes, and embedded assets as compiler inputs. A changed selected release
invalidates compilation reuse. Cache eviction, missing files, or modification
of extracted files must also invalidate or fail validation; Go's normally
read-only extraction is not proof that files can never change. Retain current
content validation initially rather than replacing it with version-only trust.
Memoize graph resolution within a Session against all of these inputs and the
effective Go environment; do not consult the network on every unchanged editor
request. Skip disk compilation reuse if complete dependency receipts cannot
be captured. There is no new dependency cache owned by bork.

`bork fmt` formats only requested project files. It does not follow imports
into the module cache or rewrite downloaded sources. Explicit attempts to
format/write cache sources fail with guidance to edit the original repository
and publish a new version. Library authors keep their `.bork` files fmt-clean.

LSP imports, completion, hover, and go-to-definition use the same resolver and
original cached source positions. Definition targets are navigable read-only
cache documents. Diagnose missing downloads using `bork deps download`;
dependency fixes and rename edits must not write into the cache. An editor
request uses cached dependencies and does not silently install new modules.

Standalone `// bork:require <module> <canonical-version>` headers accept both
kinds without a new directive. Resolve them before import loading using the
existing script dependency cache, extend its identity/version for the new
resolution rules, and capture the resulting graph and checksums as inputs.
First resolution uses Go's normal verification; subsequent compilation uses
the pinned cached graph. As today, these headers do not supply a portable
checksum lock file: deleting the script dependency cache permits fresh
resolution of transitive requirements. Projects remain the reproducible option
for a committed graph. Library unsafe grants are honored independently of
`// bork:unsafe`, which authorizes only the root script's unsafe code.

`bork clean` keeps its current cache scope. `bork clean --all` also removes
bork's script resolution entries, but never source manifests or Go's shared
module cache. Use `go clean -modcache` to remove shared downloaded modules.
Private deployments can use a corporate `GOPROXY` (including Athens or
Artifactory) or `GOPRIVATE` direct access without changing bork manifests.
Checksum verification follows `GOSUMDB` and `GONOSUMDB`; private deployments
may not have public checksum-database verification.

## Official proxy: feasibility, permission, and privacy

The [Go module archive specification](https://pkg.go.dev/golang.org/x/mod/zip)
allows ordinary source files without a Go-only extension requirement. Its
constraints cover paths, module versions, case collisions, size, regular files,
and nested modules. `.bork`, `bork.mod`, and asset files fit those rules. Publish
assets inside the module: symlinks and files omitted from a Go module archive
cannot be required at runtime or compile time.

An offline probe with Go 1.27.1 and a temporary `file://` proxy confirmed that a
module containing only `go.mod`, `bork.mod`, and `text/text.bork` can be added
with `go get example.com/bork-only@v1.0.0`, downloaded, extracted, and assigned
content and go.mod hashes. `go get example.com/bork-only/text@v1.0.0` instead
fails because there is no Go package. This proves the local transport mechanics,
not an affirmative legal permission or an official-proxy availability promise.

I read both service homepages, both privacy pages, Google's general terms and
privacy policy, and its service-specific terms directory. The service pages
link a shared privacy policy; neither exposes a separate service terms page
(`/terms` returned HTTP 404), and the service-specific directory has no entry
for these Go module services. No reviewed text imposes a percentage of Go code
or requires a `.go` file. There is also no explicit statement approving modules
written in another language. Our conclusion is an inference: ordinary public,
valid, licensed modules consumed through Go are a sensible use of the published
service, including bork libraries compiled to Go. We cannot claim that Google
has specifically authorized bork-only modules. The implementation should use
normal configurable Go tooling, so an operator can choose another proxy if its
policy differs. [Service description](https://proxy.golang.org/),
[checksum service](https://sum.golang.org/),
[service-specific terms directory](https://policies.google.com/terms/service-specific?hl=en).

The relevant [Google terms](https://policies.google.com/terms?hl=en), effective
July 30, 2026 in the fetched US version, require compliance with their terms,
other people's rights, lawful sharing, and anti-abuse rules. They say:

> You must not abuse, harm, interfere with, or disrupt our services or systems

The quoted restriction applies to normal automated clients too. Distributing a
library requires the rights to redistribute its contents; we do not use the
proxy as arbitrary storage or perform bulk crawling. Nothing reviewed supplies
a permanence or service-availability guarantee. See also the
[general privacy policy](https://policies.google.com/privacy?hl=en), to which
the Go services' privacy policy explicitly refers.

The service FAQ says:

> These services can only access publicly available source code.

It recommends `GOPRIVATE` for private modules and documents license-sensitive
retention: a module without a detectable suitable license may be served only
temporarily, while recorded checksums persist. Publish an identifiable
redistributable license and keep the upstream repository available. Retraction
and a new release are the way to supersede a bad version; moving an existing
tag is not an update strategy. [Proxy FAQ](https://proxy.golang.org/).

Both [proxy privacy](https://proxy.golang.org/privacy) and
[checksum privacy](https://sum.golang.org/privacy) pages (last updated
August 6, 2019) describe logging timestamps, IP addresses, full request URLs,
and technical request details for monitoring/debugging. They say:

> We also do not correlate or combine information from our request logs

The sentence continues by excluding combination with personal information
provided to Google for other services. Logged personally identifying information
is retained for at most 30 days; aggregate anonymized popularity metrics may
be shared. A request reveals module path and version. Set `GOPRIVATE` before
requesting a private module so those identifiers are not sent to the public
proxy or checksum database. Using `GOPROXY=direct` alone does not disable the
public checksum service. [Private-module configuration](https://go.dev/ref/mod#private-modules).

## Example and implementation gate

The implementation will include a small library and consumer fixture:

```text
greeting/
  LICENSE
  bork.mod            # module example.com/greeting
  go.mod              # module example.com/greeting; go 1.26
  go.sum
  text/text.bork      # fn Greeting(): String { "hello" }
consumer/
  bork.mod            # module example.com/consumer
  go.mod              # require example.com/greeting v1.0.0
  go.sum
  main.bork           # import "example.com/greeting/text"
```

Publish the fixture into a temporary file proxy with `.info`, `.mod`, `.zip`,
and version-list endpoints using `golang.org/x/mod/zip` to enforce archive
rules. Run with isolated `GOMODCACHE`, `GOPROXY=file://...`, `GOSUMDB=off`,
and `GOWORK=off`; no network is needed. The test checksum database setting is
only for synthetic fixtures and does not change production defaults. Use the
proxy rather than `replace`, which is unsupported by the current manifests and
would not test release download/checksum behavior.

Acceptance covers bork-only and mixed libraries; Go dependencies called by
library unsafe code; std dependencies declared by publishers; transitive and
diamond selection; upgrades and `/v2` coexistence; ambiguity and path mismatch;
malformed markers and missing packages; cross-module cycles; scoped unsafe
grants; rejected unsupported manifests; failed resolution leaving manifests
intact; scripts; binding/evaluator/build graph parity; warm and empty offline
caches; missing/conflicting/tampered checksums; cache invalidation on dependency
changes, deletion, or content edits; read-only formatting and LSP navigation;
and cleaning without touching `GOMODCACHE`. Update reader-facing CLI, packages,
Go interop and script documentation, grammar, requirements, and README during
implementation. Local checks remain focused; GitHub CI runs full suites.

The review decisions are the native manifest name with legacy compatibility,
trusting dependency-owned unsafe grants, and the documented limits of the
official-proxy permission inference. No compiler or CLI behavior changes in
this design PR.
