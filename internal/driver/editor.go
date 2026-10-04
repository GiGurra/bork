package driver

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

// EditorAnalysis is a successful checked snapshot. Its compiler graph remains
// private, separate from Check/Emit artifacts. Use it serially with its Session.
type EditorAnalysis struct {
	program  *compiledProgram
	usage    *goUsage
	overlays map[string]string
	warnings []diag.Diagnostic
}

// Analyze checks a package using absolute-path unsaved source overlays. Returned
// analyses remain snapshots when later requests fail or change their overlays.
func (s *Session) Analyze(path string, overlays map[string]string) (*EditorAnalysis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { phase(s.observe, "") }()
	phase(s.observe, "configuration")
	context := captureSessionGoContextWithSettings(s.editorContext, append(slices.Clone(s.goSettings), "GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"))
	s.editorContext = context
	phase(s.observe, "validate")
	if a := s.editor; a != nil && s.editorPath == path && maps.Equal(a.overlays, overlays) &&
		context.namespace == a.program.context.namespace && sessionBypassReason(context, a.usage) == "" &&
		a.program.inputs.current() && a.program.assets.current() && editorNamesCurrent(a, context) {
		return a, nil
	}
	loaded, module, err := loadCompilationInputsFrom(path, s.observe, func() *sourceSnapshot {
		snapshot := newSourceSnapshot()
		snapshot.localDependencies = true
		snapshot.disk = overlaySources{files: maps.Clone(overlays)}
		return snapshot
	})
	if err != nil {
		return nil, err
	}
	usage := &goUsage{proofs: s.proofCache()}
	program, err := checkLoadedProgramTracked(loaded, module, context, captureEmbedsSnapshot, usage, s.observe)
	if err != nil {
		return nil, err
	}
	warnings := check.DebugWarnings(program.info)
	warnings.Append(check.LazyWarnings(program.info))
	warnings.Append(check.MigrationWarnings(program.info))
	warnings.Append(check.LintWarnings(program.files, program.info))
	a := &EditorAnalysis{program: program, usage: usage, overlays: maps.Clone(overlays), warnings: warnings.Sorted()}
	s.editor, s.editorPath = a, path
	return a, nil
}

func editorNamesCurrent(a *EditorAnalysis, context *goContext) bool {
	for _, input := range a.usage.names {
		if input.inputs != nil {
			if !input.inputs.current() {
				return false
			}
			continue
		}
		usage := &goUsage{}
		names := (goPackages{module: a.program.module, context: context, usage: usage}).Names(input.paths)
		if !maps.Equal(names, input.names) || len(usage.names) != 1 || !usage.names[0].standard {
			return false
		}
	}
	return true
}

func (a *EditorAnalysis) Warnings() []diag.Diagnostic { return cloneSessionDiagnostics(a.warnings) }

// Sources returns owned source text for disk packages in the checked graph.
func (a *EditorAnalysis) Sources() map[string]string {
	out := map[string]string{}
	for _, file := range a.program.files {
		if !file.Prelude && !strings.HasPrefix(file.Package, std.Prefix) {
			path, err := filepath.Abs(file.Path)
			if err == nil {
				out[path] = file.Source
			}
		}
	}
	return out
}

func (a *EditorAnalysis) Describe(pos diag.Pos) (*describe.Result, error) {
	for _, file := range a.program.files {
		path, err := filepath.Abs(file.Path)
		if err == nil && path == pos.File {
			pos.File = file.Path
			return describeProgram(a.program, pos, []byte(file.Source), "")
		}
	}
	return nil, fmt.Errorf("source is not in the checked package")
}

// overlaySources changes only .bork source loading. Manifest and asset reads
// continue through diskSources; overlays also contribute new directory members.
type overlaySources struct {
	diskSources
	files map[string]string
}

func (o overlaySources) readFile(name string) ([]byte, error) {
	path, err := filepath.Abs(name)
	if err == nil {
		if src, ok := o.files[path]; ok {
			return []byte(src), nil
		}
	}
	return o.diskSources.readFile(name)
}
func (o overlaySources) isDirectory(name string) (bool, error) {
	path, err := filepath.Abs(name)
	if err == nil {
		if _, ok := o.files[path]; ok {
			return false, nil
		}
	}
	return o.diskSources.isDirectory(name)
}
func (o overlaySources) directory(name string) ([]sourceEntry, error) {
	entries, err := o.diskSources.directory(name)
	if err != nil {
		return nil, err
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	for file := range o.files {
		if filepath.Dir(file) != path || filepath.Ext(file) != ".bork" {
			continue
		}
		base := filepath.Base(file)
		if !slices.ContainsFunc(entries, func(e sourceEntry) bool { return e.name == base }) {
			entries = append(entries, sourceEntry{name: base})
		}
	}
	slices.SortFunc(entries, func(a, b sourceEntry) int { return strings.Compare(a.name, b.name) })
	return entries, nil
}

// Definition resolves a token identity without evaluating facts.
func (a *EditorAnalysis) Definition(pos diag.Pos) (*diag.Pos, error) {
	for _, file := range a.program.files {
		path, err := filepath.Abs(file.Path)
		if err == nil && path == pos.File && !file.Prelude && !strings.HasPrefix(file.Package, std.Prefix) {
			pos.File = file.Path
			// Declaration positions point at fn/type keywords. Resolve their
			// identifier tokens before using them as editor identities.
			for _, fn := range file.Funcs {
				if fn.ScriptMain {
					continue
				}
				namePos := editorDeclarationName(file, fn.Pos, fn.Name)
				if pos.Line == namePos.Line && pos.Col >= namePos.Col && pos.Col < namePos.Col+len(fn.Name) {
					namePos.File = path
					return &namePos, nil
				}
			}
			selection, err := describe.Lookup(a.program.files, a.program.info, pos, []byte(file.Source))
			if err != nil {
				return nil, err
			}
			if selection.Definition == nil {
				return nil, nil
			}
			result := *selection.Definition
			for _, target := range a.program.files {
				if target.Path == result.File && !target.Prelude && !strings.HasPrefix(target.Package, std.Prefix) {
					for _, fn := range target.Funcs {
						if fn.ScriptMain {
							continue
						}
						if result == fn.Pos {
							result = editorDeclarationName(target, fn.Pos, fn.Name)
							break
						}
					}
					for _, typ := range target.Types {
						if result == typ.Pos {
							result = editorDeclarationName(target, typ.Pos, typ.Name)
							break
						}
					}
					result.File, err = filepath.Abs(result.File)
					return &result, err
				}
			}
			return nil, nil
		}
	}
	return nil, nil
}

func editorDeclarationName(file *syntax.File, start diag.Pos, name string) diag.Pos {
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	depth := 0
	for _, token := range tokens {
		if token.Pos.Line < start.Line || token.Pos.Line == start.Line && token.Pos.Col < start.Col {
			continue
		}
		switch token.Kind {
		case syntax.LParen, syntax.LBrack:
			depth++
		case syntax.RParen, syntax.RBrack:
			depth--
		}
		if token.Kind == syntax.TIdent && token.Text == name && depth == 0 {
			return token.Pos
		}
	}
	return start
}
