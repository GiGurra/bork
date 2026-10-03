package driver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// goContext freezes process/effective Go settings for one compilation. Missing
// Go remains a deferred error: check-only programs that need no Go type/evaluator
// work retain their existing behavior. This is not a complete reuse key for
// external package metadata, toolchain contents or evaluator effects.
type goContext struct {
	processEnv []string
	env        []string
	tool       string
	driver     string
	self       string
	values     map[string]string
	err        error
	driverErr  error
	namesCache bool
	namespace  [sha256.Size]byte
}

func captureGoContext() *goContext {
	ctx := &goContext{processEnv: slices.Clone(os.Environ())}
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
	cmd := exec.Command(ctx.tool, "env", "-json")
	cmd.Env = ctx.processEnv
	output, err := cmd.Output()
	if err != nil {
		ctx.err = err
		return ctx
	}
	if err := json.Unmarshal(output, &ctx.values); err != nil {
		ctx.err = err
		return ctx
	}
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
	// Include launcher bytes, not just its path/version. A full toolchain/input
	// inventory is still required before compilation-result reuse.
	toolFile, err := os.Open(ctx.tool)
	if err != nil {
		ctx.err = err
		return ctx
	}
	toolHash := sha256.New()
	_, hashErr := io.Copy(toolHash, toolFile)
	closeErr := toolFile.Close()
	if hashErr != nil {
		ctx.err = hashErr
		return ctx
	}
	if closeErr != nil {
		ctx.err = closeErr
		return ctx
	}
	identity = append(identity, ctx.tool, ctx.driver, fmt.Sprintf("%x", toolHash.Sum(nil)))
	encoded, _ := json.Marshal(identity)
	ctx.namespace = sha256.Sum256(encoded)
	ctx.namesCache = ctx.values["GO111MODULE"] != "off" && ctx.driver == "off" && ctx.driverErr == nil
	return ctx
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
