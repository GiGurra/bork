package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Body edits preserve the template and shape while invalidating the ordinary
// checked-program cache. This distinguishes expansion reuse from snapshot hits.
func BenchmarkDeriveTemplate(b *testing.B) {
	var source strings.Builder
	source.WriteString(`import "bork/shape"
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] { [comptime for (f in shape.fields[T]()) f.name] }
}
type Item = {
`)
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&source, " f%d: Int\n", i)
	}
	source.WriteString("} derive(Labels)\n")
	prefix := source.String()
	for _, mode := range []string{"cold", "unchanged", "body-edit"} {
		b.Run(mode, func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "main.bork")
			write := func(n int) {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("%sfn main() { println(%d) }\n", prefix, n)), 0600); err != nil {
					b.Fatal(err)
				}
			}
			write(-1)
			session := NewSession()
			if mode != "cold" {
				if _, err := session.Check(dir); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if mode == "cold" {
					session = NewSession()
				}
				if mode == "body-edit" {
					write(i)
					i++
				}
				if _, err := session.Check(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
