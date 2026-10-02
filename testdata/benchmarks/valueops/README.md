Run the same fixture with each compiler revision to compare generated value
operations. From the repository root:

```sh
bench_dir=$(mktemp -d)
cp testdata/benchmarks/valueops/go.mod testdata/benchmarks/valueops/runtime_test.go "$bench_dir/"
go run ./cmd/bork emit testdata/benchmarks/valueops > "$bench_dir/main.go"
(cd "$bench_dir" && GOMAXPROCS=1 go test -bench . -benchmem -benchtime=300ms -count=5)
```

The benchmarks cover primitive, list and record map keys, plus generated list
and record equality and list rendering. They use the public Go representation
of bork values, including `[]T` lists. The fixture's Go module keeps benchmark
sources separate from the compiler's normal test suite.
