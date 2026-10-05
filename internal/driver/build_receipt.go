package driver

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const buildReceiptLimit = 64 << 20

// Build receipts cover the emitted program and concrete Go dependency inputs.
// They do not certify the compiler's checks or any compile-time execution;
// buildOutput must obtain freshly checked or independently validated Go source.
type buildReceipt struct {
	Schema    int
	Input     [sha256.Size]byte
	Output    buildExecutableIdentity
	Declined  bool
	SDK       *installedSDKIdentity
	Inventory *buildInventory
}

type buildExecutableIdentity struct {
	Digest [sha256.Size]byte
	Mode   os.FileMode
}

type buildInventory struct {
	Stage       string
	Packages    []buildPackage
	Files       map[string]buildFileInput
	Directories map[string]buildDirectoryIdentity
}

type buildPackage struct {
	Dir, ImportPath                                                                  string
	Standard                                                                         bool
	GoFiles, CgoFiles, SFiles, HFiles, SysoFiles, EmbedFiles, EmbedPatterns, Imports []string
	Module                                                                           *buildModule
	Error                                                                            *json.RawMessage
}

type buildModule struct {
	Path, Version, GoMod string
	Replace              *buildModule
}

// Directory identities detect additions, removals and symlink replacements
// without enumerating dependencies on a hit. File contents are always hashed.
type buildDirectoryIdentity struct {
	Device, Inode                            uint64
	Mode                                     os.FileMode
	MtimeSec, MtimeNsec, CtimeSec, CtimeNsec int64
}

type buildFileInput struct {
	Digest   [sha256.Size]byte
	Identity buildDirectoryIdentity
	Size     int64
}

func buildWithReceipt(program *compiledProgram, out, dir string, pinned, stable bool, files map[string][]byte) error {
	ctx := program.context
	encoded, _ := json.Marshal(struct {
		Schema  int
		Files   map[string][]byte
		Context [sha256.Size]byte
		Stage   string
	}{1, files, ctx.namespace, dir})
	input := sha256.Sum256(encoded)
	name := filepath.Join(filepath.Dir(dir), fmtBuildReceiptName(out))
	var receipt *buildReceipt
	var seed *buildExecutableIdentity
	if stable && !cacheDisabled() {
		receipt = readBuildReceipt(name)
		if receipt != nil {
			if actual, err := readBuildExecutable(out); err == nil && actual == receipt.Output && actual.Mode.Perm()&0111 != 0 {
				seed = &receipt.Output
			}
		}
		if receipt != nil && seed != nil && !receipt.Declined && receipt.Input == input &&
			receipt.SDK.current(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"]) &&
			receipt.Inventory.current() {
			testCacheProbeAt("BORK_TEST_BUILD_CACHE_PROBE", "hit")
			return nil
		}
	}
	var sdk *installedSDKIdentity
	var inventory *buildInventory
	if stable && !cacheDisabled() && ctx.processValue("GOCACHEPROG") == "" && ctx.processValue("GO_EXTLINK_ENABLED") != "1" {
		if info, err := buildinfo.ReadFile(ctx.tool); err == nil && info.Path == "cmd/go" && supportedGoVersion(info.GoVersion) {
			sdk = captureInstalledSDK(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
			knownDecline := receipt != nil && receipt.Declined && receipt.Input == input && receipt.SDK.current(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
			if sdk != nil && !knownDecline {
				inventory = captureBuildInventory(dir, ctx)
			}
		}
	}
	output, err := buildAtomicOutput(program, out, dir, pinned, seed)
	if err != nil {
		return err
	}
	if inventory != nil && sdk.current(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"]) && inventory.current() {
		// Recheck package selection after the build. A directory edit between
		// go list and the first snapshot cannot certify an incomplete closure.
		after := captureBuildInventory(dir, ctx)
		beforeBytes, _ := json.Marshal(inventory)
		afterBytes, _ := json.Marshal(after)
		if bytes.Equal(beforeBytes, afterBytes) && inventory.current() {
			writeBuildReceipt(name, &buildReceipt{Schema: 1, Input: input, Output: output, SDK: sdk, Inventory: inventory})
			testCacheProbeAt("BORK_TEST_BUILD_CACHE_PROBE", "miss")
			return nil
		}
	}
	if stable && !cacheDisabled() {
		// A declined receipt can seed a future Go target with independently
		// verified bytes, but never authorizes skipping Go's build checks.
		writeBuildReceipt(name, &buildReceipt{Schema: 1, Input: input, Output: output, SDK: sdk, Declined: true})
	}
	testCacheProbeAt("BORK_TEST_BUILD_CACHE_PROBE", "bypass")
	return nil
}

func fmtBuildReceiptName(out string) string {
	digest := sha256.Sum256([]byte(out))
	return fmt.Sprintf("executable-%x.json", digest)
}

func readBuildReceipt(path string) *buildReceipt {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, buildReceiptLimit+1))
	if err != nil || len(data) > buildReceiptLimit || validateCacheJSON(data) != nil {
		return nil
	}
	var receipt buildReceipt
	var envelope cacheArtifactEnvelope
	if decodeStrictCacheJSON(data, &envelope) != nil || envelope.Schema != 1 {
		return nil
	}
	digest := sha256.Sum256(envelope.Body)
	if envelope.Checksum != fmt.Sprintf("%x", digest) || decodeStrictCacheJSON(envelope.Body, &receipt) != nil || receipt.Schema != 1 {
		return nil
	}
	canonical, err := json.Marshal(&receipt)
	if err != nil || !bytes.Equal(canonical, envelope.Body) || (!receipt.Declined && (receipt.SDK == nil || receipt.Inventory == nil || len(receipt.Inventory.Packages) == 0)) {
		return nil
	}
	return &receipt
}

func writeBuildReceipt(path string, receipt *buildReceipt) {
	data, err := json.Marshal(receipt)
	if err != nil || len(data) > buildReceiptLimit {
		return
	}
	digest := sha256.Sum256(data)
	data, err = json.Marshal(cacheArtifactEnvelope{Schema: 1, Body: data, Checksum: fmt.Sprintf("%x", digest)})
	if err != nil || len(data) > buildReceiptLimit {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".build-receipt-*")
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

func captureBuildInventory(dir string, ctx *goContext) *buildInventory {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil
	}
	cmd := ctx.command("list", "-deps", "-json", "-mod=readonly", "-buildvcs=false", ".")
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=")
	data, err := cmd.Output()
	if err != nil || len(data) > buildReceiptLimit {
		return nil
	}
	result := &buildInventory{Stage: canonical, Files: map[string]buildFileInput{}, Directories: map[string]buildDirectoryIdentity{}}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg buildPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil || pkg.Error != nil || !filepath.IsAbs(pkg.Dir) || len(pkg.CgoFiles) != 0 {
			return nil
		}
		result.Packages = append(result.Packages, pkg)
		if pkg.Standard || pkg.Dir == canonical {
			continue
		}
		// Assembly includes and external embed globs need a wider closure than
		// Go's package file listing. Leave those builds on Go's own cache.
		if len(pkg.SFiles)+len(pkg.HFiles)+len(pkg.SysoFiles)+len(pkg.EmbedPatterns) != 0 {
			return nil
		}
		identity, ok := buildDirectoryStat(pkg.Dir)
		if !ok {
			return nil
		}
		result.Directories[pkg.Dir] = identity
		for _, name := range pkg.GoFiles {
			if !filepath.IsLocal(name) || !result.captureFile(filepath.Join(pkg.Dir, name)) {
				return nil
			}
		}
		for module := pkg.Module; module != nil; module = module.Replace {
			if module.GoMod != "" && !result.captureFile(module.GoMod) {
				return nil
			}
		}
	}
	if len(result.Packages) == 0 || !result.current() {
		return nil
	}
	return result
}

