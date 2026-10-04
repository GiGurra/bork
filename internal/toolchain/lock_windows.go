package toolchain

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func tryLock(file *os.File, exclusive bool) error {
	var overlapped syscall.Overlapped
	flags := uintptr(1)
	if exclusive {
		flags |= 2
	}
	result, _, err := lockFileEx.Call(file.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result == 0 {
		return err
	}
	return nil
}

func lockBusy(err error) bool { return errors.Is(err, syscall.Errno(33)) }
