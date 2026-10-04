package driver

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

func hashCompilerImage() ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		return digest, fmt.Errorf("%w: %v", errCompilerImageUnavailable, err)
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return digest, errCompilerImageUnavailable
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, fmt.Errorf("%w: %v", errCompilerImageUnavailable, err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return digest, errCompilerImageUnavailable
	}
	// The descriptor names the running inode, even if its pathname was replaced.
	// Linux executable write exclusion supplies byte stability for that image.
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func sameRunningImage(file os.FileInfo) bool {
	running, err := os.Stat("/proc/self/exe")
	return err == nil && os.SameFile(file, running)
}

func openCachePublisherImage() (*os.File, error) { return os.Open("/proc/self/exe") }
func cachePublisherExecutable(*os.File) string   { return "/proc/self/fd/3" }
