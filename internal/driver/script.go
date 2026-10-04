package driver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
	"golang.org/x/mod/modfile"
	gomodule "golang.org/x/mod/module"
)

// A private request tag partitions normal/script cache and Session keys. The
// loader strips it before observing files, so diagnostics retain source paths.
const scriptRequestPrefix = "bork-script:"

// RunScript executes a single file with an implicit main, even without a shebang.
func RunScript(path string, args []string) (int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 1, err
	}
	return Run(scriptRequestPrefix+absolute, args)
}

type scriptHeader struct {
	requirements []gomodule.Version
	unsafe       bool
}

func readScriptHeader(file *syntax.File, project bool, diags *diag.List) scriptHeader {
	var header scriptHeader
	found := false
	for _, comment := range file.Comments {
		found = found || strings.HasPrefix(comment.Text, "// bork:")
	}
	if !found {
		return header
	}
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	firstLine := tokens[0].Pos.Line
	if tokens[0].Kind == syntax.EOF {
		firstLine++
	}
	seen := map[string]string{}
	for _, comment := range file.Comments {
		text, ok := strings.CutPrefix(comment.Text, "// bork:")
		if !ok {
			continue
		}
		if project {
			diags.AddCode(comment.Pos, "script.directive-in-project", "inline script directives are not allowed in bork.mod projects; declare Go dependencies in go-deps.mod and unsafe opt-ins in bork.mod")
			continue
		}
		if !file.Script || comment.Pos.Line >= firstLine {
			diags.AddCode(comment.Pos, "script.directive-position", "script directives must appear in the header of a script, before imports and declarations")
			continue
		}
		fields := strings.Fields(text)
		switch {
		case len(fields) == 1 && fields[0] == "unsafe":
			header.unsafe = true
		case len(fields) == 3 && fields[0] == "require":
			path, version := fields[1], fields[2]
			if err := gomodule.Check(path, version); err != nil || gomodule.CanonicalVersion(version) != version {
				diags.AddCode(comment.Pos, "script.dependency", "script dependencies need a valid module path and canonical pinned version, found %q", text)
				continue
			}
			if old := seen[path]; old != "" {
				diags.AddCode(comment.Pos, "script.dependency", "script dependency %s is declared more than once", path)
				continue
			}
			seen[path] = version
			header.requirements = append(header.requirements, gomodule.Version{Path: path, Version: version})
		default:
			diags.AddCode(comment.Pos, "script.directive", "expected // bork:require <module> <version> or // bork:unsafe")
		}
	}
	return header
}

// Pin a script's Go graph once, storing its resolved manifest and checksums in
// the compiler cache. Captured reads make these files ordinary validated inputs
// of subsequent result-cache requests; no files are written beside the script.
func scriptGoDependencies(file *syntax.File, requirements []gomodule.Version, reader sourceReader) ([]std.GoDependencyManifest, error) {
	root, err := cacheRootDir()
	if err != nil {
		return nil, err
	}
	var identity strings.Builder
	identity.WriteString("script-deps-v1\n")
	for _, dep := range requirements {
		fmt.Fprintf(&identity, "%s@%s\n", dep.Path, dep.Version)
	}
	for _, name := range []string{"GOPROXY", "GOSUMDB", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOENV", "GOTOOLCHAIN"} {
		fmt.Fprintf(&identity, "%s=%s\n", name, os.Getenv(name))
	}
	key := sha256.Sum256([]byte(identity.String()))
	dir := filepath.Join(root, "scripts", "deps", fmt.Sprintf("%x", key))
	read := func() ([]std.GoDependencyManifest, error) {
		mod, err := reader.readFile(filepath.Join(dir, "go-deps.mod"))
		if err != nil {
			return nil, err
		}
		sum, err := reader.readFile(filepath.Join(dir, "go-deps.sum"))
		if err != nil {
			return nil, err
		}
		return []std.GoDependencyManifest{{Name: file.Path, Mod: mod, Sum: sum}}, nil
	}
	if manifests, err := read(); err == nil {
		return manifests, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	temp, err := os.MkdirTemp(filepath.Dir(dir), "resolve-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), []byte("module borkscript\ngo 1.26\n"), 0o600); err != nil {
		return nil, err
	}
	run := func(args ...string) error {
		cmd := exec.Command("go", args...)
		cmd.Dir = temp
		cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOFLAGS=")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("resolve script dependencies: go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	args := []string{"get"}
	for _, dep := range requirements {
		args = append(args, dep.Path+"@"+dep.Version)
	}
	if err := run(args...); err != nil {
		return nil, err
	}
	if err := run("mod", "download", "all"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(temp, "go.mod"))
	if err != nil {
		return nil, err
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	parsed.DropToolchainStmt()
	data, err = parsed.Format()
	if err != nil {
		return nil, err
	}
	sum, err := os.ReadFile(filepath.Join(temp, "go.sum"))
	if err != nil {
		return nil, err
	}
	// Validate before publishing; malformed/unpinned results never enter the cache.
	if _, _, err := std.GoModuleFiles(nil, std.GoDependencyManifest{Name: file.Path, Mod: data, Sum: sum}); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(temp, "go-deps.mod"), data, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(temp, "go-deps.sum"), sum, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(temp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return nil, err
		}
	}
	return read()
}
