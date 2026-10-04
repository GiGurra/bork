//go:build linux || darwin

package toolchain

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func lockBusy(err error) bool { return errors.Is(err, syscall.EWOULDBLOCK) }
