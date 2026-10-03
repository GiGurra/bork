package driver

import (
	"os"
	"syscall"
)

func platformGoToolIdentity(path string, info os.FileInfo) *goToolIdentity {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return nil
	}
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystem); err != nil {
		return nil
	}
	// FAT/exFAT do not supply independent Unix change-time evidence.
	if filesystem.Type == 0x4d44 || filesystem.Type == 0x2011bab0 {
		return nil
	}
	return &goToolIdentity{
		path: path, device: uint64(stat.Dev), inode: uint64(stat.Ino),
		size: info.Size(), mode: info.Mode(),
		mtimeSec: int64(stat.Mtim.Sec), mtimeNsec: int64(stat.Mtim.Nsec),
		ctimeSec: int64(stat.Ctim.Sec), ctimeNsec: int64(stat.Ctim.Nsec),
	}
}
