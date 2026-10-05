package driver

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPMultiStartupFailure(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		t.Skipf("cannot reserve the example's API port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	exe, err := buildFixtureOutput(t, filepath.Join("..", "..", "examples", "http_multi"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "serve").CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("startup failure must exit 1: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "localhost:8080:") || !strings.Contains(string(out), "address already in use") {
		t.Fatalf("startup failure must report the occupied address: %s", out)
	}
}
