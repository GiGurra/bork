# Contributing and design notes

These documents are for people working on the compiler and the standard library. They record what was decided and why, in much more detail than the reader pages. You do not need them to use the language.

## References

- [Grammar](grammar.md): the syntax the compiler accepts, in EBNF, with the semantic rules in brief.
- [Changing the syntax](syntax-changes.md): every component to update when the syntax or grammar changes.
- [Requirements](requirements.md): the full specification of every feature, including the decisions behind it.
- [Roadmap](roadmap.md): the milestones and the compiler pipeline.
- [Go helpers for standard packages](std-go.md): the Go API that `unsafe go` code in the standard library uses, and how Go dependencies are pinned.
- [The prelude sources](../internal/prelude/README.md): the bork code that is available in every program.

## Working on the compiler

- [CI test shards](ci.md): how the test suite is split and cached in CI.
- [Compiler performance](design/performance.md): the benchmark harness and profiling reports.
- [Driver test scheduling](design/driver-test-scheduling.md): an audit of the tests that run serially.

Golden tests live in `testdata/cases/<name>/`, and every example's expected output in `testdata/examples/`.

The documentation is tested too, in `internal/driver/docs_snippets_test.go`:

- `TestDocSnippets` compiles the bork code blocks of the README and the reader pages. A block marked `bork` must compile and be formatted. One marked `bork fails` must not compile, and a `text` block right after it quotes the compiler's message, which is checked as well. One marked `bork fragment` is not checked.
- `TestDocLinks` checks the relative links, and the headings they point at, in every Markdown page under `docs/` and `examples/`.

## Building the documentation site

The [MkDocs](https://www.mkdocs.org/) site builds the existing Markdown pages and derives navigation from `docs/README.md`. Standard-package navigation follows `docs/std/README.md`. Search runs in the browser. The build rewrites links outside `docs/` to GitHub source URLs; `BORK_DOCS_REVISION` selects the revision (the deployed build uses its commit SHA). Source pages keep their repository-relative links, and `TestDocSnippets` and `TestDocLinks` remain the checks for examples and links.

```sh
python3 -m venv .venv-docs
.venv-docs/bin/pip install -r scripts/docs-requirements.txt
.venv-docs/bin/mkdocs serve
.venv-docs/bin/mkdocs build --strict
python3 -m unittest discover -s scripts -p 'test_docs_site.py'
```

`serve` previews the site at `http://127.0.0.1:8000/bork/`; `build` writes `site/`. Neither changes the Markdown sources. The Documentation workflow validates pull requests and publishes main through GitHub Pages. A repository administrator must select **Settings → Pages → Build and deployment → Source: GitHub Actions** once. The workflow needs no publishing token or extra secret; its deploy job uses GitHub's Pages permissions. See [GitHub's custom workflow documentation](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

## Building the browser playground

After building the docs, add the Wasm playground and serve the generated site:

```sh
python3 scripts/build_playground.py
python3 -m http.server 8000 --directory site
```

Open `http://127.0.0.1:8000/try/`. The build uses the selected Go SDK and copies its matching `wasm_exec.js` with the Wasm module and worker into a content-hashed directory. It prints raw/gzip download sizes. Browser source lives under `web/playground`; generated assets remain in ignored `site/`.

```sh
go test ./internal/playground
npm ci --prefix web/playground
npm test --prefix web/playground
cd web/playground
npx playwright install chromium
npm run test:browser
```

The Documentation workflow builds these assets and runs the browser smoke tests before publishing. The [playground design](design/playground.md) describes its scope and the local-compiler fallback for unsupported operations.

## Compiler releases

The Release workflow waits for successful CI on a main commit, builds Linux/macOS/Windows archives for amd64 and arm64 plus the VS Code VSIX, then creates the next stable patch tag and GitHub Release with SHA-256 checksums. Existing stable tags determine the version; without any, the first release is `v0.0.1`. Tags outside the `vX.Y.Z` format, including `vscode-v...`, do not affect compiler numbering. Manual stable version tags must also point to a main commit with successful CI. A rerun reuses the commit's existing tag and updates its assets.

Main CI and releases use [GitHub's concurrency queues](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency) to retain pending merges and serialize publication. Patch numbers are reserved by main's first-parent commit order starting with the introduction of release automation. If newer merge B finishes CI before A, B receives the later patch number and A can still publish its earlier number afterward; it does not become the latest release or downgrade the tap. Failed CI leaves a reserved gap until that commit passes and is released. Existing stable tags on an ancestor supply the version baseline, including a manually tagged minor release. No tag is pushed until all archives and the extension package are ready.

After publication, the workflow creates or updates `Formula/bork.rb` in `GiGurra/homebrew-tap` using the source tarball checksum. It checks out the tap with the `HOMEBREW_TAP_DEPLOY_KEY` write deploy key and skips tap updates cleanly when that secret is absent. The formula follows [Homebrew's Go build helpers](https://docs.brew.sh/Formula-Cookbook) and keeps Go as a runtime dependency. To retry a tap update without another release, dispatch the workflow with `homebrew_only=true`; it syncs the latest published release.

The packaged extension remains thin: it launches the installed compiler's `bork lsp`. Its independent extension version and registry publication guide are in [the VS Code directory](../editors/vscode/PUBLISHING.md).

## Design notes

| Note | Subject |
| --- | --- |
| [browser playground](design/playground.md) | Browser-local checking, hosting, download size, and execution boundaries |
| [async](design/async.md) | Transparent async bindings |
| [lazy](design/lazy.md) | Lazy bindings and computed record fields |
| [comptime](design/comptime.md) | Compile-time evaluation |
| [interpolators](design/interpolators.md) | Typed string interpolation |
| [interpolator validation](design/interpolator-validation.md) | Compile-time validators for interpolators |
| [interpolator validator reuse](design/interpolator-validator-reuse.md) | Shipping standard validators with the compiler |
| [backpressure](design/backpressure.md) | Bounded task pools, HTTP admission, and retry budgets |
| [HTTP propagation](design/http-propagation.md) | Forwarding deadlines and ambient values between services |
| [incremental compilation](design/incremental.md) | Package interfaces, cache invalidation, and watch sessions |
| [disk cache](design/disk-cache.md) | The compile cache on disk |
| [evaluation cache](design/evaluation-cache.md) | Caching compile-time evaluation |
| [execution API](design/execution-api.md) | How the compiler runs Go tools |
| [Go execution inventory](design/go-execution-inventory.md) | Where the compiler invokes Go |
