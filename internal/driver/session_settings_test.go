package driver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSessionOwnedSettings(t *testing.T) {
	t.Parallel()
	ambient := os.Environ()
	settings := []string{"GOPACKAGESDRIVER=off"}
	session := newOwnedFixtureSession(settings...)
	settings[0] = "GOPACKAGESDRIVER=changed"
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := session.Check(path); err != nil {
			t.Fatal(err)
		}
	}
	if session.Stats().Hits != 1 || session.last.context.driver != "off" {
		t.Fatalf("owned settings: %+v", session.Stats())
	}
	if !slices.Equal(ambient, os.Environ()) {
		t.Fatal("Session changed process environment")
	}
	// A different per-call effective setting must still invalidate the previous
	// context; the fast validation path must not discard fixture overrides.
	next := captureSessionGoContextWithSettings(session.last.context, []string{"GOPACKAGESDRIVER=off", "GOFLAGS=-tags=owned"})
	if next == session.last.context || next.namespace == session.last.context.namespace || next.values["GOFLAGS"] != "-tags=owned" {
		t.Fatal("changed effective setting reused previous context")
	}
}
