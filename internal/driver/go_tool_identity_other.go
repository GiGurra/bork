//go:build !linux && !darwin

package driver

import "os"

func platformGoToolIdentity(string, os.FileInfo) *goToolIdentity { return nil }
