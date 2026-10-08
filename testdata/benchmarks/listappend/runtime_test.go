package main

import (
	"fmt"
	"testing"
)

var result []int64

func BenchmarkAppend(b *testing.B) {
	for _, n := range []int64{1000, 10000, 100000} {
		for _, impl := range []struct {
			name  string
			build func(int64) []int64
		}{{"copy", buildCopy}, {"reuse", buildList}, {"branch", buildFiltered}} {
			b.Run(fmt.Sprintf("%s/%d", impl.name, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					result = impl.build(n)
				}
			})
		}
	}
}
