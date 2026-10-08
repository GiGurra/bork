//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package driver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMainResultSignalExit(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", `import "bork/fs"
import "bork/process"
import "bork/time"
fn main(): Ok | fs.Error | Cancelled | process.ExitCode {
 scope s {
  dir = fs.TempDir(s)?
  println(fs.DirectoryPath(dir))
  if (process.Args().isEmpty()) {
   time.Sleep(s, time.Nanoseconds(3600000000000))?
  } else {
   time.Sleep(s, time.Nanoseconds(3600000000000))?{ _ => process.ExitCode { code: 23, message: "chosen" } }
  }
 }
}
`)
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		for _, custom := range []bool{false, true} {
			t.Run(sig.String()+"/custom="+strconv.FormatBool(custom), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var args []string
				if custom {
					args = []string{"code"}
				}
				cmd := exec.CommandContext(ctx, exe, args...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = cmd.Process.Kill() }()
				ready, err := bufio.NewReader(stdout).ReadString('\n')
				if err != nil {
					t.Fatalf("readiness: %v", err)
				}
				path := strings.TrimSpace(ready)
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				err = cmd.Wait()
				var exit *exec.ExitError
				wantCode, wantStderr := 128+int(sig), ""
				if custom {
					wantCode, wantStderr = 23, "error: chosen\n"
				}
				if !errors.As(err, &exit) || exit.ExitCode() != wantCode || stderr.String() != wantStderr {
					t.Fatalf("exit %v, stderr %q; want %d, %q", err, stderr.String(), wantCode, wantStderr)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("scope directory survived signal: %s (%v)", path, err)
				}
			})
		}
	}
}
