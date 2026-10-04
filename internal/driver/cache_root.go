package driver

import (
	"path/filepath"

	"github.com/GiGurra/bork/internal/toolenv"
)

// cacheRootDir selects the complete cache root for results, staging and cleaning.
// The harness seam supplies the default parent only; explicit settings win.
func cacheRootDir() (string, error) {
	setting, err := toolenv.Lookup("BORKCACHE")
	if err != nil {
		return "", err
	}
	if setting.Source != "default" {
		return setting.Value, nil
	}
	base, err := goStageCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "bork"), nil
}

func cacheDisabled() bool {
	value, err := toolenv.Value("BORK_CACHE")
	// An unreadable or invalid configuration disables reuse; fresh compilation
	// remains available. env and clean report the configuration error directly.
	return err != nil || value == "off"
}
