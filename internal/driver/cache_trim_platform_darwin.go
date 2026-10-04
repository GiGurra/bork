//go:build darwin

package driver

import (
	"os"
	"syscall"
)

func openCacheTrimImage() (*os.File, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}
func cacheTrimExecutable(image *os.File) string { return image.Name() }
func cacheTrimImageMatches(info os.FileInfo) bool {
	path, err := os.Executable()
	if err != nil {
		return false
	}
	running, err := os.Stat(path)
	return err == nil && running.Mode().IsRegular() && os.SameFile(info, running)
}
func cacheTrimRootDescriptor() string { return "/dev/fd/4" }
func lowerCacheTrimPriority()         { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19) }
