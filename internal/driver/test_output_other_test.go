//go:build !linux

package driver

import (
	"path/filepath"
	"testing"
)

func fixtureOutputPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "program")
}
