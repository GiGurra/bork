//go:build !linux && !darwin

package driver

import "os"

func openCacheFile(_ *os.Root, _ string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, errInvalidCacheArtifact
}
func lockCacheFile(_ *os.File) error { return errInvalidCacheArtifact }
