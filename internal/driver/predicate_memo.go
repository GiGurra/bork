package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/version"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"golang.org/x/mod/modfile"
)

const predicateMemoEntries = 256
const predicateMemoResultBytes = 64 << 10
const predicateMemoInputBytes = 4 << 20

type sessionProofCache struct {
	memo                   *predicateMemo
	hits, misses, declines uint64
}

// predicateMemo stores owned bounded results. Its owner is either one
// evaluateComptimes call or the separately guarded Session proof cache. It is
// never persisted. All accesses occur serially within the owner.
type predicateMemo struct {
	results map[[sha256.Size]byte][]bool
	bytes   int
}

func newPredicateMemo() *predicateMemo {
	return &predicateMemo{results: make(map[[sha256.Size]byte][]bool)}
}

func (m *predicateMemo) get(key [sha256.Size]byte) ([]bool, bool) {
	result, ok := m.results[key]
	return slices.Clone(result), ok
}

func (m *predicateMemo) put(key [sha256.Size]byte, result []bool) {
	if len(m.results) >= predicateMemoEntries || len(result) > predicateMemoResultBytes-m.bytes {
		return
	}
	if _, exists := m.results[key]; exists {
		return
	}
	m.results[key] = slices.Clone(result)
	m.bytes += len(result)
}

// Bind the bytes actually published by stageGo, including the module hook's
// result, under its slot lock. Calling goStageFiles again would call the hook
// twice. Declines always continue through the ordinary build and executor.
func predicateMemoKey(dir string, ctx *goContext, timeout time.Duration, embeds []*check.Embedded) ([sha256.Size]byte, bool) {
	var zero [sha256.Size]byte
	paths := map[string]bool{"main.go": true, "go.mod": true, "go.sum": true}
	for _, request := range embeds {
		for _, file := range request.Files {
			path := filepath.FromSlash(file.StagePath)
			if !filepath.IsLocal(path) || len(paths) >= goStageInventoryLimit {
				return zero, false
			}
			paths[path] = true
		}
	}
	inputs := make(map[string][]byte, len(paths))
	remaining := predicateMemoInputBytes
	for name := range paths {
		file, err := os.Open(filepath.Join(dir, name))
		if os.IsNotExist(err) && name == "go.sum" {
			continue
		}
		if err != nil {
			return zero, false
		}
		data, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || len(data) > remaining {
			return zero, false
		}
		remaining -= len(data)
		inputs[name] = data
	}
	// The output target is fresh and deliberately excluded: bounded audited
	// proofs cannot observe their executable path. All other build/execution
	// settings and the exact stage path remain part of the identity. In-place
	// toolchain edits during a compiler call are unsupported, as for Go's cache.
	identity := struct {
		Root, Tool, Mode string
		ToolDigest       [sha256.Size]byte
		BuildEnv, Env    []string
		Args             []string
		Timeout          time.Duration
		Inputs           map[string][]byte
	}{dir, ctx.tool, "predicate", ctx.toolDigest,
		append(slices.Clone(ctx.env), "GOWORK=off", "GOFLAGS="), ctx.processEnv,
		[]string{"build", "-mod=readonly", "-buildvcs=false", "."}, timeout, inputs}
	data, err := json.Marshal(identity)
	if err != nil || len(data) > 2*predicateMemoInputBytes {
		return zero, false
	}
	return sha256.Sum256(data), true
}

// The static query audit covers reachable Bork code, not import initialization
// or generated support. Allow only known standard-library support imports and
// the compiler's fixed startup declarations. New support declines until audited.
func predicateMemoSupport(source []byte) bool {
	if len(source) > predicateMemoInputBytes {
		return false
	}
	file, err := parser.ParseFile(token.NewFileSet(), "", source, parser.ParseComments)
	if err != nil {
		return false
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return false
		}
		switch path {
		case "cmp", "context", "encoding/base64", "encoding/json", "errors", "fmt", "hash/maphash", "log/slog", "math", "math/bits", "os", "os/signal", "reflect", "slices", "strconv", "strings", "sync", "sync/atomic", "syscall", "time", "unicode/utf8":
		default:
			return false
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if bytes.HasPrefix([]byte(comment.Text), []byte("//go:")) {
				return false
			}
		}
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok != token.VAR {
				continue
			}
			for _, spec := range decl.Specs {
				spec := spec.(*ast.ValueSpec)
				if len(spec.Values) == 0 {
					continue
				}
				switch predicateSupportText(spec) {
				case `_mainContext = _signalContext()`,
					`_errCancelled = errors.New("cancelled")`, `_errScopeEnded = errors.New("the scope ended")`,
					`_mapHash = _hash`, `_mapSeed = maphash.MakeSeed()`:
				default:
					return false
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil && decl.Name.Name == "init" {
				if len(decl.Body.List) != 1 {
					return false
				}
				switch predicateSupportText(decl.Body.List[0]) {
				case `_equalMapHook = _equalMaps`, `_lazyCompileTime = true`:
				default:
					return false
				}
			}
		}
	}
	return true
}

func predicateSupportText(node ast.Node) string {
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), node); err != nil {
		return ""
	}
	return out.String()
}

// Metadata validation establishes a supported native launcher. Its generated
// module must also stay on that installed toolchain rather than auto-switching.
func sessionProofStage(dir string, ctx *goContext) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || len(data) > predicateMemoInputBytes {
		return false
	}
	module, err := modfile.Parse("go.mod", data, nil)
	return err == nil && module.Toolchain == nil && module.Go != nil && version.IsValid("go"+module.Go.Version) && version.Compare("go"+module.Go.Version, ctx.values["GOVERSION"]) <= 0
}
