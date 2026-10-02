package main

import "testing"

var result bool
var textResult string

func BenchmarkIntMapGet(b *testing.B) {
	var m _Map[int64, int64]
	for i := int64(0); i < 1000; i++ {
		m = m.put(i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, result = m.get(int64(i % 1000))
	}
}

func BenchmarkListMapGet(b *testing.B) {
	var m _Map[[]int64, int64]
	for i := int64(0); i < 1000; i++ {
		m = m.put([]int64{i, i + 1, i + 2}, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := int64(i % 1000)
		_, result = m.get([]int64{k, k + 1, k + 2})
	}
}

func BenchmarkRecordMapGet(b *testing.B) {
	var m _Map[Key, int64]
	for i := int64(0); i < 1000; i++ {
		m = m.put(Key{parts: []int64{i, i + 1, i + 2}, label: "key"}, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := int64(i % 1000)
		_, result = m.get(Key{parts: []int64{k, k + 1, k + 2}, label: "key"})
	}
}

func BenchmarkListEqual(b *testing.B) {
	a, c := []int64{1, 2, 3, 4, 5, 6, 7, 8}, []int64{1, 2, 3, 4, 5, 6, 7, 8}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result = equalInts(a, c)
	}
}

func BenchmarkRecordEqual(b *testing.B) {
	a := Key{parts: []int64{1, 2, 3}, label: "key"}
	c := Key{parts: []int64{1, 2, 3}, label: "key"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result = equalKeys(a, c)
	}
}

func BenchmarkListShow(b *testing.B) {
	a := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		textResult = showInts(a)
	}
}
