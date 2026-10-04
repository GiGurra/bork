package driver

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"github.com/GiGurra/bork/internal/gotoolchain"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// goContext freezes process/effective Go settings for one compilation. Missing
// Go remains a deferred error: check-only programs that need no Go type/evaluator
// work retain their existing behavior. This is not a complete reuse key for
// external package metadata, toolchain contents or evaluator effects.
type goContext struct {
	// The module transform is captured once; all metadata and build stages use it.
	moduleHook goModuleHookFunc
	// Tests can shorten comptime deadlines; a nonzero limit changes execution
	// policy and must be included in any future evaluator reuse identity.
	evalLimit    time.Duration
	processEnv   []string
	env          []string
	tool         string
	driver       string
	self         string
	values       map[string]string
	err          error
	driverErr    error
	namesCache   bool
	namespace    [sha256.Size]byte
	toolDigest   [sha256.Size]byte
	sdkDigest    [sha256.Size]byte
	toolEvidence *goToolEvidence
	validation   *goContextValidation

	// Tests retain the standalone ordinary recipe path for clean parity.
	comptimeStandalone bool
}

// goContextOptions owns per-call Go settings. Overrides affect Go subprocesses;
// launcher discovery still uses the caller's PATH. Defaults are assigned once
// before compilation starts, and a captured context never rereads the hook.
type goContextOptions struct {
	settings   []string
	moduleHook goModuleHookFunc
}

func captureGoContext() *goContext {
	return captureGoContextWithOptions(goContextOptions{moduleHook: goModuleHook})
}

func captureGoContextWithOptions(options goContextOptions) *goContext {
	return loadGoContext(resolveGoContextWithOptions(options))
}

func resolveGoContext() *goContext {
	return resolveGoContextWithOptions(goContextOptions{moduleHook: goModuleHook})
}

func resolveGoContextWithOptions(options goContextOptions) *goContext {
	ctx := &goContext{processEnv: append(slices.Clone(os.Environ()), options.settings...), moduleHook: options.moduleHook}
	ctx.env = slices.Clone(ctx.processEnv)
	ctx.self, _ = os.Executable()
	ctx.driver = ctx.processValue("GOPACKAGESDRIVER")
	switch ctx.driver {
	case "off":
	case "":
		if driver, err := exec.LookPath("gopackagesdriver"); err == nil {
			ctx.driver, _ = filepath.Abs(driver)
		} else {
			ctx.driver = "off"
		}
	default:
		driver, err := exec.LookPath(ctx.driver)
		if err != nil {
			ctx.driverErr = err
		} else {
			ctx.driver, ctx.driverErr = filepath.Abs(driver)
		}
	}
	tool, err := exec.LookPath("go")
	if err != nil {
		ctx.err = err
		return ctx
	}
	ctx.tool, err = filepath.Abs(tool)
	if err != nil {
		ctx.err = err
		return ctx
	}
	return ctx
}

func loadGoContext(ctx *goContext) *goContext {
	if ctx.err != nil {
		return ctx
	}
	values, env, tool, err := gotoolchain.Query(ctx.tool, "", ctx.processEnv)
	if err != nil {
		ctx.err = err
		return ctx
	}
	ctx.values = values
	ctx.env = slices.Clone(env)
	// A real Go launcher may have switched to another installed SDK. Hash and
	// invoke that SDK's executable so cache identity reflects the selected Go.
	ctx.tool = tool
	// Wrappers retain their behavior, but compilation identity also includes
	// the selected SDK executable rather than only the wrapper's bytes.
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	selected := filepath.Join(values["GOROOT"], "bin", name)
	if info, readErr := buildinfo.ReadFile(ctx.tool); readErr != nil || info.Path != "cmd/go" {
		ctx.sdkDigest, err = goToolDigest(selected)
		if err != nil {
			ctx.err = err
			return ctx
		}
	}

	// Include launcher bytes, not just its path/version. A full toolchain/input
	// inventory is still required before compilation-result reuse.
	ctx.toolDigest, ctx.toolEvidence, err = captureGoToolEvidence(ctx.tool)
	if err != nil {
		ctx.err = err
		return ctx
	}
	ctx.pinSettings()
	ctx.namesCache = ctx.values["GO111MODULE"] != "off" && ctx.driver == "off" && ctx.driverErr == nil
	return ctx
}

// pinSettings derives subprocess settings and namespace from captured values.
func (ctx *goContext) pinSettings() {
	// Prevent later saved-GOENV edits from changing a generated subprocess's
	// settings. Derived read-only values are identity evidence, not overrides.
	keys := make([]string, 0, len(ctx.values))
	for key := range ctx.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	identity := slices.Clone(ctx.processEnv)
	for _, key := range keys {
		if key == "GOGCCFLAGS" {
			continue
		} // includes a fresh temporary build path
		identity = append(identity, key+"="+ctx.values[key])
		if !readOnlyGoSetting(key) {
			ctx.env = append(ctx.env, key+"="+ctx.values[key])
		}
	}
	ctx.env = append(ctx.env, "GOENV=off")
	identity = append(identity, ctx.tool, ctx.driver, fmt.Sprintf("%x", ctx.toolDigest), fmt.Sprintf("%x", ctx.sdkDigest))
	encoded, _ := json.Marshal(identity)
	ctx.namespace = sha256.Sum256(encoded)
}

func readOnlyGoSetting(key string) bool {
	switch key {
	case "GOENV", "GOHOSTARCH", "GOHOSTOS", "GOMOD", "GOWORK", "GOTOOLDIR", "GOVERSION", "GOGCCFLAGS", "GOTELEMETRYDIR":
		return true
	}
	return false
}
func (ctx *goContext) processValue(key string) string {
	prefix := key + "="
	for i := len(ctx.processEnv) - 1; i >= 0; i-- {
		if strings.HasPrefix(ctx.processEnv[i], prefix) {
			return strings.TrimPrefix(ctx.processEnv[i], prefix)
		}
	}
	return ""
}
func (ctx *goContext) command(args ...string) *exec.Cmd {
	tool := ctx.tool
	if tool == "" {
		tool = "go"
	}
	cmd := exec.Command(tool, args...)
	cmd.Env = slices.Clone(ctx.env)
	return cmd
}
func (ctx *goContext) metadataEnv() []string {
	return append(slices.Clone(ctx.env), "GOWORK=off", "GOFLAGS=-mod=readonly")
}

func goToolDigest(path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	copy(digest[:], hash.Sum(nil))
	return digest, err
}
