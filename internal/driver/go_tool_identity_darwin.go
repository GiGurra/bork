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
	var kind []byte
	for _, character := range filesystem.Fstypename {
		if character == 0 {
			break
		}
		kind = append(kind, byte(character))
	}
	if string(kind) == "msdos" || string(kind) == "exfat" {
		return nil
	}
	return &goToolIdentity{
		path: path, device: uint64(stat.Dev), inode: uint64(stat.Ino),
		size: info.Size(), mode: info.Mode(),
		mtimeSec: int64(stat.Mtimespec.Sec), mtimeNsec: int64(stat.Mtimespec.Nsec),
		ctimeSec: int64(stat.Ctimespec.Sec), ctimeNsec: int64(stat.Ctimespec.Nsec),
	}
}