func (inventory *buildInventory) captureFile(path string) bool {
	if !filepath.IsAbs(path) || !validReceiptPath(path) {
		return false
	}
	input, ok := captureBuildFile(path)
	if !ok {
		return false
	}
	inventory.Files[path] = input
	return len(inventory.Files) <= goStageInventoryLimit
}

func (inventory *buildInventory) current() bool {
	if inventory == nil || len(inventory.Files) > goStageInventoryLimit || len(inventory.Directories) > goStageInventoryLimit {
		return false
	}
	for path, before := range inventory.Directories {
		if !filepath.IsAbs(path) || !validReceiptPath(path) {
			return false
		}
		if after, ok := buildDirectoryStat(path); !ok || before != after {
			return false
		}
	}
	for path, expected := range inventory.Files {
		if !filepath.IsAbs(path) || !validReceiptPath(path) {
			return false
		}
		if input, ok := captureBuildFile(path); !ok || input != expected {
			return false
		}
	}
	// The graph itself is persisted so deletion of its concrete observations
	// cannot silently turn a dependency-bearing receipt into an empty one.
	for _, pkg := range inventory.Packages {
		if pkg.Error != nil || len(pkg.CgoFiles) != 0 || !filepath.IsAbs(pkg.Dir) || !validReceiptPath(pkg.Dir) {
			return false
		}
		if !pkg.Standard && pkg.Dir != inventory.Stage {
			if len(pkg.SFiles)+len(pkg.HFiles)+len(pkg.SysoFiles)+len(pkg.EmbedPatterns) != 0 {
				return false
			}
			if _, ok := inventory.Directories[pkg.Dir]; !ok {
				return false
			}
			for _, file := range pkg.GoFiles {
				if !filepath.IsLocal(file) {
					return false
				}
				if _, ok := inventory.Files[filepath.Join(pkg.Dir, file)]; !ok {
					return false
				}
			}
			for module := pkg.Module; module != nil; module = module.Replace {
				if module.GoMod != "" {
					if _, ok := inventory.Files[module.GoMod]; !ok {
						return false
					}
				}
			}
		}
	}
	return true
}

func readBuildExecutable(path string) (buildExecutableIdentity, error) {
	input, ok := captureBuildFile(path)
	if !ok {
		return buildExecutableIdentity{}, errInvalidCacheArtifact
	}
	return buildExecutableIdentity{input.Digest, input.Identity.Mode}, nil
}

func captureBuildFile(path string) (buildFileInput, bool) {
	var zero buildFileInput
	before, err := os.Stat(path)
	if err != nil {
		return zero, false
	}
	id := platformGoToolIdentity(path, before)
	if id == nil {
		return zero, false
	}
	digest, err := buildFileDigest(path)
	if err != nil {
		return zero, false
	}
	after, err := os.Stat(path)
	if err != nil {
		return zero, false
	}
	last := platformGoToolIdentity(path, after)
	if last == nil || *id != *last {
		return zero, false
	}
	return buildFileInput{digest, buildDirectoryIdentity{id.device, id.inode, id.mode, id.mtimeSec, id.mtimeNsec, id.ctimeSec, id.ctimeNsec}, id.size}, true
}

func buildFileDigest(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return result, errInvalidCacheArtifact
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, err
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}
