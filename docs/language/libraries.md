# Libraries

A bork library is a Go module containing `bork.mod` and `.bork` packages. Import
its packages by module path plus directory, as for [local packages](packages.md).

## Add a dependency

From the consuming module's root:

```sh
bork deps get github.com/acme/greeting@v1.0.0
bork deps download
bork check .
```

Pass the **module path**, rather than a directory containing only `.bork` files;
Go does not recognize that directory as a Go package. The command records pinned
`require` lines in `bork.mod`, checksums in `bork.sum`, and a generated `go.mod`.
Commit all three. See [dependency commands](../cli.md#deps) for command options.

Use the same command for Go and bork libraries. Upgrade with a newer version or
`@latest`; remove a requirement with `@none`. Go selects one version per module
path using minimum version selection: a diamond selects the higher requirement.
Major versions from 2 onward use `/v2`, `/v3` and so on, so different major paths
can coexist. Visibility and import-cycle rules also apply across libraries.

Downloaded sources live in Go's module cache and are checked against pinned
content hashes. After warming the cache with `bork deps download`, `GOPROXY=off`
supports offline work; missing modules fail rather than being fetched.

## Publish a library

1. Create `bork.mod` with the public module path, such as `module github.com/you/greeting`. Export the API with upper-case names. A library needs no `main` function.
2. Run `bork deps init` to create generated `go.mod` and empty `bork.sum`. Add dependencies with `bork deps get`.
3. Run `bork deps download` before tagging. It records dependencies of every authored package, including imported standard packages.
4. Check, test and format each authored package. See [Testing](testing.md#test-every-package) for a CI recipe.
5. Commit the sources, `bork.mod`, `bork.sum`, generated `go.mod` and a redistributable license. Push a version tag such as `v1.0.0`.

The generated Go manifest lets proxies and consumers discover transitive
requirements. `module` and `require` declarations must agree between `bork.mod`
and `go.mod`. Do not edit generated `go.mod` or run `go mod tidy`: Go cannot see
bork imports. `bork deps download` repairs generated-manifest drift.

The [library/consumer fixture](../../testdata/libraries) includes a library
with a `_test.bork` file, committed manifests and checksums, and an offline
proxy test. Library authors can bind to Go helper packages in their repository
before publication. Bork stages the local replacement in its temporary Go
compilation module; committed manifests retain published paths.

## Private modules

Go's `GOPROXY`, `GOSUMDB`, credentials and module-cache settings apply. Configure
`GOPRIVATE` **before** requesting private module paths, so the normal public
proxy and checksum service do not receive those requests:

```sh
export GOPRIVATE=github.com/your-company/*
bork deps get github.com/your-company/greeting@v1.0.0
```

Configure credentials for the repository host, or use your corporate module
proxy. See the [proxy service analysis](../design/library-dependencies.md#official-proxy-feasibility-permission-and-privacy)
for official-service terms and privacy details.

## Unsafe libraries and cached sources

A library's `unsafe` grants apply to its packages; consumers need not repeat
them. `bork deps get` and `bork deps download` report newly selected releases
that contain unsafe Go packages, including transitive dependencies. There is
no approval prompt. Checksums establish integrity, not correctness or effect
honesty. Unsafe Go can violate guarantees and run during compiler evaluation;
it is not sandboxed. See [Calling Go](go-interop.md).

Downloaded sources are read-only to `bork fmt` and editor edits. Hover,
completion and navigation use cached source positions. Editor checks do not
install missing modules: run `bork deps download` first.

`bork clean` preserves the shared Go module cache. `bork clean --all` also drops
Bork's script resolution graphs. Use `go clean -modcache` to clear Go's cache.
