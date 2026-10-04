package driver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Synthetic neighbours exercise entry-count scaling, not semantic evidence.
// Only the measured target is a real artifact/stage. Setup and cleanup are
// excluded; fresh keys remain published throughout the publication measurement.
func BenchmarkCachePopulation(b *testing.B) {
	b.Setenv("BORK_CACHE", "on")
	b.Setenv("GOPACKAGESDRIVER", "off")
	b.Setenv("GOTOOLCHAIN", "local")
	sourceRoot := b.TempDir()
	path := filepath.Join(sourceRoot, "main.bork")
	if err := os.WriteFile(path, []byte("fn main(){}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	session := NewSession()
	source, err := session.Emit(path)
	if err != nil {
		b.Fatal(err)
	}
	if session.last == nil {
		b.Skipf("ineligible fixture: %+v", session.Stats())
	}
	namespace := sha256.Sum256([]byte("population benchmark"))
	body, err := cacheArtifactFrom(session.last, session.last.sourcePaths, namespace)
	if err != nil {
		b.Fatal(err)
	}
	meta := goStageMetadata{Schema: goStageSchema, Program: sourceRoot, Mode: "population-benchmark", Namespace: session.last.context.namespace}
	for _, count := range []int{1, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("entries%d", count), func(b *testing.B) {
			base := b.TempDir()
			store := cacheStore{root: base, namespace: namespace}
			for i := range count - 1 {
				key := sha256.Sum256([]byte(fmt.Sprintf("neighbour%d", i)))
				name := fmt.Sprintf("%x", key)
				stage := filepath.Join(base, goStageEntryPath(name))
				if err := os.MkdirAll(filepath.Join(stage, "tree"), 0700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stage, "used"), nil, 0600); err != nil {
					b.Fatal(err)
				}
				result := filepath.Join(base, store.path(key))
				if err := os.MkdirAll(filepath.Dir(result), 0700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(result, []byte("synthetic unrelated result"), 0600); err != nil {
					b.Fatal(err)
				}
				index := filepath.Join(base, cacheIndexPath(key))
				if err := os.MkdirAll(filepath.Dir(index), 0700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(index, []byte(fmt.Sprintf("%x", namespace)), 0600); err != nil {
					b.Fatal(err)
				}
			}
			if err := store.write(body); err != nil {
				b.Fatal(err)
			}
			stageKey := fmt.Sprintf("%x", sha256.Sum256([]byte("measured-stage")))
			dir, _, release, err := stageGoStable(base, stageKey, source, session.last.module, nil, meta)
			if err != nil {
				b.Fatal(err)
			}
			release()
			nextResult, nextStage := 0, 0
			cases := []struct {
				name string
				run  func(int) error
			}{
				{"result-lookup", func(_ int) error {
					if findCacheArtifact(base, body.Request) == nil {
						return fmt.Errorf("lookup miss")
					}
					return nil
				}},
				{"result-validated-hit", func(_ int) error {
					hit := findCacheArtifact(base, body.Request)
					if hit == nil {
						return fmt.Errorf("hit miss")
					}
					_, err := hit.validateWithContext(body.Request, namespace, func(receipt *goContextReceipt) (*goContext, error) {
						return receipt.restoreInstalledSDK(resolveGoContext())
					})
					if err == nil {
						store.touch(body.Key)
					}
					return err
				}},
				{"result-new-key-publication", func(_ int) error {
					i := nextResult
					nextResult++
					fresh := *body
					fresh.Request.Path = filepath.Join(sourceRoot, fmt.Sprintf("new%d.bork", i))
					fresh.Key, _ = fresh.Request.key()
					return store.write(&fresh)
				}},
				{"stage-direct-path-lookup", func(_ int) error { _, err := os.Stat(filepath.Join(dir, "main.go")); return err }},
				{"stage-repeat-publication", func(_ int) error {
					_, _, release, err := stageGoStable(base, stageKey, source, session.last.module, nil, meta)
					if err == nil {
						release()
					}
					return err
				}},
				{"stage-new-key-publication", func(_ int) error {
					i := nextStage
					nextStage++
					key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("new-stage%d", i))))
					_, _, release, err := stageGoStable(base, key, source, session.last.module, nil, meta)
					if err == nil {
						release()
					}
					return err
				}},
			}
			for _, item := range cases {
				b.Run(item.name, func(b *testing.B) {
					b.ReportAllocs()
					i := 0
					for b.Loop() {
						if err := item.run(i); err != nil {
							b.Fatal(err)
						}
						i++
					}
				})
			}
			// An unfinished cycle with a live worker must do only bounded state reads
			// and a nonblocking admission attempt, regardless of its remaining size.
			state, err := json.Marshal(cacheTrimState{Version: 2, Layer: 1, Shard: 73, Cutoff: time.Now().Add(-cacheTrimAge)})
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "trim.json"), state, 0600); err != nil {
				b.Fatal(err)
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				b.Fatal(err)
			}
			lease, err := store.tryLock(root, "trim-admission.lock")
			if err != nil {
				b.Fatal(err)
			}
			b.Run("trim-mid-cycle-busy-admission", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if queueCacheTrim(base) {
						b.Fatal("workers piled up behind admission")
					}
				}
			})
			_ = lease.Close()
			_ = root.Close()
			b.Run("trim-fresh-whole-cycle", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					b.StopTimer()
					_ = os.Remove(filepath.Join(base, "trim.json"))
					b.StartTimer()
					for {
						report, err := runCacheTrim(context.Background(), base, time.Now(), 256)
						if err != nil && !errors.Is(err, context.DeadlineExceeded) {
							b.Fatal(err)
						}
						if report.Complete {
							break
						}
					}
				}
			})
		})
	}
}
