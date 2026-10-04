package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Both arms keep Go context and object caches warm and recheck source edits.
// The fresh arm discards only the proof-result cache before each request.
func BenchmarkSessionProofEdits(b *testing.B) {
	for _, name := range []string{"config", "http_server"} {
		b.Run(name, func(b *testing.B) {
			dir, err := filepath.Abs(filepath.Join("../../examples", name))
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(dir, "main.bork")
			source, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []string{"fresh", "reuse"} {
				b.Run(mode, func(b *testing.B) {
					session := NewSession()
					if _, err := session.Analyze(dir, map[string]string{path: string(source)}); err != nil {
						b.Fatal(err)
					}
					initial := session.Stats()
					var freshMisses uint64
					times := map[string]time.Duration{}
					previous := ""
					since := time.Now()
					session.observe = func(next string) {
						now := time.Now()
						if previous != "" {
							times[previous] += now.Sub(since)
						}
						previous, since = next, now
					}
					b.ResetTimer()
					for i := range b.N {
						if mode == "fresh" {
							session.proofs = &sessionProofCache{memo: newPredicateMemo()}
						}
						text := string(source) + strings.Repeat("\n", 1+i%2)
						if _, err := session.Analyze(dir, map[string]string{path: text}); err != nil {
							b.Fatal(err)
						}
						if mode == "fresh" {
							freshMisses += session.Stats().ProofMisses
						}
					}
					b.StopTimer()
					stats := session.Stats()
					if mode == "fresh" {
						b.ReportMetric(float64(freshMisses)/float64(b.N), "proof-misses/op")
					} else {
						b.ReportMetric(float64(stats.ProofHits-initial.ProofHits)/float64(b.N), "proof-hits/op")
						b.ReportMetric(float64(stats.ProofMisses-initial.ProofMisses)/float64(b.N), "proof-misses/op")
					}
					for phase, duration := range times {
						b.ReportMetric(float64(duration.Nanoseconds())/float64(b.N), phase+"-ns/op")
					}
				})
			}
		})
	}
}
