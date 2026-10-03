package syntax

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestParseFilesDeterministic(t *testing.T) {
	paths := []string{"z.bork", "a.bork", "m.bork", "b.bork"}
	srcs := [][]byte{[]byte("fn z() { @ }"), []byte("fn a( {"), []byte("fn m() { 1 }"), []byte("fn b() { @ }")}
	for _, parseOne := range []func(string, []byte, *diag.List) *File{Parse, ParseEmbedded} {
		expected := &diag.List{}
		want := parseFiles(paths, srcs, expected, parseOne, 1)
		for range 10 {
			diagnostics := &diag.List{}
			got := parseFiles(paths, srcs, diagnostics, parseOne, 4)
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(diagnostics, expected) {
				t.Fatal("parallel parsing changed file or diagnostic order")
			}
		}
	}
}

func BenchmarkParseFiles(b *testing.B) {
	paths, err := filepath.Glob("../prelude/*.bork")
	if err != nil {
		b.Fatal(err)
	}
	var srcs [][]byte
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		srcs = append(srcs, src)
	}
	// Prime immutable tokens identically for both execution strategies.
	parseFiles(paths, srcs, &diag.List{}, ParseEmbedded, 1)
	for _, item := range []struct {
		name    string
		workers int
	}{{"serial", 1}, {"parallel", min(runtime.GOMAXPROCS(0), 8)}} {
		b.Run(item.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				diags := &diag.List{}
				parseFiles(paths, srcs, diags, ParseEmbedded, item.workers)
				if diags.Len() != 0 {
					b.Fatal(diags)
				}
			}
		})
	}
}
