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
	exe, err := buildFixtureOutput(t, filepath.Join("..", "..", "examples", "http_multi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{":8080", "127.0.0.1:9090"} {
		t.Run(address, func(t *testing.T) {
			if address != ":8080" {
				probe, err := net.Listen("tcp", ":8080")
				if err != nil {
					t.Skipf("cannot reserve the example's API port: %v", err)
				}
				_ = probe.Close()
			}
			listener, err := net.Listen("tcp", address)
			if err != nil {
				t.Skipf("cannot reserve the example's port: %v", err)
			}
			defer func() { _ = listener.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, exe, "serve").CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("startup failure must exit 1: %v\n%s", err, out)
			}
			if !strings.Contains(string(out), address+":") || !strings.Contains(string(out), "address already in use") {
				t.Fatalf("startup failure must report the occupied address: %s", out)
			}
			if address != ":8080" {
				probe, err := net.Listen("tcp", ":8080")
				if err != nil {
					t.Fatalf("partially started API listener was not released: %v", err)
				}
				_ = probe.Close()
			}
		})
	}
}
