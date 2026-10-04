//go:build !linux && !darwin

package driver

// Retained caches stay disabled until this platform has both bounded resumable
// maintenance and a detached launcher. There is no inline scan fallback.
func cacheTrimSupported() bool   { return false }
func queueCacheTrim(string) bool { return false }
