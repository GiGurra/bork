package driver

import (
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// These inventories cover the builtin Go 1.26/1.27 metadata-only path. Other
// launchers, switched toolchains, overlays and workspaces keep full reloads.
// They are not export-data or execution dependency manifests.
type goContextValidation struct {
	installedSDK               *installedSDKIdentity
	inputs                     *sourceSnapshot
	root, version              string
	cache, tmp                 string
	compilers                  map[string]string
	toolDigest, selfDigest     [sha256.Size]byte
	toolEvidence               *goToolEvidence
	self                       string
	launcher, resolvedLauncher string
	selfFile                   os.FileInfo
	selfMode                   os.FileMode
	directoryModes             map[string]os.FileMode
}

func captureSessionGoContext(previous *goContext) *goContext {
	return captureSessionGoContextWithSettings(previous, nil)
}

func captureSessionGoContextWithSettings(previous *goContext, settings []string) *goContext {
	resolved := resolveGoContextWithOptions(goContextOptions{settings: settings, moduleHook: goModuleHook})
	if previous != nil && previous.validation != nil && resolved.err == nil && resolved.driverErr == nil &&
		slices.Equal(resolved.processEnv, previous.processEnv) && resolved.tool == previous.tool &&
		resolved.driver == previous.driver && resolved.self == previous.self && previous.validation.current() {
		return previous
	}
	// Capture before querying Go, so edits during configuration discovery cannot
	// certify a result obtained from earlier settings.
	validation := captureGoContextValidation(resolved)
	ctx := loadGoContext(resolved)
	if validation != nil && validation.accepts(ctx) {
		// A cache path replaced with a file makes even metadata-only Go fail.
		if validation.current() {
			ctx.validation = validation
		}
	}
	return ctx
}

func supportedGoVersion(version string) bool {
	return strings.HasPrefix(version, "go1.26.") || strings.HasPrefix(version, "go1.27.")
}

func captureGoContextValidation(ctx *goContext) *goContextValidation {
	return captureGoContextValidationWithSDK(ctx, nil)
}

// A freshly hashed launcher can use its unchanged file evidence under the
// immutable installed-SDK contract, avoiding a second hash in a CLI request.
func captureGoContextValidationWithSDK(ctx *goContext, sdk *installedSDKIdentity) *goContextValidation {
	if ctx.err != nil || ctx.driverErr != nil || ctx.driver != "off" || runtime.GOOS == "windows" {
		return nil
	}
	info, err := buildinfo.ReadFile(ctx.tool)
	if err != nil || info.Path != "cmd/go" || !supportedGoVersion(info.GoVersion) {
		return nil
	}
	launcher, err := filepath.EvalSymlinks(ctx.tool)
	if err != nil {
		return nil
	}
	root := filepath.Dir(filepath.Dir(launcher))
	if setting := ctx.processValue("GOROOT"); setting != "" {
		root = filepath.Clean(setting)
	}
	if !filepath.IsAbs(root) {
		return nil
	}
	inputs := newSourceSnapshot()
	if inputs.cwdErr != nil {
		return nil
	}
	userFile := ctx.processValue("GOENV")
	if userFile == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil
		}
		userFile = filepath.Join(dir, "go", "env")
	}
	var savedCC bool
	saved := map[string]string{}
	if userFile != "off" {
		data, _ := inputs.readFile(userFile)
		savedCC = savedCompilerSetting(data)
		readSavedGoSettings(data, saved)
	}
	data, _ := inputs.readFile(filepath.Join(root, "go.env"))
	savedCC = savedCC || savedCompilerSetting(data)
	sdkSettings := map[string]string{}
	readSavedGoSettings(data, sdkSettings)
	for key, value := range sdkSettings {
		if _, exists := saved[key]; !exists {
			saved[key] = value
		}
	}
	// A saved CC can mask the compiled-in DefaultCC used to decide cgo
	// availability. Leave that uncommon configuration on full reload.
	if savedCC && ctx.processValue("CC") == "" && !explicitCgoSetting(ctx.processValue("CGO_ENABLED")) {
		return nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	telemetryDir := ctx.processValue("TEST_TELEMETRY_DIR")
	if telemetryDir == "" {
		telemetryDir = filepath.Join(configDir, "go", "telemetry")
	}
	_, _ = inputs.readFile(filepath.Join(telemetryDir, "mode"))
	_, _ = inputs.isDirectory(filepath.Join(root, "pkg", "tool"))
	// Include negative nearer candidates: adding a module/workspace above cwd
	// can change toolchain selection or derived GOMOD/GOWORK without an env edit.
	for dir := inputs.cwd; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"go.mod", "go.work"} {
			path := filepath.Join(dir, name)
			_, _ = inputs.isDirectory(path)
			_, _ = inputs.readFile(path)
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// Default cgo availability depends on whether the platform's default C
	// compiler is executable in PATH. No C compiler result is being cached.
	validation := &goContextValidation{inputs: inputs, root: root, version: info.GoVersion, compilers: map[string]string{}, self: ctx.self, launcher: ctx.tool, resolvedLauncher: launcher, directoryModes: map[string]os.FileMode{}}
	effective := func(key string) string {
		if value := ctx.processValue(key); value != "" {
			return value
		}
		return saved[key]
	}
	validation.tmp = effective("GOTMPDIR")
	if validation.tmp == "" {
		validation.tmp = os.TempDir()
	}
	validation.cache = effective("GOCACHE")
	if validation.cache == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil
		}
		validation.cache = filepath.Join(dir, "go-build")
	}
	if !validation.recordDirectory(validation.cache) || !validation.recordDirectory(validation.tmp) || !validation.recordDirectory(os.TempDir()) {
		return nil
	}
	if sdk != nil {
		if ctx.toolEvidence == nil || ctx.toolEvidence.identity == nil || !sdk.current(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"]) {
			return nil
		}
		file, err := os.Stat(ctx.tool)
		if err != nil {
			return nil
		}
		identity := platformGoToolIdentity(ctx.tool, file)
		if identity == nil || *identity != *ctx.toolEvidence.identity {
			return nil
		}
		validation.toolDigest, validation.installedSDK = ctx.toolDigest, sdk
	} else {
		validation.toolDigest, validation.toolEvidence, err = captureGoToolEvidence(ctx.tool)
		if err != nil {
			return nil
		}
	}
	// Kernel mapped-image identity and the immutable installed-compiler policy
	// avoid rehashing a large self bridge on each validation.
	selfFile, err := os.Stat(ctx.self)
	if err != nil {
		return nil
	}
	validation.selfMode = selfFile.Mode()
	if sameRunningImage(selfFile) {
		validation.selfFile = selfFile
	}
	if validation.selfFile == nil {
		validation.selfDigest, err = goToolDigest(ctx.self)
		if err != nil {
			return nil
		}
	}

	if ctx.processValue("CC") == "" && !explicitCgoSetting(ctx.processValue("CGO_ENABLED")) {
		// Query the actual compiled-in default, rather than assuming gcc/clang
		// for custom distributions. This cheap query runs only when capturing.
		cmd := exec.Command(ctx.tool, "env", "CC")
		cmd.Env = ctx.processEnv
		output, err := cmd.Output()
		if err != nil {
			return nil
		}
		name := strings.TrimSpace(string(output))
		validation.compilers[name] = compilerLocation(name)
	}
	return validation
}

