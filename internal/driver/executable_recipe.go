package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/toolenv"
)

// BuildOptions controls validation of an executable, independently of compiler
// result reuse. Rebuild always wins over Fast.
type BuildOptions struct{ Fast, Rebuild bool }

func resolveBuildOptions(options []BuildOptions) (BuildOptions, error) {
	var result BuildOptions
	for _, option := range options {
		result.Fast = result.Fast || option.Fast
		result.Rebuild = result.Rebuild || option.Rebuild
	}
	for _, option := range []struct {
		name   string
		target *bool
	}{{"BORKFAST", &result.Fast}, {"BORKREBUILD", &result.Rebuild}} {
		value, err := toolenv.Value(option.name)
		if err != nil {
			return result, err
		}
		*option.target = *option.target || value == "1"
	}
	return result, nil
}

// The receipt is a bounded set of paths observed during one complete build.
// Hits never enumerate a directory, run the compiler, or invoke Go.
type executableRecipe struct {
	Schema              int
	Namespace           [sha256.Size]byte
	Request, Executable string
	Output              buildExecutableIdentity
	Environment         []string
	SDK                 *installedSDKIdentity
	Launcher            string
	Inputs              *buildInventory
	Missing             []string
	Compilers           map[string]string
	Untracked           []string
	Fast                bool
}

func recipeEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GO") || strings.HasPrefix(name, "CGO_") || strings.HasPrefix(name, "BORK") && !strings.HasPrefix(name, "BORK_TEST_") && name != "BORKFAST" && name != "BORKREBUILD" || name == "CC" || name == "CXX" || name == "FC" || strings.HasPrefix(name, "PKG_CONFIG") || name == "PATH" || name == "HOME" || name == "XDG_CONFIG_HOME" || recipeCompilerEnvironment(name) {
			result = append(result, entry)
		}
	}
	sort.Strings(result)
	return result
}

func recipeRequest(path, out string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	data, _ := json.Marshal([]string{cwd, path, out})
	return string(data)
}
func recipePath(request string) string {
	digest := sha256.Sum256([]byte(request))
	return filepath.Join(cacheCLIState.root, "executables", "v1", fmt.Sprintf("%02x", digest[0]), fmt.Sprintf("%x.json", digest))
}
func lookupExecutableRecipe(request string, options BuildOptions) *executableRecipe {
	if request == "" || options.Rebuild || cacheCLIState == nil || cacheDisabled() {
		return nil
	}
	file, err := os.Open(recipePath(request))
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, buildReceiptLimit+1))
	if err != nil || len(data) > buildReceiptLimit || validateCacheJSON(data) != nil {
		return nil
	}
	var envelope cacheArtifactEnvelope
	var result executableRecipe
	if decodeStrictCacheJSON(data, &envelope) != nil || envelope.Schema != 1 {
		return nil
	}
	digest := sha256.Sum256(envelope.Body)
	if envelope.Checksum != fmt.Sprintf("%x", digest) || decodeStrictCacheJSON(envelope.Body, &result) != nil || result.Schema != 1 || result.Request != request {
		return nil
	}
	canonical, err := json.Marshal(&result)
	if err != nil || !bytes.Equal(canonical, envelope.Body) || !result.current() {
		return nil
	}
	if len(result.Untracked) > 0 && !options.Fast && !result.Fast {
		fmt.Fprintf(os.Stderr, "bork: fast reuse does not track %s; use --rebuild (BORKREBUILD=1) to recheck, or --fast (BORKFAST=1, or fast in bork.mod) to accept.\n", strings.Join(result.Untracked, "; "))
	}
	if root, err := os.OpenRoot(cacheCLIState.root); err == nil {
		relative, err := filepath.Rel(cacheCLIState.root, filepath.Join(filepath.Dir(result.Inputs.Stage), "used"))
		if err == nil && filepath.IsLocal(relative) {
			_ = markCacheUse(root, relative, time.Now(), false)
		}
		relative, err = filepath.Rel(cacheCLIState.root, recipePath(request))
		if err == nil && filepath.IsLocal(relative) {
			_ = markCacheUse(root, relative, time.Now(), false)
		}
		_ = root.Close()
	}
	_ = queueCacheTrim(cacheCLIState.root)
	testCacheProbe("executable-hit")
	testCacheProbeAt("BORK_TEST_EXECUTABLE_RECIPE_PROBE", "hit")
	return &result
}
func (recipe *executableRecipe) current() bool {
	if recipe.Inputs == nil || !recipe.Inputs.Broad || recipe.SDK == nil || !bytes.Equal(mustRecipeJSON(recipe.Environment), mustRecipeJSON(recipeEnvironment())) || !recipe.Inputs.current() {
		return false
	}
	for _, name := range recipe.Missing {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			return false
		}
	}
	for command, path := range recipe.Compilers {
		actual, err := exec.LookPath(command)
		if err != nil || actual != path {
			return false
		}
	}
	resolved := resolveGoContext()
	if resolved.err != nil || resolved.tool != recipe.Launcher || !recipe.SDK.current(recipe.SDK.Launcher, recipe.SDK.Root, recipe.SDK.GoVersion) {
		return false
	}
	cacheCLIState.startIdentity()
	<-cacheCLIState.done
	if cacheCLIState.err != nil || cacheCLIState.namespace != recipe.Namespace {
		return false
	}
	actual, err := readBuildExecutable(recipe.Executable)
	return err == nil && actual == recipe.Output && actual.Mode.Perm()&0111 != 0
}
func mustRecipeJSON(value any) []byte { data, _ := json.Marshal(value); return data }

