package driver

import (
	"maps"
	"slices"
	"sync"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

type goNameInput struct {
	paths    []string
	names    map[string]string
	standard bool
	inputs   *goNameValidation
}
type goUsage struct {
	proofs           *sessionProofCache
	deferInputs      bool
	names            []goNameInput
	types, evaluator bool
	// Request-local observations never qualify an execution receipt or escape
	// into the retained Session artifact.
	executions executionObservations
	execution  *executionTracker
}

// Session reuses the last successful complete-program result. Check and Emit
// retain no compiler graph and return independent data. Analyze separately
// retains an editor query snapshot, used serially by the language server.
// Go type/export metadata, custom drivers and compile-time evaluation bypass
// complete-program reuse. Audited pure predicate batches have a separate bounded
// Session cache.
type Session struct {
	editor        *EditorAnalysis
	editorPath    string
	editorContext *goContext
	mu            sync.Mutex
	last          *sessionArtifact
	proofs        *sessionProofCache
	stats         SessionStats
	watch         bool
	attempt       *watchAttempt
	observe       func(string)
	// Private fixture settings are assigned before the Session's first request.
	// Production sessions use the current process environment on every request.
	goSettings []string
}

// SessionStats counts requests and explains the most recent hit or miss.
// Proof counters count native predicate batches, separately from program hits.
type SessionStats struct {
	Hits, Misses, Bypasses                uint64
	ProofHits, ProofMisses, ProofDeclines uint64
	Reason                                string
}

type sessionArtifact struct {
	rootSource  string
	sourcePaths []string
	path        string
	emit        bool
	inputs      *sourceSnapshot
	module      *goModuleInputs
	assets      *embedSnapshot
	context     *goContext
	names       []goNameInput
	goSrc       []byte
	warnings    []diag.Diagnostic
}

func NewSession() *Session { return &Session{} }

// Check returns owned warning diagnostics. Errors retain the one-shot compiler's
// diagnostic behavior; unsuccessful results are compiled again on each request.
func (s *Session) Check(path string) ([]diag.Diagnostic, error) {
	_, warnings, err := s.compile(path, false)
	return warnings, err
}

// Emit returns an owned copy of generated Go source. Predicate checking may
// reuse Session-owned audited boolean batches.
func (s *Session) Emit(path string) ([]byte, error) {
	src, _, err := s.compile(path, true)
	return src, err
}

func (s *Session) Stats() SessionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	if s.proofs != nil {
		stats.ProofHits, stats.ProofMisses, stats.ProofDeclines = s.proofs.hits, s.proofs.misses, s.proofs.declines
	}
	return stats
}

