# Derive expansion cache experiment

> **Status:** Completed experiment; the metadata-only cross-check plan cache is removed. Current reference: [compiler performance](performance.md). Checked-program Session reuse and check-local helper memoization remain implemented.

The initial metadata-only plan cache was removed after the realistic workload
failed to improve warm edits. The existing checked-program session cache and
check-local typed helper memoization remain. Resume cross-check plan caching
only when a concrete supported workload demonstrates a clear benefit; the
2026-10-06 review requested roughly 20% faster warm edits as the justification.

The prototype and benchmark are reproducible at commit
`ef779c59adc201ba0f7e3c32c2c6333a10d2b1fa`:

```sh
go test ./internal/driver -run '^$' -bench '^BenchmarkDeriveManyTypesEdits$' -benchtime=3x
```

The fixture contains 200 distinct records deriving `codec.Encode`,
`codec.Decode`, and `Labels`, including list/Option fields and defaults. Each
iteration changes only `main.bork`; model declarations remain in `models.bork`.
Emit measures checking plus generation. Watch measures checking through the
session's watch input tracking. The cache admitted Labels; source Encode used
typed field-read actions and deliberately declined the incomplete replay path.
Decode still used its compiler generator. Each cached iteration had 200 plan
hits and 200 declines. Every mode had zero native predicate evaluator batches.

Linux amd64, AMD Ryzen 7 5700G, three timed iterations per mode:

| Warm edit | Cached time | Uncached time | Cached bytes/op | Uncached bytes/op |
| --- | ---: | ---: | ---: | ---: |
| Emit | 947 ms | 900 ms | 232.6 MB | 213.6 MB |
| Watch | 292 ms | 254 ms | 88.1 MB | 69.2 MB |

The smaller 100-field Labels fixture also failed to show a clear benefit:
about 111 ms with or without caching after key/replay optimizations, with
approximately 9% more allocated bytes on replay. These are local samples, not
a statistical performance guarantee. Their direction supports removing this
prototype rather than extending its action coverage speculatively.

`BenchmarkDeriveManyTypesEdits` remains as the baseline for subsequent symbolic
checking and source Decode/schema work. It uses the existing session behavior
and reports native evaluator batches separately from ordinary compiler work.
