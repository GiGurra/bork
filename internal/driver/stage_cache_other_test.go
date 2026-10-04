//go:build !linux

package driver

// Keep platform-specific HOME/LocalAppData overrides on their normal paths.
func configureTestStageCache() {}
