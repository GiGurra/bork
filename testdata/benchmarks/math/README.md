From the repository root, benchmark generated Decimal operations:

```sh
math_bench_dir=$(mktemp -d)
cp testdata/benchmarks/math/go.mod testdata/benchmarks/math/runtime_test.go "$math_bench_dir/"
go run ./cmd/bork emit testdata/benchmarks/math > "$math_bench_dir/main.go"
(cd "$math_bench_dir" && GOMAXPROCS=1 go test -bench . -benchmem -benchtime=300ms -count=3)
```

Decimal stores canonical coefficient strings. Each operation parses them into
fresh mutable Go big integers and formats an immutable result. Addition currently
aligns and formats both coefficients, then parses them again for addition.
The fixture includes a direct Go big.Int addition baseline to expose this cost;
that baseline excludes scale alignment, parsing, formatting and bork boxing, so
it is not an equivalent end-to-end operation. Keep this fixture when considering
an immutable binary representation or eliminating intermediate string conversions.

Initial measurement (Linux amd64, Ryzen 7 5700G, Go 1.27.1, one CPU, 300ms,
three runs): Decimal Add 769–879 ns/op, 432 B/op and 23 allocations; Decimal Div
657–751 ns/op, 288 B/op and 18 allocations; the direct big.Int baseline 39–40
ns/op, 80 B/op and 2 allocations. Treat these as an illustration of overhead,
not a performance guarantee; rerun on the target machine and compiler revision.
