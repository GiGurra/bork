package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGoToolIdentityInvalidatesChanges(t *testing.T) {
	for _, change := range []string{"rewrite", "replace", "chmod", "remove"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go")
			if err := os.WriteFile(path, []byte("first"), 0o755); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			digest, identity, err := captureGoToolEvidence(path)
			if err != nil {
				t.Fatal(err)
			}
			if identity.identity == nil {
				t.Skip("filesystem identity unavailable")
			}
			validation := &goContextValidation{launcher: path, toolDigest: digest, toolEvidence: identity}
			if !validation.toolCurrent() {
				t.Fatal("unchanged launcher invalidated")
			}
			switch change {
			case "rewrite":
				err = os.WriteFile(path, []byte("other"), 0o755)
			case "replace":
				other := path + ".replacement"
				// Identical contents and restored mtime still change inode/ctime.
				if err = os.WriteFile(other, []byte("first"), 0o755); err == nil {
					err = os.Rename(other, path)
				}
			case "chmod":
				err = os.Chmod(path, 0o644)
			case "remove":
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if change != "remove" {
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if change == "replace" {
				if !validation.toolCurrent() || *identity.identity == *platformGoToolIdentity(path, before) {
					t.Fatal("identical replacement did not rehash and refresh evidence")
				}
			} else if validation.toolCurrent() {
				t.Fatal("launcher change reused digest")
			}
		})
	}
}

func TestGoToolDigestFallbackDetectsEqualMtimeEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, evidence, err := captureGoToolEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Force the portable no-identity fallback, including its mode evidence.
	evidence.identity = nil
	evidence.stableSince = time.Now().Add(-goToolTimestampMargin - time.Second)
	validation := &goContextValidation{launcher: path, toolDigest: digest, toolEvidence: evidence}
	if !validation.toolCurrent() {
		t.Fatal("unchanged fallback invalidated")
	}
	if err := os.WriteFile(path, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if validation.toolCurrent() {
		t.Fatal("equal-size/equal-mtime edit reused fallback digest")
	}
}

func TestGoToolIdentityFollowsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges vary on Windows")
	}
	root := t.TempDir()
	first, second, link := filepath.Join(root, "first"), filepath.Join(root, "second"), filepath.Join(root, "go")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("same!"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	digest, identity, err := captureGoToolEvidence(link)
	if err != nil {
		t.Fatal(err)
	}
	if identity.identity == nil {
		t.Skip("filesystem identity unavailable")
	}
	validation := &goContextValidation{launcher: link, toolDigest: digest, toolEvidence: identity}
	if !validation.toolCurrent() {
		t.Fatal("unchanged symlink invalidated")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	old := *identity.identity
	if !validation.toolCurrent() || *identity.identity == old {
		t.Fatal("identical symlink target did not refresh evidence")
	}
}

func TestGoToolIdentityRehashesRacyMetadataMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, evidence, err := captureGoToolEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.identity == nil {
		t.Skip("filesystem identity unavailable")
	}
	if err := os.WriteFile(path, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Model a coarse timestamp collision by supplying the current tuple with
	// the earlier bytes' digest. Its recent observation must force hashing.
	evidence.identity = platformGoToolIdentity(path, info)
	evidence.stableSince = time.Now()
	validation := &goContextValidation{launcher: path, toolDigest: digest, toolEvidence: evidence}
	if validation.toolCurrent() {
		t.Fatal("racy metadata match hid changed bytes")
	}
}

func TestGoToolIdentityStableObservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, evidence, err := captureGoToolEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.identity == nil {
		t.Skip("filesystem identity unavailable")
	}
	validation := &goContextValidation{launcher: path, toolDigest: digest, toolEvidence: evidence}
	observed := evidence.stableSince
	if !validation.toolCurrent() || evidence.stableSince != observed {
		t.Fatal("identical-byte reread reset observation period")
	}
	// Established observations can reuse metadata. The fake expected digest
	// proves this takes the stat path; a recent observation must hash instead.
	evidence.stableSince = time.Now().Add(-goToolTimestampMargin - time.Second)
	validation.toolDigest = [32]byte{}
	if !validation.toolCurrent() {
		t.Fatal("stable metadata did not reuse digest")
	}
	evidence.stableSince = time.Now()
	if validation.toolCurrent() {
		t.Fatal("recent metadata did not rehash")
	}
}
