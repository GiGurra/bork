//go:build !linux && !darwin

package driver

import (
	"crypto/sha256"
	"os"
)

func hashCompilerImage() ([sha256.Size]byte, error) {
	return [sha256.Size]byte{}, errCompilerImageUnavailable
}

func sameRunningImage(os.FileInfo) bool { return false }
