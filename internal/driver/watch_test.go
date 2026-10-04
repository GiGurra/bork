package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchRecoversAndTracksEqualMtimeEdits(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	before := []byte("fn main() { println(missing) }\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var statuses []string
	err := Watch(ctx, path, WatchOptions{Interval: 10 * time.Millisecond}, func(result WatchResult) error {
		statuses = append(statuses, result.Status)
		if result.SchemaVersion != 1 || result.RequestID != uint64(len(statuses)) {
			t.Fatalf("bad result: %+v", result)
		}
		if len(statuses) == 1 {
			if result.Status != "error" || len(result.Diagnostics) == 0 {
				t.Fatalf("missing compiler error: %+v", result)
			}
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fn main() { println(1234567) }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
				t.Fatal(err)
			}
		} else {
			if result.Status != "ok" {
				t.Fatalf("repair failed: %+v", result)
			}
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || len(statuses) != 2 {
		t.Fatalf("results %v, error %v", statuses, err)
	}
}

func TestWatchMissingRootAppears(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var count int
	err := Watch(ctx, path, WatchOptions{Interval: 10 * time.Millisecond}, func(result WatchResult) error {
		count++
		if count == 1 {
			if result.Status != "error" {
				t.Fatalf("missing path succeeded: %+v", result)
			}
			if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			if result.Status != "ok" {
				t.Fatalf("new source failed: %+v", result)
			}
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 2 {
		t.Fatalf("count %d, error %v", count, err)
	}
}

func TestWatchManualRetrigger(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	trigger := make(chan struct{}, 1)
	var count int
	err := Watch(ctx, path, WatchOptions{Interval: time.Hour, Retrigger: trigger}, func(result WatchResult) error {
		count++
		if result.Status != "ok" {
			t.Fatalf("failed: %+v", result)
		}
		if count == 1 {
			trigger <- struct{}{}
		} else {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 2 {
		t.Fatalf("count %d, error %v", count, err)
	}
}

func TestWatchAttemptRetainsBypassedInputs(t *testing.T) {
	t.Parallel()
	session := NewSession()
	session.watch = true
	if _, err := session.Check("../../examples/config"); err != nil {
		t.Fatal(err)
	}
	if session.Stats().Bypasses != 1 || session.attempt == nil || !session.attempt.current() {
		t.Fatalf("missing bypass inventory: %+v", session.Stats())
	}
	// A current bypass inventory is a trigger hint, not a reusable result.
	if session.last != nil {
		t.Fatal("bypassed program gained a result artifact")
	}
}

func TestWatchCancellationDuringValidationDoesNotPublish(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	published, err := publishWatchResult(ctx, WatchResult{}, func() bool {
		cancel()
		return true
	}, func(WatchResult) error {
		t.Fatal("cancelled validation published a result")
		return nil
	})
	if published || !errors.Is(err, context.Canceled) {
		t.Fatalf("published=%v, err=%v", published, err)
	}
}

func TestWatchBypassDoesNotRecompileWhilePolling(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session := NewSession()
	var publications, reads int
	err := watchSession(ctx, "../../examples/config", WatchOptions{Interval: time.Millisecond}, func(result WatchResult) error {
		publications++
		if result.Status != "ok" || session.Stats().Bypasses != 1 {
			t.Fatalf("missing bypass: %+v", session.Stats())
		}
		snapshot := session.attempt.inputs
		snapshot.disk = notifyingWatchSources{snapshot.disk, func() {
			reads++
			if reads >= 5 {
				cancel()
			}
		}}
		return nil
	}, session)
	if !errors.Is(err, context.Canceled) || publications != 1 || reads < 5 || session.Stats().Misses != 1 {
		t.Fatalf("idle watcher: publications=%d reads=%d stats=%+v error=%v", publications, reads, session.Stats(), err)
	}
}

type notifyingWatchSources struct {
	sourceReader
	onRead func()
}

func (sources notifyingWatchSources) readFile(path string) ([]byte, error) {
	sources.onRead()
	return sources.sourceReader.readFile(path)
}

func TestWatchMissingAssetRecovers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	if err := os.WriteFile(path, []byte("import \"bork/embed\"\nfn main() { println(embed.ReadString(\"asset\")) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var count int
	err := Watch(ctx, path, WatchOptions{Interval: 10 * time.Millisecond}, func(result WatchResult) error {
		count++
		if count == 1 {
			if result.Status != "error" {
				t.Fatalf("missing asset succeeded: %+v", result)
			}
			if err := os.WriteFile(filepath.Join(root, "asset"), []byte("new"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			if result.Status != "ok" {
				t.Fatalf("new asset failed: %+v", result)
			}
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 2 {
		t.Fatalf("count %d, error %v", count, err)
	}
}

func TestWatchIncludesMigrationWarning(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn success(): Unit { Ok }\nfn main() { success() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var count int
	err := Watch(ctx, path, WatchOptions{}, func(result WatchResult) error {
		count++
		if result.Status != "ok" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "migration.unit" || len(result.Diagnostics[0].Fixes) != 1 {
			t.Fatalf("missing warning/fix: %+v", result)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("count %d, error %v", count, err)
	}
}

func TestWatchPendingManualRequestWithholdsOldResult(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{}
	var count int
	err := Watch(ctx, path, WatchOptions{Interval: time.Hour, Retrigger: trigger}, func(result WatchResult) error {
		count++
		if result.Status != "ok" || result.RequestID != 2 {
			t.Fatalf("obsolete result published: %+v", result)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("count %d, error %v", count, err)
	}
}
