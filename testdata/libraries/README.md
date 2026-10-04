# A library and its consumer

`greeting/` is a complete library repository: a public `Greet` function, a test,
`bork.mod`, generated `go.mod`, and `bork.sum`. `consumer/` is a separate project
that requires the published library at `v1.0.0` and prints `Hello, Ada!`.

The `example.com` paths illustrate repository names. To publish, copy the
library into your own repository, replace its module path in `bork.mod`, then:

```sh
bork deps download
bork check .
bork test .
git add bork.mod bork.sum go.mod lib.bork lib_test.bork
git commit -m 'Publish greeting library'
git tag v1.0.0
git push origin HEAD v1.0.0
```

In a separate project, add your repository by its module path and tag, and import
that path. For example, with a real repository named `example.com/greeting`:

```sh
bork deps get example.com/greeting@v1.0.0
bork build . -o greeting-app
./greeting-app
```

Upgrade with `bork deps get example.com/greeting@v1.1.0`, after publishing that
release. Commit the consumer's `bork.mod`, `bork.sum`, and generated `go.mod`.
For a library using Go helpers or standard packages with external Go modules,
`bork deps download` also records the dependencies needed by its consumers.

CI tests this exact pair with a temporary `file://` Go proxy and isolated module
cache. No public repository or network access is needed:

```sh
go test ./internal/driver -run '^TestLibraryOfflineExample$' -count=1
```

This test preserves the example's committed checksum pins, installs the library
from the local proxy, then checks and builds with `GOPROXY=off`. It also checks
that cleaning Bork's cache preserves the shared Go module cache.
