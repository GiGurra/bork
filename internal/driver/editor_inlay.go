package driver

import (
	"fmt"

	"github.com/GiGurra/bork/internal/check"
)

func (a *EditorAnalysis) EditorInlays(path string, options check.EditorInlayOptions) ([]check.EditorInlay, error) {
	file, from := a.editorFile(path)
	if file == nil {
		return nil, fmt.Errorf("source is outside the checked package")
	}
	return check.EditorInlays(a.program.info, file, from, options), nil
}