func captureExecutableRecipe(request, executable, dir string, program *compiledProgram, certified buildExecutableIdentity, before *buildInventory) *executableRecipe {
	if cacheCLIState == nil || cacheDisabled() || program.inputs == nil || !program.inputs.current() {
		return nil
	}
	ctx := program.context
	inputs := captureBuildInventoryMode(dir, ctx, true)
	if inputs == nil || before == nil || !bytes.Equal(mustRecipeJSON(before), mustRecipeJSON(inputs)) || !before.current() {
		return nil
	}
	// Stage files are derived artifacts; record original bork sources/assets below.
	inputs.Stage, _ = filepath.EvalSymlinks(dir)
	recipe := &executableRecipe{Schema: 1, Request: request, Executable: executable, Environment: recipeEnvironment(), SDK: captureInstalledSDK(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"]), Inputs: inputs, Compilers: map[string]string{}}
	resolved := resolveGoContext()
	if resolved.err != nil {
		return nil
	}
	recipe.Launcher = resolved.tool
	if recipe.SDK == nil {
		return nil
	}
	cacheCLIState.startIdentity()
	<-cacheCLIState.done
	if cacheCLIState.err != nil {
		return nil
	}
	recipe.Namespace = cacheCLIState.namespace
	capture := func(path string, expected []byte) bool {
		input, ok := captureBuildFile(path)
		if !ok || expected != nil && input.Digest != sha256.Sum256(expected) {
			return false
		}
		inputs.Files[path] = input
		return true
	}
	directory := func(path string) bool {
		identity, ok := buildDirectoryStat(path)
		if !ok {
			return false
		}
		inputs.Directories[path] = identity
		return true
	}
	for key, value := range program.inputs.reads {
		if value.err != nil {
			if !os.IsNotExist(value.err) {
				return nil
			}
			recipe.Missing = append(recipe.Missing, key.path)
			continue
		}
		switch key.kind {
		case "file":
			if !capture(key.path, value.data) {
				return nil
			}
			if filepath.Base(key.path) == ModFile {
				mod, err := parseModFile(string(value.data))
				if err == nil && mod.fast {
					recipe.Fast = true
				}
			}
		case "directory":
			if !directory(key.path) {
				return nil
			}
		case "stat":
			if value.directory && !directory(key.path) {
				return nil
			}
		}
	}
	captureAssets := func(base string, value embedRead) bool {
		if value.err != nil {
			return false
		}
		for _, entry := range value.entries {
			if !filepath.IsAbs(entry.path) && entry.mode.IsDir() && !directory(filepath.Join(base, entry.path)) {
				return false
			}
		}
		for _, content := range value.contents {
			if !capture(filepath.Join(base, content.path), content.data) {
				return false
			}
		}
		return true
	}
	if program.assets != nil {
		for key, value := range program.assets.reads {
			if !captureAssets(key.base, value) {
				return nil
			}
		}
	}
	if program.inputs.rooted != nil {
		for key, value := range program.inputs.rooted.reads {
			if !captureAssets(key.root, value.value) {
				return nil
			}
		}
	}
	if path := ctx.values["GOENV"]; path != "" && path != "off" {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			recipe.Missing = append(recipe.Missing, path)
		} else if !capture(path, nil) {
			return nil
		}
	}
	hasCgo := false
	var cgoPackages []string
	hasFortran := false
	hasCXX := false
	for _, pkg := range inputs.Packages {
		hasCgo = hasCgo || len(pkg.CgoFiles) > 0
		if !pkg.Standard && len(pkg.CgoFiles) > 0 {
			cgoPackages = append(cgoPackages, pkg.ImportPath)
		}
		hasFortran = hasFortran || len(pkg.FFiles) > 0
		hasCXX = hasCXX || len(pkg.CXXFiles) > 0
	}
	if len(cgoPackages) > 0 {
		sort.Strings(cgoPackages)
		recipe.Untracked = append(recipe.Untracked, "system C headers and libraries ("+strings.Join(cgoPackages, ", ")+")")
	}
	if hasCgo {
		for _, key := range []string{"CC", "CXX", "FC", "PKG_CONFIG"} {
			command := ctx.values[key]
			if command == "" {
				command = ctx.processValue(key)
			}
			if key == "FC" && command == "" && hasFortran {
				command = "gfortran"
			}
			if command == "" {
				continue
			}
			fields, ok := splitBuildWords(command)
			if !ok || len(fields) == 0 {
				continue
			}
			resolved, err := exec.LookPath(fields[0])
			if err != nil {
				if key == "CC" || key == "CXX" && hasCXX || key == "FC" && hasFortran {
					return nil
				}
				continue
			}
			recipe.Compilers[fields[0]] = resolved
			absolute, err := filepath.Abs(resolved)
			if err != nil || !capture(absolute, nil) {
				return nil
			}
		}
	}
	if program.evaluator {
		recipe.Untracked = append(recipe.Untracked, "external state read by compile-time code (environment and files outside the project)")
	}
	actual, err := readBuildExecutable(executable)
	if err != nil || actual != certified {
		return nil
	}
	recipe.Output = certified
	sort.Strings(recipe.Missing)
	if !program.inputs.current() || !inputs.current() || !recipe.current() {
		return nil
	}
	return recipe
}
func writeExecutableRecipe(recipe *executableRecipe) {
	if recipe == nil {
		return
	}
	body, err := json.Marshal(recipe)
	if err != nil || len(body) > buildReceiptLimit {
		return
	}
	digest := sha256.Sum256(body)
	data, err := json.Marshal(cacheArtifactEnvelope{Schema: 1, Body: body, Checksum: fmt.Sprintf("%x", digest)})
	if err != nil || len(data) > buildReceiptLimit {
		return
	}
	path := recipePath(recipe.Request)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".recipe-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(file.Name(), path)
	}
}

