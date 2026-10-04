package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedArtifactCurrent(t *testing.T) {
	const runtimePath = "../../../stdvalidators/runtime.go"
	const generatedPath = "../../../stdvalidators/generated.go"
	output := filepath.Join(t.TempDir(), "generated.go")
	savedArgs := os.Args
	os.Args = []string{"genstdvalidators", runtimePath, output}
	t.Cleanup(func() { os.Args = savedArgs })
	if err := run(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("standard validator artifact is stale; run go generate ./internal/stdvalidators")
	}
}
