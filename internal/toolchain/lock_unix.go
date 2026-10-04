//go:build linux || darwin

package toolchain

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(file *os.File, exclusive bool) error {
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	return syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
}

func lockBusy(err error) bool { return errors.Is(err, syscall.EWOULDBLOCK) }
