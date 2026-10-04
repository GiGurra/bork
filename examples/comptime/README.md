# Compile-time table and validated configuration

Run from the repository root:

```sh
bork run examples/comptime
```

The first block computes an insertion-ordered map of squares. The second reads
`config.json` relative to this module and decodes it into `Config` during
compilation. Derived decoding validates a nonempty name, a positive limit, and
the whole-record invariant that the limit is at most four. Invalid JSON or failed
validation makes compilation fail. `configuredName` receives the record's known
invariant without a runtime guard.

Later blocks use the earlier baked map and configuration to select a square and
sum the table. Their values are computed once while checking; the resulting
program only prints the baked data. This example does not require result caching.

Inspect the generated Go:

```sh
bork emit examples/comptime > /tmp/comptime.go
```

Look for the map entries, `compiled defaults`, `16`, and `30` in the literals.
The runtime path contains no configuration file read or JSON parsing recipe.

To verify that the compiled program no longer needs its data file, use a temporary
copy so the checked-in example remains runnable:

```sh
example_dir=$(mktemp -d)
cp examples/comptime/main.bork examples/comptime/bork.mod examples/comptime/config.json "$example_dir/"
bork build "$example_dir" -o "$example_dir/program"
rm "$example_dir/config.json"
"$example_dir/program"
rm -r "$example_dir"
```

The output still matches [the golden output](../../testdata/examples/comptime.txt).
Recompiling after removing the file fails, because each fresh check requires its
build inputs. See [the comptime contract](../../docs/design/comptime.md) for purity,
closed captures, native targets, build-input rules and limits.
