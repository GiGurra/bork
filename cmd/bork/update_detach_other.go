//go:build !linux && !darwin && !windows

package main

import "os/exec"

func detachUpdateWorker(*exec.Cmd) {}