// Only foreign helpers can escape the checked build-input contract. Compiler
// intrinsics and audited standard helpers retain their closed-input behavior.
func recipeHasForeignHelpers(node *check.Comptime) bool {
	for _, fn := range check.ComptimeHelpers(node) {
		if recipeHasForeignFunction(fn, map[*check.Func]bool{}) {
			return true
		}
	}
	return false
}
func recipeHasForeignFunction(fn *check.Func, seen map[*check.Func]bool) bool {
	if fn == nil || seen[fn] {
		return false
	}
	seen[fn] = true
	if fn.Decl != nil && fn.Decl.IsGo() && !fn.Prelude && fn.Pkg != nil && fn.Pkg.Path != "bork" && !strings.HasPrefix(fn.Pkg.Path, "bork/") {
		return true
	}
	for _, callee := range fn.Calls {
		if recipeHasForeignFunction(callee, seen) {
			return true
		}
	}
	if fn.Body != nil {
		for _, callee := range check.ComptimeHelpers(&check.Comptime{Body: fn.Body}) {
			if recipeHasForeignFunction(callee, seen) {
				return true
			}
		}
	}
	return false
}
func recipeHasForeignQuery(query check.Query) bool {
	if recipeHasForeignFunction(query.Pred, map[*check.Func]bool{}) {
		return true
	}
	for _, queries := range [][]check.Query{query.Or, query.And} {
		for _, child := range queries {
			if recipeHasForeignQuery(child) {
				return true
			}
		}
	}
	for _, value := range append(append([]check.Expr(nil), query.Values...), query.Subject) {
		if value != nil && recipeHasForeignHelpers(&check.Comptime{Body: &check.Block{Tail: value}}) {
			return true
		}
	}
	for _, fn := range check.ComptimeHelpers(&check.Comptime{Body: &check.Block{}}, query.Dicts...) {
		if recipeHasForeignFunction(fn, map[*check.Func]bool{}) {
			return true
		}
	}
	return false
}

func recipeCompilerEnvironment(name string) bool {
	switch name {
	case "CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH", "OBJC_INCLUDE_PATH", "LIBRARY_PATH", "COMPILER_PATH", "GCC_EXEC_PREFIX", "SDKROOT", "DEVELOPER_DIR", "MACOSX_DEPLOYMENT_TARGET", "LANG", "LC_ALL", "SOURCE_DATE_EPOCH":
		return true
	}
	return false
}
