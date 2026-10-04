package driver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/gen"
)

// BenchmarkGoStageAccounting isolates cache inventory and stage publication
// from Bork checking and Go subprocesses. Other entries contain small published
// trees so the entry count measures inventory work rather than payload size.
func BenchmarkGoStageAccounting(b *testing.B) {
	for _, item := range []struct{ name, path string }{
		{"config", "../../examples/config"},
		{"http_server", "../../examples/http_server"},
	} {
		b.Run(item.name, func(b *testing.B) {
			program, err := checkProgramObserved(item.path, nil)
			if err != nil {
				b.Fatal(err)
			}
			source, err := gen.Package(program.files, program.info)
			if err != nil {
				b.Fatal(err)
			}
			programRoot, err := goStageProgramRoot(program.files)
			if err != nil {
				b.Fatal(err)
			}
			metadata := goStageMetadata{Schema: goStageSchema, Program: programRoot, Mode: "accounting-benchmark", Namespace: program.context.namespace}
			meta, err := json.Marshal(metadata)
			if err != nil {
				b.Fatal(err)
			}
			for _, count := range []int{1, 128, 512, 1024} {
				b.Run(fmt.Sprintf("entries%d", count), func(b *testing.B) {
					base := b.TempDir()
					// The measured target is the final entry after priming.
					for index := range count - 1 {
						key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(index))))
						entry := filepath.Join(base, "stage", "v2", key)
						if err := os.MkdirAll(filepath.Join(entry, "tree"), 0700); err != nil {
							b.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(entry, "metadata.json"), meta, 0600); err != nil {
							b.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(entry, "tree", "main.go"), []byte("package main\nfunc main(){}\n"), 0600); err != nil {
							b.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(entry, "tree", "go.mod"), []byte("module example.com/stage\n\ngo 1.24\n"), 0600); err != nil {
							b.Fatal(err)
						}
					}
					key := fmt.Sprintf("%x", sha256.Sum256([]byte("measured-stage")))
					_, _, release, err := stageGoStable(base, key, source, program.module, program.info.Embeds, metadata)
					if err != nil {
						b.Fatal(err)
					}
					release()
					b.ReportAllocs()
					for b.Loop() {
						_, _, release, err := stageGoStable(base, key, source, program.module, program.info.Embeds, metadata)
						if err != nil {
							b.Fatal(err)
						}
						release()
					}
					b.ReportMetric(float64(len(source)), "go-bytes")
				})
			}
		})
	}
}
