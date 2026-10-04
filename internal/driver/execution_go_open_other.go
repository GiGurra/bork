//go:build !unix

package driver

import "os"

func openGoExecutionFile(path string) (*os.File, error) { return os.Open(path) }
