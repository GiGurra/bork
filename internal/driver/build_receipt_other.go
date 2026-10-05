//go:build !linux && !darwin

package driver

func buildDirectoryStat(string) (buildDirectoryIdentity, bool) {
	return buildDirectoryIdentity{}, false
}
