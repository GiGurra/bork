package driver

import (
	"maps"
	"runtime"
	"slices"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

// compileCacheMiss uses the ordinary one-shot pipeline. Usage observations do
// not capture SDK or configuration inventories until bypass eligibility is known.
func compileCacheMiss(path string, emit bool, observe func(string)) ([]byte, []diag.Diagnostic, *sessionArtifact, error) {
	defer phase(observe, "")
	loaded, module, err := loadCompilationInputs(path, observe)
	if err != nil {
		return nil, nil, nil, err
	}
	phase(observe, "configuration")
	context := captureGoContext()
	usage := &goUsage{deferInputs: true}
	program, err := checkLoadedProgramTracked(loaded, module, context, captureEmbedsSnapshot, usage, observe)
	if err != nil {
		return nil, nil, nil, err
	}
	phase(observe, "warnings")
	warnings := check.DebugWarnings(program.info)
	warnings.Append(check.LazyWarnings(program.info))
	warnings.Append(check.MigrationWarnings(program.info))
	warningData := warnings.Sorted()
	var src []byte
	if emit {
		if err := program.requireMain(); err != nil {
			return nil, nil, nil, err
		}
		phase(observe, "generate")
		src, err = gen.Package(program.files, program.info)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	// These paths stop before any cache configuration/SDK inventory capture.
	if sessionBypassReason(context, usage) != "" || !supportedCacheMissSettings(context) || program.inputs.usesDriveContext() {
		return src, warningData, nil, nil
	}
	program.assets.mu.Lock()
	hasAssets := len(program.assets.reads) != 0
	program.assets.mu.Unlock()
	if hasAssets {
		return src, warningData, nil, nil
	}
	phase(observe, "eligible-inventory")
	// Late inventory cannot certify earlier metadata blindly. Re-query names
	// between captured SDK observations and compare values actually used by checking.
	fresh := captureSessionGoContext(nil)
	if fresh.validation == nil || fresh.namespace != context.namespace || !slices.Equal(fresh.processEnv, context.processEnv) {
		return src, warningData, nil, nil
	}
	verified := &goUsage{}
	for _, input := range usage.names {
		before := len(verified.names)
		names := (goPackages{files: program.files, module: module, context: fresh, usage: verified}).Names(input.paths)
		if !maps.Equal(names, input.names) || len(verified.names) != before+1 || !verified.names[before].standard || verified.names[before].inputs == nil {
			return src, warningData, nil, nil
		}
	}
	if !program.inputs.current() {
		return src, warningData, nil, nil
	}
	return src, warningData, &sessionArtifact{path: path, emit: emit, inputs: program.inputs, module: module, assets: program.assets, context: fresh, names: verified.names, goSrc: slices.Clone(src), warnings: cloneSessionDiagnostics(warningData), sourcePaths: sourcePaths(program.files)}, nil
}

// These unsupported settings are already known from the ordinary go env result.
// Decline before attempting to capture a cache configuration inventory.
func supportedCacheMissSettings(ctx *goContext) bool {
	if runtime.GOOS != "linux" || !supportedGoVersion(ctx.values["GOVERSION"]) || ctx.values["GOFLAGS"] != "" || (ctx.values["GOFIPS140"] != "" && ctx.values["GOFIPS140"] != "off") || (ctx.values["GOWORK"] != "" && ctx.values["GOWORK"] != "off") {
		return false
	}
	switch ctx.values["GOTOOLCHAIN"] {
	case "", "auto", "local":
		return true
	}
	return false
}
