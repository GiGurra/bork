package driver

import (
	"os"
	"path/filepath"
)

// Installed SDKs are immutable, as assumed by Go's object cache. Persistent
// toolchain replacements/upgrades invalidate; in-place GOROOT edits (including
// equal-mtime rewrites of SDK files) are unsupported by this hit policy.
type installedSDKFile struct {
	Device, Inode       uint64
	Size                int64
	Mode                os.FileMode
	MtimeSec, MtimeNsec int64
}
type installedSDKIdentity struct {
	Version                   int
	Root, GoVersion, Launcher string
	Tool, VersionFile         installedSDKFile
}

func installedSDKStat(path string) (installedSDKFile, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return installedSDKFile{}, false
	}
	id := platformGoToolIdentity(path, info)
	if id == nil {
		return installedSDKFile{}, false
	}
	return installedSDKFile{Device: id.device, Inode: id.inode, Size: id.size, Mode: id.mode, MtimeSec: id.mtimeSec, MtimeNsec: id.mtimeNsec}, true
}
func captureInstalledSDK(tool, root, version string) *installedSDKIdentity {
	launcher, err := filepath.EvalSymlinks(tool)
	if err != nil || !filepath.IsAbs(root) || !supportedGoVersion(version) {
		return nil
	}
	binary, ok := installedSDKStat(launcher)
	if !ok {
		return nil
	}
	versionFile, ok := installedSDKStat(filepath.Join(root, "VERSION"))
	if !ok {
		return nil
	}
	return &installedSDKIdentity{Version: 1, Root: root, GoVersion: version, Launcher: launcher, Tool: binary, VersionFile: versionFile}
}
func (id *installedSDKIdentity) current(tool, root, version string) bool {
	if id == nil || id.Version != 1 || id.Root != root || id.GoVersion != version {
		return false
	}
	current := captureInstalledSDK(tool, root, version)
	return current != nil && *current == *id
}
