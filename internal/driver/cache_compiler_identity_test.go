package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCompilerArtifactNamespace(t *testing.T) {
	digest, err := hashCompilerImage()
	if runtime.GOOS != "linux" {
		if !errors.Is(err, errCompilerImageUnavailable) {
			t.Fatalf("unsupported platform identity: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if digest != sha256.Sum256(data) {
		t.Fatal("identity differs from running image bytes")
	}
	namespace, err := compilerArtifactNamespace("receipt-v1", "output-v1")
	if err != nil {
		t.Fatal(err)
	}
	if namespace != compilerNamespaceDigest(digest, "receipt-v1", "output-v1") {
		t.Fatal("namespace differs")
	}
	changed := digest
	changed[0] ^= 1
	for _, alternate := range [][sha256.Size]byte{
		compilerNamespaceDigest(changed, "receipt-v1", "output-v1"),
		compilerNamespaceDigest(digest, "receipt-v2", "output-v1"),
		compilerNamespaceDigest(digest, "receipt-v1", "output-v2"),
	} {
		if alternate == namespace {
			t.Fatal("namespace missed identity component")
		}
	}
	if compilerNamespaceDigest(digest, "a", "bc") == compilerNamespaceDigest(digest, "ab", "c") {
		t.Fatal("ambiguous namespace")
	}
}

func TestCompilerIdentityRunningInode(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("running image access is Linux-only")
	}
	if ready := os.Getenv("BORK_IDENTITY_HELPER_READY"); ready != "" {
		before, err := hashCompilerImage()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ready, []byte(hex.EncodeToString(before[:])), 0600); err != nil {
			t.Fatal(err)
		}
		var byte [1]byte
		if _, err := os.Stdin.Read(byte[:]); err != nil {
			t.Fatal(err)
		}
		after, err := hashCompilerImage()
		if err != nil || after != before {
			t.Fatalf("running image identity changed: %v", err)
		}
		return
	}
	root := t.TempDir()
	program := filepath.Join(root, "compiler")
	ready := filepath.Join(root, "ready")
	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, self, 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(program, "-test.run=^TestCompilerIdentityRunningInode$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "BORK_IDENTITY_HELPER_READY="+ready)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The ready file is only a handshake; use the existing polling helper below.
	output := &identityOutput{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	if err := waitIdentityReady(ready); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(program, program+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("replacement image"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("helper: %v\n%s", err, output.String())
	}
}

type identityOutput struct {
	sync.Mutex
	data []byte
}

func (o *identityOutput) Write(data []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	o.data = append(o.data, data...)
	return len(data), nil
}
func (o *identityOutput) String() string { o.Lock(); defer o.Unlock(); return string(o.data) }

func waitIdentityReady(path string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("compiler identity helper did not start")
}
