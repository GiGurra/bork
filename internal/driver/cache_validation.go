package driver

import (
	"crypto/sha256"
	"slices"

	"github.com/GiGurra/bork/internal/diag"
)

// cachedCompilation contains an owned result, without a checker or AST. It is
// constructed only after all supported receipts have passed fresh validation.
type cachedCompilation struct {
	rootSource  string
	sourcePaths []string
	goSource    []byte
	warnings    []diag.Diagnostic
	module      *goModuleInputs
	context     *goContext
}

func (body *cacheArtifactBody) validate(request cacheArtifactRequest, namespace [sha256.Size]byte) (*cachedCompilation, error) {
	return body.validateWithContext(request, namespace, func(receipt *goContextReceipt) (*goContext, error) { return receipt.restore(resolveGoContext()) })
}

func (body *cacheArtifactBody) validateWithContext(request cacheArtifactRequest, namespace [sha256.Size]byte, restore func(*goContextReceipt) (*goContext, error)) (*cachedCompilation, error) {
	if !body.valid() || body.Namespace != namespace || body.Request != request {
		return nil, errInvalidCacheArtifact
	}
	inputs, err := body.Source.snapshot()
	if err != nil || !inputs.current() {
		return nil, errInvalidCacheArtifact
	}
	// Resolve PATH and raw environment in this process, never from saved fields.
	context, err := restore(body.Go)
	if err != nil {
		return nil, err
	}
	names := make([]*goNameInput, 0, len(body.Names))
	for _, receipt := range body.Names {
		name, err := receipt.restore(context.validation)
		if err != nil {
			return nil, errUnsupportedGoReceipt
		}
		names = append(names, name)
	}
	warnings := make([]diag.Diagnostic, len(body.Warnings))
	for index, warning := range body.Warnings {
		warnings[index] = diag.Diagnostic{Pos: warning.Pos, End: warning.End, Msg: warning.Message, Code: warning.Code, Severity: warning.Severity, Fixes: warning.Fixes}
	}
	result := &cachedCompilation{rootSource: body.RootSource, sourcePaths: slices.Clone(body.SourcePaths), goSource: slices.Clone(body.GoSource), warnings: cloneSessionDiagnostics(warnings), module: &goModuleInputs{mod: slices.Clone(body.Module.Mod), sum: slices.Clone(body.Module.Sum)}, context: context}
	// Recheck after assembling the result. Inputs changed during validation are
	// misses; the normal compiler remains responsible for rebuilding them.
	for _, name := range names {
		if !name.inputs.currentMetadata() {
			return nil, errUnsupportedGoReceipt
		}
	}
	if !inputs.current() || !context.validation.current() {
		return nil, errInvalidCacheArtifact
	}
	return result, nil
}
