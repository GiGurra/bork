//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package driver

import "os/exec"

func configureEvaluationProcess(cmd *exec.Cmd) {}
