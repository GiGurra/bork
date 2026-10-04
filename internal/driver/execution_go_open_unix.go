//go:build unix

package driver

import (
	"os"
	"syscall"
)

// Nonblocking open makes replacement by a FIFO between Stat and Open decline
// immediately once the descriptor's type is checked, instead of hanging.
func openGoExecutionFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
