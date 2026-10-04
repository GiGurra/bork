package driver

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionEmitRequiresMain(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "helper.bork")
	if err := os.WriteFile(path, []byte("fn helper() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, cleanError := Emit(path)
	var clean *DiagError
	if !errors.As(cleanError, &clean) {
		t.Fatalf("clean emission: %v", cleanError)
	}
	session := NewSession()
	// Check remains valid for a library package; switching to emission must fail.
	if _, err := session.Check(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		source, err := session.Emit(path)
		var actual *DiagError
		if !errors.As(err, &actual) || !reflect.DeepEqual(actual.Diags.Sorted(), clean.Diags.Sorted()) {
			t.Fatalf("session diagnostics differ: %v", err)
		}
		if source != nil || session.last != nil {
			t.Fatal("failed emission retained")
		}
	}
	if session.Stats().Hits != 0 {
		t.Fatal("failed emission reused")
	}
	if err := os.WriteFile(path, []byte("fn helper() {}\nfn main() { helper() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := session.Emit(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanSource, err := Emit(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, cleanSource) {
		t.Fatal("repaired emission differs")
	}
}
