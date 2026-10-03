Throwaway migration tools for bork-tbci6n. DELETE this directory before opening the PR.

- prelude_conv.py: run from repo root on a CLEAN prelude; moves each free function that a
  one-line method forwards to into that method (doc comment included) and deletes the free one.
- zzmigrate: `go run ./wip/zzmigrate` needs package main under the module; build with
  `go build -o /tmp/zzmigrate ./wip/zzmigrate` then
  `FREE="$(cat wip/free_names.txt)" /tmp/zzmigrate $(git ls-files '*.bork')`.
  Rewrites f(a, b) -> a.f(b) (and maps.F(m, x) -> m.f(x)), innermost first, wrapping
  non-simple receivers in parens; drops `import "bork/maps"`. Skips calls inside string
  interpolations and function values (map(xs, length)): fix those by hand.
