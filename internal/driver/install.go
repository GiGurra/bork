package driver

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/GiGurra/bork/internal/toolenv"
)

// Install builds into BORKBIN, replacing an existing executable only on success.
func Install(path string) error {
	bin, err := toolenv.Value("BORKBIN")
	if err != nil {
		return err
	}
	name := DefaultOutput(path)
	if name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("cannot determine executable name for %q", path)
	}
	program, source, err := emitProgramObserved(path, nil)
	if err != nil {
		return err
	}
	if program.context.values["GOOS"] == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(bin, ".bork-install-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	if err := buildGoWithContext(program.files, source, temporary, program.module, program.context, program.info.Embeds...); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(bin, name))
}
