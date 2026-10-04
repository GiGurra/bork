package gen

// Keep runtime assertions in both suites, with race instrumentation matching the
// parent test binary. Disable Go test result caching so every child executes.
func runtimeTestArgs() []string {
	args := []string{"test", "-count=1"}
	if testRaceEnabled {
		args = append(args, "-race")
	}
	return append(args, "./...")
}
