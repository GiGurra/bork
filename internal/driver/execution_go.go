package driver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const goExecutionDiscoveryLimit = 16 << 20
const goExecutionPackageLimit = 4096

// goExecutionInvocation owns the exact supported build envelope. A later caller
// must use it for execution; a discovery made with ctx defaults alone cannot
// certify a differently configured build. Output paths are bound by integration.
type goExecutionInvocation struct {
	Tool, Root, Mode string
	Env, BuildArgs   []string
}
type goExecutionStage struct {
	Root, Mode string
	Program    []byte
	Module     *goModuleInputs
}
type goExecutionPackage struct {
	ImportPath, Name, Dir, Root                                                          string
	Standard, Goroot                                                                     bool
	GoFiles, CgoFiles, IgnoredGoFiles, IgnoredOtherFiles                                 []string
	CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles []string
	EmbedPatterns, EmbedFiles, Imports                                                   []string
	Error                                                                                *struct{ Err string }
	DepsErrors                                                                           []struct{ Err string }
	Module                                                                               *struct {
		Path, Dir, GoMod, GoVersion string
		Main                        bool
		Replace                     *json.RawMessage
	}
}
type goExecutionInventory struct {
	seal                     [sha256.Size]byte
	Version                  int
	Invocation               goExecutionInvocation
	Program, Module, Sum     [sha256.Size]byte
	Root, ToolDir, GoVersion string
	Packages                 []goExecutionPackage
	Inputs                   goExecutionInputs
}

