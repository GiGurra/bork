//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package driver

import "os"

func openBuildFile(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