func (s *Session) compile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { phase(s.observe, "") }()
	phase(s.observe, "configuration")
	var previous *goContext
	if s.last != nil {
		previous = s.last.context
	} else if s.watch && s.attempt != nil {
		previous = s.attempt.context
	}
	context := captureSessionGoContextWithSettings(previous, s.goSettings)
	phase(s.observe, "validate")
	if reason := s.hitMissReason(path, emit, context); reason == "" {
		s.stats.Hits++
		s.stats.Reason = "unchanged validated inputs"
		return slices.Clone(s.last.goSrc), cloneSessionDiagnostics(s.last.warnings), nil
	} else {
		s.stats.Misses++
		s.stats.Reason = reason
		s.last = nil
	}
	var loaded *loadedSources
	var module *goModuleInputs
	var err error
	if s.watch {
		s.attempt = newWatchAttempt(context)
		loaded, module, err = loadCompilationInputsFrom(path, s.observe, func() *sourceSnapshot {
			inputs := newSourceSnapshot()
			s.attempt.inputs = inputs
			return inputs
		})
	} else {
		loaded, module, err = loadCompilationInputs(path, s.observe)
	}
	if err != nil {
		return nil, nil, err
	}
	usage := &goUsage{proofs: s.proofCache()}
	captureAssets := captureEmbedsSnapshot
	if s.watch {
		defer func() { s.attempt.names = slices.Clone(usage.names) }()
		captureAssets = func(info *check.Info, diags *diag.List, sources *sourceSnapshot) *embedSnapshot {
			assets := captureEmbedsFrom(info, diags, sources, func(sources *sourceSnapshot) *embedSnapshot {
				assets := newEmbedSnapshot(sources)
				s.attempt.assets = assets
				return assets
			})
			return assets
		}
	}
	program, err := checkLoadedProgramTracked(loaded, module, context, captureAssets, usage, s.observe)
	if err != nil {
		return nil, nil, err
	}
	phase(s.observe, "warnings")
	warnings := check.DebugWarnings(program.info)
	warnings.Append(check.LazyWarnings(program.info))
	warnings.Append(check.MigrationWarnings(program.info))
	warningData := warnings.Sorted()
	var src []byte
	if emit {
		if err := program.requireMain(); err != nil {
			return nil, nil, err
		}
		phase(s.observe, "generate")
		src, err = gen.Package(program.files, program.info)
		if err != nil {
			return nil, nil, err
		}
	}
	phase(s.observe, "retain")
	if reason := sessionBypassReason(context, usage); reason != "" {
		s.stats.Bypasses++
		s.stats.Reason = reason
	} else if program.inputs.usesDriveContext() {
		s.stats.Bypasses++
		s.stats.Reason = "uncaptured Windows drive context"
	} else if !program.inputs.current() || !program.assets.current() {
		s.stats.Bypasses++
		s.stats.Reason = "inputs changed during compilation"
	} else {
		s.last = &sessionArtifact{rootSource: goStageRootSource(program.files), sourcePaths: sourcePaths(program.files), path: path, emit: emit, inputs: program.inputs, module: program.module, assets: program.assets, context: context, names: usage.names, goSrc: slices.Clone(src), warnings: cloneSessionDiagnostics(warningData)}
	}
	return src, warningData, nil
}

func sessionBypassReason(context *goContext, usage *goUsage) string {
	switch {
	case context.err != nil || context.driverErr != nil:
		return "Go configuration unavailable"
	case context.driver != "off":
		return "custom Go package driver"
	case usage.types:
		return "Go type metadata"
	case usage.evaluator:
		return "compile-time evaluator"
	}
	for _, input := range usage.names {
		if !input.standard {
			return "external or unavailable Go package names"
		}
	}
	return ""
}

func (s *Session) hitMissReason(path string, emit bool, context *goContext) string {
	artifact := s.last
	switch {
	case artifact == nil:
		return "no previous result"
	case artifact.path != path || artifact.emit != emit:
		return "request path or mode changed"
	case context.err != nil || context.driverErr != nil || context.namespace != artifact.context.namespace:
		return "Go configuration changed"
	case !artifact.inputs.current():
		return "source or manifest inputs changed"
	case !artifact.assets.current():
		return "asset inputs changed"
	}
	// Validate proven builtin name-query inputs by content. Unknown query
	// configurations keep the complete metadata reload with name reuse disabled.
	for _, input := range artifact.names {
		if input.inputs != nil {
			if !input.inputs.current() {
				return "Go package names changed"
			}
			continue
		}
		usage := &goUsage{}
		names := (goPackages{module: artifact.module, context: context, usage: usage}).Names(input.paths)
		if !maps.Equal(names, input.names) || len(usage.names) != 1 || !usage.names[0].standard {
			return "Go package names changed"
		}
	}
	if !artifact.inputs.current() || !artifact.assets.current() {
		return "inputs changed during validation"
	}
	return ""
}

func cloneSessionDiagnostics(items []diag.Diagnostic) []diag.Diagnostic {
	out := slices.Clone(items)
	for i := range out {
		out[i].Fixes = slices.Clone(out[i].Fixes)
		for j := range out[i].Fixes {
			out[i].Fixes[j].Edits = slices.Clone(out[i].Fixes[j].Edits)
		}
	}
	return out
}

// Called only while the Session mutex is held, including editor requests.
func (s *Session) proofCache() *sessionProofCache {
	if s.proofs == nil {
		s.proofs = &sessionProofCache{memo: newPredicateMemo()}
	}
	return s.proofs
}
