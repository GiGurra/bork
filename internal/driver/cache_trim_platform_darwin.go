//go:build darwin

package driver

import (
	"os"
	"syscall"
)

func openCacheTrimImage() (*os.File, error)       { return openCachePublisherImage() }
func cacheTrimExecutable(image *os.File) string   { return image.Name() }
func cacheTrimImageMatches(info os.FileInfo) bool { return sameRunningImage(info) }
func cacheTrimRootDescriptor() string             { return "/dev/fd/4" }
func lowerCacheTrimPriority()                     { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19) }
