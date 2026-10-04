//go:build linux

package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const executableCopierSource = `package main
import("io";"os";"fmt")
func main(){
 if len(os.Args)==1 {p,err:=os.Executable();if err!=nil {os.Exit(1)};fmt.Print(p);return}
 if len(os.Args)!=4 {os.Exit(2)}
 if err:=copyFile(os.Args[1],os.Args[2]);err!=nil {
  _=os.WriteFile(os.Args[3],[]byte("copy-failed"),0600)
  fmt.Fprintln(os.Stderr,err);os.Exit(1)
 }
}
func copyFile(source,destination string)error {
 in,err:=os.Open(source);if err!=nil {return err};defer in.Close()
 out,err:=os.OpenFile(destination,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0755)
 if err!=nil {return err}
 _,err=io.Copy(out,in);closed:=out.Close();if err!=nil {_=os.Remove(destination);return err};if closed!=nil {_=os.Remove(destination)};return closed
}
`

func configureCachedTestLinks() {
	if runtime.GOOS != "linux" {
		return
	}
	native := captureGoContext()
	if native.err != nil || !cachedLinkVersion(native.values["GOVERSION"]) {
		return
	}
	cache, err := goStageCacheDir()
	if err != nil {
		return
	}
	base, err := privateTestCacheDir(filepath.Join(cache, "link-helper-v1"))
	if err != nil {
		return
	}
	source := filepath.Join(base, "copy.go")
	if atomicHelperFile(source, []byte(executableCopierSource)) != nil ||
		atomicHelperFile(filepath.Join(base, "go.mod"), []byte("module bork.test.copy\n\ngo 1.24\n")) != nil {
		return
	}
	// Helper setup is optional; it must never stall the suite.
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	boot := exec.CommandContext(deadline, native.tool, "run", "-mod=readonly", "-buildvcs=false", source)
	boot.WaitDelay = time.Second
	boot.Dir = base
	boot.Env = append(slices.Clone(native.env), "GOWORK=off", "GOFLAGS=")
	path, err := boot.Output()
	if err != nil {
		return
	}
	copier := strings.TrimSpace(string(path))
	if !filepath.IsAbs(copier) {
		return
	}
	goBuildCommandHook = cachedTestLinkFactory(native, copier)
}

// Go owns the executable cache. Only fresh test outputs are copied; neither
// staging nor the result store retains native executables. A future execution
// receipt collector must observe these effective run/debug flags or decline
// the hook rather than certify the ordinary build contract.
func cachedTestLinkFactory(native *goContext, copier string) func(*goContext, string, *exec.Cmd) (*exec.Cmd, func([]byte) bool, func()) {
	bridge, ok := goExecCommand([]string{copier})
	if !ok {
		return nil
	}

	return func(ctx *goContext, mode string, normal *exec.Cmd) (*exec.Cmd, func([]byte) bool, func()) {
		if normal.Cancel != nil || normal.Stdin != nil || normal.Stdout != nil || normal.Stderr != nil || normal.WaitDelay != 0 {
			return nil, nil, nil
		}
		if mode != "predicate" && mode != "comptime" && mode != "test" {
			return nil, nil, nil
		}
		if ctx.tool != native.tool || ctx.toolDigest != native.toolDigest || ctx.driver != native.driver {
			return nil, nil, nil
		}
		if ctx.values["GOOS"] != runtime.GOOS || ctx.values["GOARCH"] != runtime.GOARCH {
			return nil, nil, nil
		}
		if ctx.values["GOCACHE"] == "off" || ctx.values["GOCACHEPROG"] != "" || !cachedLinkVersion(ctx.values["GOVERSION"]) {
			return nil, nil, nil
		}
		if len(normal.Args) != 7 || normal.Args[1] != "build" || normal.Args[2] != "-mod=readonly" || normal.Args[3] != "-buildvcs=false" || normal.Args[4] != "-o" || normal.Args[6] != "." {
			return nil, nil, nil
		}
		out := normal.Args[len(normal.Args)-2]
		if _, err := os.Lstat(out); !os.IsNotExist(err) {
			return nil, nil, nil
		}
		if info, err := os.Stat(copier); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil, nil, nil
		}
		status, err := os.CreateTemp(filepath.Dir(out), ".copy-result-")
		if err != nil {
			return nil, nil, nil
		}
		statusPath := status.Name()
		_ = status.Close()
		cleanup := func() { _ = os.Remove(statusPath) }
		cmd := ctx.command("run", "-mod=readonly", "-buildvcs=false", "-gcflags=-dwarf=true", "-ldflags=-s=false -w=false", "-exec", bridge, ".", out, statusPath)
		cmd.Dir = normal.Dir
		cmd.Env = slices.Clone(normal.Env)
		return cmd, func(_ []byte) bool {
			data, _ := os.ReadFile(statusPath)
			if string(data) == "copy-failed" {
				return true
			}
			info, err := os.Stat(copier)
			return err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0
		}, cleanup
	}
}

func goExecCommand(args []string) (string, bool) {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\r\n\"'") {
			if !strings.ContainsRune(arg, '\'') {
				arg = "'" + arg + "'"
			} else if !strings.ContainsRune(arg, '"') {
				arg = "\"" + arg + "\""
			} else {
				return "", false
			}
		}
		quoted[i] = arg
	}
	return strings.Join(quoted, " "), true
}

func cachedLinkVersion(version string) bool {
	if !strings.HasPrefix(version, "go1.") {
		return false
	}
	minor := strings.Split(strings.TrimPrefix(version, "go1."), ".")[0]
	n, err := strconv.Atoi(minor)
	return err == nil && n >= 24
}

// Publish immutable helper inputs atomically across concurrent test processes.
func atomicHelperFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "helper-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
