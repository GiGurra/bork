//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package driver

import (
	"os"
	"syscall"
)

// A regular file has no nonblocking behavior. If the last component is replaced
// by a FIFO between inspection and open, this avoids blocking before Stat can
// reject its changed identity and type.
func openBuildFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
