package driver

import (
	"os"
	"testing"
)

// buildFixtureOutput preserves public Build coverage and fresh execution while
// letting Go validate an existing target with its own build action identity.
func buildFixtureOutput(t *testing.T, dir string) (string, error) {
	t.Helper()
	out := fixtureOutputPath(t)
	return out, buildFixtureAt(dir, out)
}

func buildFixtureAt(dir, out string) error {
	err := Build(dir, out)
	if err != nil {
		// A failed or interrupted replacement must not leave a runnable old target.
		_ = os.Remove(out)
	}
	return err
}
