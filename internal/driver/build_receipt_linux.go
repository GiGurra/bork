package driver

import (
	"os"
	"syscall"
)

func buildDirectoryStat(path string) (buildDirectoryIdentity, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return buildDirectoryIdentity{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return buildDirectoryIdentity{}, false
	}
	return buildDirectoryIdentity{uint64(stat.Dev), uint64(stat.Ino), info.Mode(), int64(stat.Mtim.Sec), int64(stat.Mtim.Nsec), int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec)}, true
}
