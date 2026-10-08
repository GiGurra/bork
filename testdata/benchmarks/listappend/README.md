Compare immutable slice copying with compiled loop-carried append, including
conditional append. From the repository root:

```sh
bench_dir=$(mktemp -d)
cp testdata/benchmarks/listappend/go.mod testdata/benchmarks/listappend/runtime_test.go "$bench_dir/"
go run ./cmd/bork emit testdata/benchmarks/listappend > "$bench_dir/main.go"
(cd "$bench_dir" && GOMAXPROCS=2 go test -run '^$' -bench . -benchmem -benchtime=100ms -count=2)
```

`copy` calls append through a helper, preserving its immutable O(n) behavior.
`reuse` appends directly to the carried binding. `branch` appends only for even
indexes, using an if expression. Timings exclude compilation and process startup.

Measured on an AMD Ryzen 7 5700G with Go 1.26, using the command above:

| Appends | Ordinary copy | Carried append | Conditional append (half retained) |
| --- | --- | --- | --- |
| 1,000 | 0.40–0.43 ms | 2.2 µs | 1.5 µs |
| 10,000 | 27–33 ms | 36–49 µs | 14 µs |
| 100,000 | 3.5–5.0 s | 0.98–1.03 ms | 0.63–0.83 ms |

At 100,000 appends, copying allocated 40.4 GB in about 100,000 allocations;
reuse allocated 4.1 MB in 28 allocations. These short runs on a shared host
illustrate scaling and allocation behavior, rather than stable latency targets.
