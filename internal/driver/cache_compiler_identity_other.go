//go:build !linux

package driver

import "crypto/sha256"

func hashCompilerImage() ([sha256.Size]byte, error) {
	return [sha256.Size]byte{}, errCompilerImageUnavailable
}
