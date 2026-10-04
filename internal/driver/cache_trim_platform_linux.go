//go:build linux

package driver

import "os"

func openCacheTrimImage() (*os.File, error)       { return os.Open("/proc/self/exe") }
func cacheTrimExecutable(*os.File) string         { return "/proc/self/fd/3" }
func cacheTrimImageMatches(info os.FileInfo) bool { return sameRunningImage(info) }
func cacheTrimRootDescriptor() string             { return "/proc/self/fd/4" }
func lowerCacheTrimPriority()                     { lowerCachePublisherPriority() }