func goExecutionEnvelope(ctx *goContext, stage goExecutionStage) (goExecutionInvocation, error) {
	var out goExecutionInvocation
	if ctx == nil || ctx.err != nil || ctx.driverErr != nil || ctx.driver != "off" || goModuleHook != nil {
		return out, errors.New("unsupported Go execution context or module hook")
	}
	if stage.Mode != "predicate" && stage.Mode != "comptime" {
		return out, errors.New("unsupported Go execution mode")
	}
	if !filepath.IsAbs(stage.Root) || len(stage.Program) == 0 || len(stage.Program) > executionIdentityMaxBytes || stage.Module == nil || len(stage.Module.mod)+len(stage.Module.sum) > executionIdentityMaxBytes {
		return out, errors.New("invalid or over-budget frozen Go stage")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || ctx.values["GOAMD64"] != "v1" {
		return out, errors.New("unsupported native Go execution platform or CPU level")
	}
	if ctx.values["GOOS"] != runtime.GOOS || ctx.values["GOARCH"] != runtime.GOARCH || ctx.values["GOHOSTOS"] != runtime.GOOS || ctx.values["GOHOSTARCH"] != runtime.GOARCH {
		return out, errors.New("non-native Go execution target")
	}
	if ctx.processValue("GOCACHEPROG") != "" || ctx.values["GOCACHEPROG"] != "" || (ctx.processValue("GO_EXTLINK_ENABLED") != "" && ctx.processValue("GO_EXTLINK_ENABLED") != "0") {
		return out, errors.New("unsupported external Go execution helper")
	}
	if ctx.values["GOFLAGS"] != "" || ctx.values["GOEXPERIMENT"] != "" || (ctx.values["GOFIPS140"] != "" && ctx.values["GOFIPS140"] != "off") || (ctx.values["GOWORK"] != "" && ctx.values["GOWORK"] != "off") || ctx.values["GO111MODULE"] == "off" {
		return out, errors.New("unsupported Go execution flags or selection mode")
	}
	info, err := buildinfo.ReadFile(ctx.tool)
	if err != nil || info.Path != "cmd/go" || !supportedGoVersion(info.GoVersion) || info.GoVersion != ctx.values["GOVERSION"] {
		return out, errors.New("unsupported or switched Go execution launcher")
	}
	if !utf8.ValidString(stage.Root) || !utf8.ValidString(stage.Mode) || !utf8.ValidString(ctx.tool) {
		return out, errors.New("unsupported non-UTF-8 Go execution path")
	}
	metadata := len(stage.Root) + len(stage.Mode) + len(ctx.tool)
	for _, item := range ctx.env {
		if !utf8.ValidString(item) {
			return out, errors.New("unsupported non-UTF-8 Go execution environment")
		}
		if len(item) > goExecutionMetadataLimit-metadata {
			return out, errors.New("go execution environment budget exceeded")
		}
		metadata += len(item)
	}
	stageRoot, err := filepath.EvalSymlinks(stage.Root)
	if err != nil {
		return out, err
	}
	env := append(slices.Clone(ctx.env), "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOPROXY=off", "GOSUMDB=off", "GOCACHEPROG=", "GO_EXTLINK_ENABLED=0")
	out = goExecutionInvocation{Tool: ctx.tool, Root: stageRoot, Mode: stage.Mode, Env: env, BuildArgs: []string{"build", "-mod=readonly", "-buildvcs=false", "-ldflags=-linkmode=internal", "."}}
	return out, nil
}
func goExecutionOutput(inv goExecutionInvocation, args ...string) ([]byte, error) {
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, inv.Tool, args...)
	cmd.Dir = inv.Root
	cmd.Env = slices.Clone(inv.Env)
	cmd.WaitDelay = time.Second
	configureEvaluationProcess(cmd)
	output := &boundedOutput{limit: goExecutionDiscoveryLimit + 1}
	stderr := &boundedOutput{limit: 64 << 10}
	cmd.Stdout = output
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("go execution discovery failed")
	}
	if len(output.data) > goExecutionDiscoveryLimit {
		return nil, errors.New("go execution discovery output budget exceeded")
	}
	return output.data, nil
}
func discoverGoExecution(inv goExecutionInvocation, root string) ([]goExecutionPackage, error) {
	data, err := goExecutionOutput(inv, "list", "-deps", "-mod=readonly", "-buildvcs=false", "-ldflags=-linkmode=internal", "-json", ".")
	if err != nil {
		return nil, err
	}
	return decodeGoExecutionPackages(data, inv.Root, root)
}
func decodeGoExecutionPackages(data []byte, stage, root string) ([]goExecutionPackage, error) {
	if len(data) > goExecutionDiscoveryLimit {
		return nil, errors.New("go execution discovery budget exceeded")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var packages []goExecutionPackage
	selected := map[string]bool{}
	mainCount := 0
	for {
		var pkg goExecutionPackage
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(packages) >= goExecutionPackageLimit || pkg.ImportPath == "" || selected[pkg.ImportPath] || pkg.Error != nil || len(pkg.DepsErrors) > 0 {
			return nil, errors.New("invalid Go execution dependency discovery")
		}
		if pkg.ImportPath == "unsafe" && pkg.Dir == "" {
			if pkg.Dir != "" || !pkg.Standard || !pkg.Goroot {
				return nil, errors.New("unsupported unsafe package selection")
			}
		} else if filepath.Clean(pkg.Dir) == stage && pkg.Name == "main" {
			mainCount++
			if len(pkg.GoFiles) != 1 || pkg.GoFiles[0] != "main.go" || len(pkg.SFiles)+len(pkg.HFiles)+len(pkg.SysoFiles)+len(pkg.EmbedFiles) > 0 {
				return nil, errors.New("additional generated Go execution inputs")
			}

			if pkg.Module != nil && (!pkg.Module.Main || pkg.Module.Replace != nil || filepath.Clean(pkg.Module.Dir) != stage) {
				return nil, errors.New("unsupported generated module selection")
			}
		} else if !pkg.Standard || !pkg.Goroot || pkg.Module != nil || !filepath.IsAbs(pkg.Dir) || !withinGoExecutionRoot(pkg.Dir, filepath.Join(root, "src")) {
			return nil, errors.New("foreign or unsupported Go execution dependency")
		}
		if len(pkg.CgoFiles)+len(pkg.CFiles)+len(pkg.CXXFiles)+len(pkg.MFiles)+len(pkg.FFiles)+len(pkg.SwigFiles)+len(pkg.SwigCXXFiles)+len(pkg.SysoFiles) > 0 {
			return nil, errors.New("cgo or foreign Go build inputs")
		}
		for _, paths := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.HFiles, pkg.SFiles, pkg.SysoFiles, pkg.EmbedFiles} {
			for _, path := range paths {
				if !validGoExecutionRelative(path) {
					return nil, errors.New("unsupported Go execution selected path")
				}
			}
		}
		selected[pkg.ImportPath] = true
		packages = append(packages, pkg)
	}
	if mainCount != 1 {
		return nil, errors.New("missing or ambiguous generated Go package")
	}
	for _, pkg := range packages {
		for _, dependency := range pkg.Imports {
			if !selected[dependency] {
				return nil, errors.New("incomplete Go execution dependency closure")
			}
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ImportPath < packages[j].ImportPath })
	return packages, nil
}
func validGoExecutionRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.VolumeName(path) == "" && !strings.Contains(path, "\\") && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../")
}
func captureGoExecution(ctx *goContext, stage goExecutionStage) (*goExecutionInventory, executionDecline) {
	inv, err := goExecutionEnvelope(ctx, stage)
	if err != nil {
		return nil, executionDecline(err.Error())
	}
	// Freeze supplied generated/module bytes before any discovery subprocess.
	stage.Program = slices.Clone(stage.Program)
	stage.Module = &goModuleInputs{mod: slices.Clone(stage.Module.mod), sum: slices.Clone(stage.Module.sum)}
	envBytes, err := goExecutionOutput(inv, "env", "-json")
	if err != nil {
		return nil, executionDecline(err.Error())
	}
	var settings map[string]string
	if err := json.Unmarshal(envBytes, &settings); err != nil {
		return nil, "invalid pinned Go execution settings"
	}
	root, toolDir := filepath.Clean(settings["GOROOT"]), filepath.Clean(settings["GOTOOLDIR"])
	if settings["GOROOT"] != ctx.values["GOROOT"] || settings["GOTOOLDIR"] != ctx.values["GOTOOLDIR"] || settings["GOVERSION"] != ctx.values["GOVERSION"] || settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH || settings["CGO_ENABLED"] != "0" || settings["GOCACHEPROG"] != "" || settings["GOFLAGS"] != "" || settings["GOEXPERIMENT"] != "" || settings["GOAMD64"] != "v1" || !filepath.IsAbs(root) || !withinGoExecutionRoot(toolDir, root) {
		return nil, "switched or unsupported pinned Go execution settings"
	}
	packages, err := discoverGoExecution(inv, root)
	if err != nil {
		return nil, executionDecline(err.Error())
	}
	capture := newGoExecutionCapture()
	if err := capture.file(inv.Tool); err != nil {
		return nil, executionDecline(err.Error())
	}
	for _, path := range []string{filepath.Join(root, "VERSION"), filepath.Join(root, "go.env")} {
		if err := capture.file(path); err != nil {
			return nil, executionDecline(err.Error())
		}
	}
	if err := capture.packageDirectory(toolDir, root); err != nil {
		return nil, executionDecline(err.Error())
	}
	for _, name := range []string{"compile", "link", "asm"} {
		path := filepath.Join(toolDir, name)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if !capture.files[path] {
			return nil, "missing selected Go execution tool"
		}
	}
	if err := capture.packageDirectory(filepath.Join(root, "pkg", "include"), root); err != nil {
		return nil, executionDecline(err.Error())
	}
	for _, pkg := range packages {
		if pkg.Dir == "" {
			continue
		}
		packageRoot := root
		if filepath.Clean(pkg.Dir) == inv.Root {
			packageRoot = inv.Root
		}
		if err := capture.packageDirectory(pkg.Dir, packageRoot); err != nil {
			return nil, executionDecline(err.Error())
		}
		if err := capture.assemblyIncludes(pkg, root); err != nil {
			return nil, executionDecline(err.Error())
		}
		for _, name := range pkg.EmbedFiles {
			resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(pkg.Dir, name))
			packageRootResolved, rootErr := filepath.EvalSymlinks(packageRoot)
			if resolveErr != nil || rootErr != nil || !withinGoExecutionRoot(resolved, packageRootResolved) {
				return nil, "embedded Go execution input outside selected root"
			}
			path := filepath.Join(pkg.Dir, name)
			if err := capture.file(path); err != nil {
				return nil, executionDecline(err.Error())
			}
			if err := capture.ancestors(filepath.Dir(path), packageRoot); err != nil {
				return nil, executionDecline(err.Error())
			}
		}
	}
	inputs := capture.finish()
	expected := map[string][sha256.Size]byte{"main.go": sha256.Sum256(stage.Program), "go.mod": sha256.Sum256(stage.Module.mod)}
	if len(stage.Module.sum) > 0 {
		expected["go.sum"] = sha256.Sum256(stage.Module.sum)
	}
	for name, digest := range expected {
		found := false
		for _, file := range inputs.Files {
			if file.Path == filepath.Join(inv.Root, name) && file.Digest == digest {
				found = true
				break
			}
		}
		if !found {
			return nil, "generated Go stage does not match frozen inputs"
		}
	}
	if len(stage.Module.sum) == 0 {
		for _, file := range inputs.Files {
			if file.Path == filepath.Join(inv.Root, "go.sum") && file.Size != 0 {
				return nil, "unexpected staged Go sums"
			}
		}
	}
	inventory := &goExecutionInventory{Version: goExecutionInventoryVersion, Invocation: inv, Program: sha256.Sum256(stage.Program), Module: sha256.Sum256(stage.Module.mod), Sum: sha256.Sum256(stage.Module.sum), Root: root, ToolDir: toolDir, GoVersion: settings["GOVERSION"], Packages: packages, Inputs: inputs}
	if !inputs.current() {
		return nil, "Go execution inputs changed during capture"
	}
	rediscovered, err := discoverGoExecution(inv, root)
	if err != nil || !sameGoExecutionPackages(packages, rediscovered) || !inputs.current() {
		return nil, "Go execution dependency closure changed during capture"
	}
	inventory.seal = inventory.identity()
	return inventory, ""
}
func sameGoExecutionPackages(a, b []goExecutionPackage) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

// current performs fresh content, membership and selection validation. Receipt
// current() remains false: this helper alone is not execution certification.
func (inventory *goExecutionInventory) current() bool {
	if inventory == nil || inventory.Version != goExecutionInventoryVersion || inventory.seal != inventory.identity() || !inventory.Inputs.current() {
		return false
	}
	packages, err := discoverGoExecution(inventory.Invocation, inventory.Root)
	return err == nil && sameGoExecutionPackages(inventory.Packages, packages) && inventory.Inputs.current()
}
func (inventory *goExecutionInventory) identity() [sha256.Size]byte {
	encoded, _ := json.Marshal(inventory)
	return sha256.Sum256(encoded)
}
