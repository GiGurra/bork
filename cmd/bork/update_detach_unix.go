//go:build linux || darwin

package main

import (
	"os/exec"
	"syscall"
)

func detachUpdateWorker(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
