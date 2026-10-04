//go:build linux || darwin

package driver

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func openCacheFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {

	// Root resolves contained symlinks even when O_NOFOLLOW is supplied. Reject
	// final symlinks explicitly and compare the opened descriptor to path evidence.
	before, err := root.Lstat(name)
	if err != nil && (!errors.Is(err, fs.ErrNotExist) || flags&os.O_CREATE == 0) {
		return nil, err
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, errInvalidCacheArtifact
	}
	file, err := root.OpenFile(name, flags|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	after, pathErr := root.Lstat(name)
	// Readers may hold a complete old generation after an atomic replacement.
	// Its descriptor remains inside Root and the checksum/key certify contents;
	// pathname inode equality is only required for mutable coordination files.
	immutableRead := flags == os.O_RDONLY
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() || !immutableRead && (!os.SameFile(opened, after) || before != nil && !os.SameFile(before, opened)) {
		_ = file.Close()
		return nil, errInvalidCacheArtifact
	}
	return file, nil

}
func lockCacheFile(file *os.File) error { return syscall.Flock(int(file.Fd()), syscall.LOCK_EX) }
