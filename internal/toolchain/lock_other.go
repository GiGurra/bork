//go:build !linux && !darwin && !windows

package toolchain

import (
	"errors"
	"os"
)

func tryLock(_ *os.File, _ bool) error {
	return errors.New("compiler switching is unsupported on this platform")
}
func lockBusy(_ error) bool { return false }
