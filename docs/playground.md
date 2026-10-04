# Browser playground

[Open the playground](https://gigurra.github.io/bork/try/) to try bork without installing the compiler. It checks one file, shows the type of the name or expression at your cursor, and formats source. The code stays in your browser; it is not sent to a compilation service. Programs are not run.

Choose an example, edit it, and press **Check**. Click a diagnostic to jump to its position. To query a type, put the cursor on the name or expression and press **Inspect type**. **Format** works even when the file has type errors. **Download source** saves `playground.bork` so you can continue locally.

The browser includes the prelude and checks ordinary types, matching, effects, lifetimes, and facts established by guards. Imports, user Go interop, `comptime`, compile-time interpolation validators, and proofs that require executing predicates need the native compiler. These report **needs local bork**, rather than accepting an incomplete check. The playground accepts up to 32 KiB of source and stops a compiler action that takes too long; a later action starts a fresh compiler worker.

The first action downloads the browser compiler (roughly 3–4 MiB compressed). Older browsers without gzip decompression support download the larger raw Wasm instead. The docs build keeps the Wasm module and its Go runtime together so browser caches cannot mix compiler bundles.

After installing Go and bork as described in the [tour](tour.md), check the downloaded source:

```sh
bork check playground.bork
bork fmt playground.bork
bork run playground.bork
```

A successful browser check means the supported checks passed. Use local bork for full checking, testing, and execution. See [the bork command](cli.md) for those commands.
