//go:build unix

package driver

import (
	"os"
	"syscall"
)

const canExecProgram = true

func execProgram(executable string, args []string) error {
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}
