# Library documentation and discovery

Design for `bork-khnvl1`, following the library dependency implementation in
[Library dependencies](library-dependencies.md). Start with a useful local
`bork doc` command. Its output also gives library authors documentation they can
commit or host beside a tagged release.

## Decision

Generate the public API from checked packages and render Markdown by default,
with a standalone HTML option. Use existing module requirements, visibility
rules and source comments. There is no documentation manifest or publishing
service to configure.

`pkg.go.dev` obtains module data from the Go proxy and module index, but its
API documentation is generated from Go source. That does not render Bork
symbols. A Bork-only release is not guaranteed to appear as a useful searchable
package there; this is an inference from its documented Go source model, not a
claim that its mirror rejects the module. We should link the author's own API
page rather than promising Bork documentation on pkg.go.dev.
[About pkgsite](https://pkg.go.dev/about#documentation),
[Adding a package](https://pkg.go.dev/about#adding-a-package).

## User workflow

```sh
# Read this package's public API.
bork doc .

# Document all packages in this module in one file.
bork doc --all . > API.md

# Generate one self-contained page for a library's repository or Pages site.
bork doc --all --html . > api.html

# Read a package from a dependency already pinned by this project.
bork deps get example.com/greeting@v1.0.0
bork doc example.com/greeting

# Read a standard package.
bork doc bork/http
```

The optional argument defaults to `.`. An existing directory selects its
package. A `.bork` source file selects its containing package, including sibling
sources; documentation describes a complete package rather than one file. Otherwise the argument
names a standard package or a Bork package in the current project's selected
module graph. There is no implicit `@latest` lookup or dependency installation;
missing downloads explain `bork deps download`. A library author can document
its own unpublished repository, including its local Go helpers.

`--all` visits packages below the chosen directory, within the same module. For
an installed library module path it visits that library's packages. It excludes
hidden directories, `vendor`, and nested modules, and never includes other
selected modules just because they are dependencies. Resolve a library module
root for `--all` before requiring package sources there: a module containing
only subpackages, with no root `.bork` files, is a valid target. Skip directories
without Bork sources; report a clear empty-module result when none exist.
Every package with Bork sources is considered; there is no new special meaning for an `internal`
directory. Standard-package arguments document just that package initially;
`--all bork/http` fails with a precise usage message.

`--html` changes only rendering. Standard output is the whole document; shell
redirection is enough to save it. Diagnostics go to standard error. A failed
package check, ambiguous provider, or changed input produces no partial document
and a nonzero exit status. The command needs no executable `main`.

## Public API

Visibility must come from the checker. Ordinary imported receiver methods
require uppercase names; lowercase class methods can be exposed with an
exported class. These are distinct rules, so a single capitalization filter
for every declaration is insufficient.
An API snapshot contains declarations owned by the requested package:

- Exported record, sealed, alias, resource and Go-bound types, with parameters,
  fields, variants, constraints, defaults, and construction visibility.
- Exported functions, predicates and named constructors, with parameter/result
  types, generic constraints, `requires`, `uses`, `needs`, and unsafe Go markers.
- Methods available to an importer: exported receiver methods and the methods
  of exported classes, including their lowercase class methods. Group receiver methods with their types;
  distinguish extension methods on another package's types.
- Exported classes, named instances, instance bundles, providers, provider
  bundles, interpolators, and package bindings, with their checked signatures
  and the information needed to use them.
- Exported ambient declarations, separately from immutable package bindings,
  with their types, constraints, and logged/propagated markers so importers can
  bind the values required by callable `needs` declarations.
- Checked constructors or other public declarations synthesized by the
  compiler when those are part of the package's callable API.

The view excludes private declaration entries, function bodies, raw unsafe Go
bodies, test bodies, imported declaration entries, and compiler-generated
implementation names. A private helper type appearing in an allowed public
signature is rendered as the checker names it; this command introduces no new
export restrictions. Private record construction is labeled explicitly rather
than implying a caller can build that record. Visible fields remain documented.

Effects and constraints are useful API facts. Show
Bork signatures and short descriptions, with stable anchors by package, kind,
receiver, and name. Order packages and declarations deterministically. Report
the selected module path and version when available, and whether a package
contains unsafe Go. Label sources with relative filenames and line numbers. Initially omit source
hyperlinks rather than guessing VCS URLs or generating broken links to files
absent from a hosted page. Do not embed absolute home directories or temporary
staging paths.

## Comments

Associate adjacent `//` comment groups with declarations and fields, using
source positions. A comment separated by a blank line is not attached to the
next declaration. Preserve ordinary prose and examples without requiring a
new documentation syntax or repeating the declaration's name.

For a package description, use a leading comment group before imports, or
one separated by a blank line from the first declaration, excluding shebang
and directive headers. A group adjacent to the first declaration belongs only
to that declaration. Collect package groups in stable filename order. Avoid guessing a summary from arbitrary
comments within a function. Existing field documentation participates too.

Markdown output uses fenced Bork signatures and prose descriptions. HTML uses
escaped text and source examples, a contents list, and small embedded styling;
it has no JavaScript, remote fonts, or renderer assets. Do not interpret comment
text as raw HTML. The output is useful by itself, in a repository README/API.md,
or as one static page. Add richer Markdown rendering later only when needed.

## Implementation

Add a checker query that returns owned API descriptions without exposing mutable
checker graphs. Reuse visibility and signature construction already used by
`bork describe` and the LSP. This prevents a second handwritten export policy
and avoids presenting a parser-only approximation as a checked API.

The driver resolves local, standard and pinned dependency package targets using
the existing loader, with cached-only dependency settings. It captures source,
manifest, checksum, Go-helper, and directory membership inputs before rendering.
Module-wide output validates one consistent collected snapshot before publishing
any bytes. Existing checking can evaluate compile-time expressions, as with
`bork check`; this command does not run package tests or a program entrypoint.

Use a small document model between checker/driver queries and Markdown/HTML
renderers. Both formats share ordering, visibility, comments, and signatures. The LSP
hover uses the same doc-comment renderer as `bork doc`, so comment text and
markup have one interpretation. Render facts/`where` constraints and `uses`
effects prominently in signatures, alongside `requires` and ambient `needs`.
A JSON format, remote module queries, and a public structured schema are not
needed for the first implementation. Keep `bork describe` for position/fact
queries and editor interactions.

## Library hosting and discovery

First ship the command and document the author workflow. A library can commit
`API.md`, link it from its README, or publish `api.html` through its existing
static site pipeline. Generate release documentation using the same Bork version
and committed dependency pins as the release being documented.

Do not make deployment part of `bork doc`. After real libraries exist, consider
a reusable Pages workflow that generates the page on tags and preserves versioned
URLs. Such a workflow should use pinned tools and explicit repository publishing
permissions; no reusable workflow is necessary to make the CLI useful.

For discovery, prefer a small human-maintained list of libraries linking to each
repository, install command, version and documentation. Add it when there are
libraries to list, rather than launching an empty registry or scraping the Go
module index for Bork-specific search. Go's proxy remains the distribution layer.
A search service can be evaluated later against actual usage.

## Validation and rollout

Cover public/private visibility; private lowercase receiver methods versus
public lowercase class methods; classes/instances/bundles; exported constrained
ambients and optional cross-package needs;
private record construction and checked constructors; effects, ambient needs,
constraints and generic signatures; source comments and HTML escaping; imported
versus owned declarations; deterministic module-wide ordering and nested module
boundaries, including a library with no root package. Use the existing offline
library/consumer fixture to document a
pinned dependency with `GOPROXY=off`. Check empty API packages, scripts, ambiguous
providers, absent downloads, and failures without partial output.

Add small golden documents for both formats and CLI checks for default targets,
`--all`, standard packages and selected library paths. Update the CLI reference,
README, package/library author page and requirements. Grammar changes are only
needed if the implementation introduces new source syntax; this proposal does
not. Full suites and race checks stay in CI; locally use focused query, renderer,
CLI and fixture tests plus lint and formatting.
