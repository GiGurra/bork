package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCachePublishJobOwnsInputsAndRejectsChanges(t *testing.T) {
	_ = receiptGoContext(t)
	source := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(source, []byte("fn main(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, candidate, err := compileCacheMissUncaptured(source, true, nil)
	if err != nil || candidate == nil {
		t.Fatalf("candidate: %v", err)
	}
	job, err := newCachePublishJob(t.TempDir(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeCachePublishJob(job)
	if err != nil {
		t.Fatal(err)
	}
	candidate.goSrc[0] ^= 1
	candidate.context.values["GOVERSION"] = "changed"
	owned, err := encodeCachePublishJob(job)
	if err != nil || !bytes.Equal(encoded, owned) {
		t.Fatal("job aliases compiler inputs")
	}
	restored, err := decodeCachePublishJob(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.candidate(); err != nil {
		t.Fatalf("restored: %v", err)
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("fn main(){println(2)}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.candidate(); err == nil {
		t.Fatal("equal-mtime source change accepted")
	}
	for _, corrupt := range [][]byte{append(bytes.Clone(encoded), []byte(" trailing")...), bytes.Repeat([]byte("x"), cachePublishJobMaxBytes+1), bytes.Replace(encoded, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)} {
		if _, err := decodeCachePublishJob(bytes.NewReader(corrupt)); err == nil {
			t.Fatal("invalid job accepted")
		}
	}
	job.GoSource = make([]byte, cachePublishJobMaxBytes)
	if _, err := encodeCachePublishJob(job); err == nil {
		t.Fatal("oversized publication job accepted")
	}
}