func goTempDirectory(ctx *goContext) string {
	if path := ctx.values["GOTMPDIR"]; path != "" {
		return path
	}
	return os.TempDir()
}

func readSavedGoSettings(data []byte, settings map[string]string) {
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && len(key) != 0 && key[0] >= 'A' && key[0] <= 'Z' {
			settings[key] = value
		}
	}
}

func savedCompilerSetting(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "CC="); ok && value != "" {
			return true
		}
	}
	return false
}

func (v *goContextValidation) recordDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	v.directoryModes[path] = info.Mode()
	return true
}

func explicitCgoSetting(value string) bool { return value == "0" || value == "1" }

func compilerLocation(name string) string {
	path, err := exec.LookPath(name)
	return path + "\x00" + errorText(err)
}

func (v *goContextValidation) accepts(ctx *goContext) bool {
	if ctx.err != nil || ctx.driverErr != nil || ctx.toolDigest != v.toolDigest || ctx.values["GOROOT"] != v.root || ctx.values["GOVERSION"] != v.version ||
		ctx.values["GOCACHE"] != v.cache || goTempDirectory(ctx) != v.tmp || ctx.values["GOFLAGS"] != "" || (ctx.values["GOFIPS140"] != "" && ctx.values["GOFIPS140"] != "off") || (ctx.values["GOWORK"] != "" && ctx.values["GOWORK"] != "off") {
		return false
	}
	switch ctx.values["GOTOOLCHAIN"] {
	case "auto", "local", "":
		return true
	}
	return false
}

