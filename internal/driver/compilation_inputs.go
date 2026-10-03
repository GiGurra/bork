package driver

import "fmt"

// Capture source and manifests before semantic checking. The inventory covers
// only these inputs; assets, effective Go configuration and execution context
// still need capture before any Session result can be reused.
func loadCompilationInputs(path string, observe func(string)) (*loadedSources, *goModuleInputs, error) {
	return loadCompilationInputsFrom(path, observe, newSourceSnapshot)
}

func loadCompilationInputsFrom(path string, observe func(string), capture func() *sourceSnapshot) (*loadedSources, *goModuleInputs, error) {
	for range 2 {
		phase(observe, "parse")
		inputs := capture()
		files, root, diags, err := loadFrom(path, inputs)
		var module *goModuleInputs
		if err == nil {
			if diags.Len() != 0 {
				err = &DiagError{Diags: diags}
			} else {
				phase(observe, "module")
				module, err = captureGoModule(files, inputs)
			}
		}
		if !inputs.current() {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		return &loadedSources{files, root, diags, inputs}, module, nil
	}
	return nil, nil, fmt.Errorf("source or Go manifest inputs changed while loading; retry the command")
}
