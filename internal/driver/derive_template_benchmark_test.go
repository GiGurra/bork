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
			newSession := NewSession
			dir := b.TempDir()
			path := filepath.Join(dir, "main.bork")
			write := func(n int) {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("%sfn main() { println(%d) }\n", prefix, n)), 0600); err != nil {
					b.Fatal(err)
				}
			}
			write(-1)
			session := newSession()
			if mode != "cold" {
				if _, err := session.Check(dir); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if mode == "cold" {
					session = newSession()
				}
				if strings.HasPrefix(mode, "body-edit") {
					write(i)
					i++
				}
				if _, err := session.Check(dir); err != nil {
					b.Fatal(err)
				}
			}
			stats := session.Stats()
			b.ReportMetric(float64(stats.ProofMisses), "native-batches")
		})
	}
}

// This workload exercises codec dictionaries and metadata across many types.
// Edits touch only main.bork; models.bork contains 200 distinct record heads.
// Watch mode uses the same session input tracking as an LSP/watch edit loop.
func BenchmarkDeriveManyTypesEdits(b *testing.B) {
	var models strings.Builder
	models.WriteString(`import "bork/shape"
import "bork/codec"
use codec.Defaults
class Labels[T] { fn labels(x: T): List[String] }
derive instance labels[T]: Labels[T] {
 fn labels(x: T): List[String] {
  [comptime for (field in shape.fields[T]()) comptime if (!field.computed) field.name]
 }
}
`)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&models, "type Item%d = { id: Int, name: String, enabled: Bool, tags: List[String], retries: Int = 3, comment: Option[String] = Option.None } derive(codec.Encode, codec.Decode, Labels)\n", i)
	}
	for _, mode := range []string{"emit", "watch"} {
		b.Run(mode, func(b *testing.B) {
			dir := b.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "models.bork"), []byte(models.String()), 0600); err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(dir, "main.bork")
			write := func(n int) {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("fn main() { println(%d) }\n", n)), 0600); err != nil {
					b.Fatal(err)
				}
			}
			session := &Session{watch: mode == "watch"}
			compile := func() {
				var err error
				if session.watch {
					_, err = session.Check(dir)
				} else {
					_, err = session.Emit(dir)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			write(-1)
			compile()
			before := session.Stats()
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				write(i)
				i++
				compile()
			}
			stats := session.Stats()
			b.ReportMetric(float64(stats.ProofMisses-before.ProofMisses)/float64(b.N), "native-batches/op")
		})
	}
}
