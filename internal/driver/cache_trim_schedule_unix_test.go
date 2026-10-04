//go:build linux || darwin

package driver

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCacheTrimSchedulingGate(t *testing.T) {
	t.Setenv("BORK_CACHE", "on")
	base := t.TempDir()
	now := time.Now()
	if _, err := runCacheTrim(context.Background(), base, now, 256); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := runCacheTrim(context.Background(), base, now, 256); err != nil {
			t.Fatal(err)
		}
	}
	if queueCacheTrim(base) {
		t.Fatal("daily gate launched a worker")
	}
	if err := os.Remove(base + "/trim.json"); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	lease, err := (cacheStore{root: base}).tryLock(root, "trim-admission.lock")
	if err != nil {
		t.Fatal(err)
	}
	if queueCacheTrim(base) {
		t.Fatal("busy admission launched another worker")
	}
	_ = lease.Close()
	t.Setenv("BORK_CACHE", "off")
	if queueCacheTrim(base) {
		t.Fatal("disabled cache launched a worker")
	}
}