func (v *goContextValidation) current() bool {
	return v.toolCurrent() && v.currentConfiguration()
}

// currentConfiguration checks the non-content launcher and configuration inputs.
// Callers that use it directly must separately certify the launcher bytes.
func (v *goContextValidation) currentConfiguration() bool {
	if launcher, err := filepath.EvalSymlinks(v.launcher); err != nil || launcher != v.resolvedLauncher {
		return false
	}
	if v.selfFile != nil {
		file, err := os.Stat(v.self)
		if err != nil || !os.SameFile(file, v.selfFile) || file.Mode() != v.selfFile.Mode() {
			return false
		}
	} else {
		file, err := os.Stat(v.self)
		if err != nil || file.Mode() != v.selfMode {
			return false
		}
		if digest, err := goToolDigest(v.self); err != nil || digest != v.selfDigest {
			return false
		}
	}
	if !v.inputs.current() {
		return false
	}
	for path, mode := range v.directoryModes {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || info.Mode() != mode {
			return false
		}
	}
	for name, location := range v.compilers {
		if compilerLocation(name) != location {
			return false
		}
	}
	return true
}

// The filtered -find query suppresses embed resolution, build info and import
// errors. Its observable package name/error depends on its own file membership,
// source bytes and target settings. Record all immediate files: ignored/test
// sources and assembly build constraints can change selection and errors too.
type goNameValidation struct {
	context     *goContextValidation
	directories *sourceSnapshot
	files       map[string][sha256.Size]byte
}

func captureStandardNameInputs(ctx *goContext, paths []string) *goNameValidation {
	if ctx.err != nil || ctx.validation == nil {
		return nil
	}
	inputs := &goNameValidation{context: ctx.validation, directories: newSourceSnapshot(), files: map[string][sha256.Size]byte{}}
	buffer := make([]byte, 32*1024)
	for _, path := range paths {
		first, _, _ := strings.Cut(path, "/")
		if strings.Contains(first, ".") || path == "" || filepath.ToSlash(filepath.Clean(path)) != path || strings.ContainsAny(path, "*\\") {
			return nil
		}
		dir := filepath.Join(ctx.values["GOROOT"], "src", filepath.FromSlash(path))
		entries, err := inputs.directories.directory(dir)
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if entry.directory {
				continue
			}
			name := filepath.Join(dir, entry.name)
			digest, err := goFileDigest(name, buffer)
			if err != nil {
				return nil
			}
			inputs.files[name] = digest
		}
	}
	return inputs
}

func goFileDigest(name string, buffer []byte) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	info, err := os.Stat(name)
	if err != nil {
		return digest, err
	}
	if !info.Mode().IsRegular() {
		return digest, fmt.Errorf("metadata input is not a regular file: %s", name)
	}
	file, err := os.Open(name)
	if err != nil {
		return digest, err
	}
	hash := sha256.New()
	// Hide os.File.WriteTo so all files share the supplied buffer.
	_, err = io.CopyBuffer(hash, struct{ io.Reader }{file}, buffer)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	copy(digest[:], hash.Sum(nil))
	return digest, err
}

func (v *goNameValidation) current() bool {
	return v.context.current() && v.currentMetadata()
}

// currentMetadata validates the SDK inventory. Its owner validates configuration
// separately, avoiding a full launcher hash per metadata receipt.
func (v *goNameValidation) currentMetadata() bool {
	if v.context.installedSDK != nil {
		return v.context.installedSDK.current(v.context.launcher, v.context.root, v.context.version)
	}
	if !v.directories.current() {
		return false
	}
	buffer := make([]byte, 32*1024)
	for name, expected := range v.files {
		if digest, err := goFileDigest(name, buffer); err != nil || digest != expected {
			return false
		}
	}
	return v.directories.current()
}
