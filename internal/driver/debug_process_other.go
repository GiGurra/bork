//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package driver

import "os/exec"

func configureDebugProcess(cmd *exec.Cmd) {}
