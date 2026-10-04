package driver

import (
	"crypto/sha256"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"unicode/utf8"
)

const goReceiptSchema = 1

var errUnsupportedGoReceipt = errors.New("cannot persist Go observations")

// This is only the supported metadata-name configuration inventory, not an
// export-data or execution receipt. No stat tuple or stable-since time persists.
type goContextReceipt struct {
	Schema           int                    `json:"schema"`
	ProcessEnv       []string               `json:"process_env"`
	Values           map[string]string      `json:"values"`
	Tool             string                 `json:"tool"`
	Driver           string                 `json:"driver"`
	Self             string                 `json:"self"`
	ToolDigest       [sha256.Size]byte      `json:"tool_digest"`
	BridgeDigest     [sha256.Size]byte      `json:"bridge_digest"`
	BridgeMode       os.FileMode            `json:"bridge_mode"`
	Inputs           *sourceReceipt         `json:"inputs"`
	Root             string                 `json:"root"`
	Version          string                 `json:"version"`
	Cache            string                 `json:"cache"`
	Tmp              string                 `json:"tmp"`
	ResolvedLauncher string                 `json:"resolved_launcher"`
	Compilers        map[string]string      `json:"compilers"`
	DirectoryModes   map[string]os.FileMode `json:"directory_modes"`
}

func (ctx *goContext) receipt() (*goContextReceipt, error) {
	if ctx == nil || ctx.validation == nil || !ctx.validation.accepts(ctx) || !ctx.validation.current() {
		return nil, errUnsupportedGoReceipt
	}
	v := ctx.validation
	inputs, err := v.inputs.receipt()
	if err != nil {
		return nil, err
	}
	bridge, _, err := freshBridgeEvidence(v.self)
	if err != nil {
		return nil, err
	}
	out := &goContextReceipt{Schema: goReceiptSchema, ProcessEnv: slices.Clone(ctx.processEnv), Values: maps.Clone(ctx.values), Tool: ctx.tool, Driver: ctx.driver, Self: ctx.self, ToolDigest: ctx.toolDigest, BridgeDigest: bridge, BridgeMode: v.selfMode, Inputs: inputs, Root: v.root, Version: v.version, Cache: v.cache, Tmp: v.tmp, ResolvedLauncher: v.resolvedLauncher, Compilers: maps.Clone(v.compilers), DirectoryModes: maps.Clone(v.directoryModes)}
	if !out.valid() || !v.current() {
		return nil, errUnsupportedGoReceipt
	}
	return out, nil
}

// restore performs fresh content validation without running go env. The caller
// must additionally validate its source/name/asset receipts before a result hit.
// resolveGoContext must be fresh; no serialized resolved paths certify PATH.
func (r *goContextReceipt) restore(resolved *goContext) (*goContext, error) {
	if !r.valid() || resolved == nil || resolved.err != nil || resolved.driverErr != nil ||
		!slices.Equal(r.ProcessEnv, resolved.processEnv) || r.Tool != resolved.tool || r.Driver != resolved.driver || r.Self != resolved.self {
		return nil, errUnsupportedGoReceipt
	}
	inputs, err := r.Inputs.snapshot()
	if err != nil {
		return nil, err
	}
	digest, evidence, err := captureGoToolEvidence(resolved.tool)
	if err != nil || digest != r.ToolDigest {
		return nil, errUnsupportedGoReceipt
	}
	bridge, selfFile, err := freshBridgeEvidence(resolved.self)
	if err != nil || bridge != r.BridgeDigest || selfFile.Mode() != r.BridgeMode {
		return nil, errUnsupportedGoReceipt
	}
	ctx := &goContext{processEnv: slices.Clone(r.ProcessEnv), env: slices.Clone(r.ProcessEnv), values: maps.Clone(r.Values), tool: r.Tool, driver: r.Driver, self: r.Self, toolDigest: digest}
	ctx.pinSettings()
	ctx.namesCache = ctx.values["GO111MODULE"] != "off" && ctx.driver == "off"
	v := &goContextValidation{inputs: inputs, root: r.Root, version: r.Version, cache: r.Cache, tmp: r.Tmp, compilers: maps.Clone(r.Compilers), directoryModes: maps.Clone(r.DirectoryModes), launcher: r.Tool, resolvedLauncher: r.ResolvedLauncher, toolDigest: digest, toolEvidence: evidence, self: r.Self, selfMode: r.BridgeMode, selfDigest: bridge}
	if sameRunningImage(selfFile) {
		v.selfFile = selfFile
	}
	if !v.accepts(ctx) || !v.current() {
		return nil, errUnsupportedGoReceipt
	}
	ctx.validation = v
	return ctx, nil
}

func (r *goContextReceipt) valid() bool {
	if r == nil || r.Schema != goReceiptSchema || runtime.GOOS != "linux" || r.Driver != "off" || !supportedGoVersion(r.Version) || r.Inputs == nil {
		return false
	}
	for _, path := range []string{r.Tool, r.Self, r.Root, r.Cache, r.Tmp, r.ResolvedLauncher} {
		if !filepath.IsAbs(path) || !validReceiptPath(path) {
			return false
		}
	}
	for _, value := range r.ProcessEnv {
		if !utf8.ValidString(value) {
			return false
		}
	}
	for key, value := range r.Values {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return false
		}
	}
	for key, value := range r.Compilers {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return false
		}
	}
	if len(r.DirectoryModes) == 0 {
		return false
	}
	for path, mode := range r.DirectoryModes {
		if !filepath.IsAbs(path) || !validReceiptPath(path) || !mode.IsDir() {
			return false
		}
	}
	if !r.BridgeMode.IsRegular() {
		return false
	}
	for _, path := range []string{r.Cache, r.Tmp, os.TempDir()} {
		if _, ok := r.DirectoryModes[path]; !ok {
			return false
		}
	}
	return true
}

func sameRunningImage(file os.FileInfo) bool {
	running, err := os.Stat("/proc/self/exe")
	return err == nil && os.SameFile(file, running)
}

func freshBridgeEvidence(path string) ([sha256.Size]byte, os.FileInfo, error) {
	file, err := os.Stat(path)
	if err != nil || !file.Mode().IsRegular() {
		return [sha256.Size]byte{}, nil, errUnsupportedGoReceipt
	}
	if sameRunningImage(file) {
		digest, err := compilerImageDigest()
		return digest, file, err
	}
	digest, _, err := captureGoToolEvidence(path)
	if err != nil {
		return digest, nil, err
	}
	return digest, file, nil
}
