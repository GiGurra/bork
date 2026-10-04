package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

const persistentProofMaxBytes = 512 << 10

// One owned boolean batch lives beside a stable predicate stage. Its existing
// slot protects lookup/publication and its existing clean/trim lifecycle owns
// the sidecar. This never certifies an enclosing compilation or a comptime value.
type persistentProof struct {
	root, path string
	namespace  [sha256.Size]byte
	sdk        *installedSDKIdentity
	context    *goContext
	state      *cacheCLIStartup
}

type persistentProofBody struct {
	Namespace [sha256.Size]byte     `json:"namespace"`
	Key       [sha256.Size]byte     `json:"key"`
	SDK       *installedSDKIdentity `json:"sdk"`
	Results   []bool                `json:"results"`
}

// Only CLI closed-proof requests opt in. Reuse requires the same supported
// installed native toolchain contract as Session proofs; uncertain settings,
// wrappers and automatically switched generated toolchains execute afresh.
func preparePersistentProof(ctx *goContext) *persistentProof {
	state := cacheCLIState
	if state == nil || cacheDisabled() || ctx.values["GOOS"] != runtime.GOOS || ctx.values["GOARCH"] != runtime.GOARCH {
		return nil
	}
	sdk := captureInstalledSDK(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
	if sdk == nil {
		return nil
	}
	validation := captureGoContextValidationWithSDK(ctx, sdk)
	if validation == nil || !validation.accepts(ctx) || !validation.current() {
		return nil
	}
	ctx.validation = validation
	state.startIdentity()

	root, err := cacheRootDir()
	if err != nil {
		return nil
	}
	return &persistentProof{root: root, sdk: sdk, context: ctx, state: state}
}

// Temporary stages never persist. Call only while the stable stage slot remains
// held, including through execution and publication on a miss.
func (p *persistentProof) bind(dir string) bool {
	rel, err := filepath.Rel(p.root, dir)
	if err != nil || filepath.Base(rel) != "tree" {
		return false
	}
	entry := filepath.Dir(rel)
	key := filepath.Base(entry)
	if _, ok := cacheHexDigest(key); !ok || entry != goStageEntryPath(key) {
		return false
	}
	p.path = filepath.Join(entry, "proof-v1.json")
	return true
}

func (p *persistentProof) identity() bool {
	if p.state != nil {
		<-p.state.done
		if p.state.err != nil {
			return false
		}
		p.namespace = p.state.namespace
	}
	return true
}

func (p *persistentProof) current() bool {
	ctx := p.context
	return ctx.validation != nil && ctx.validation.accepts(ctx) && ctx.validation.current() && p.sdk.current(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
}

func (p *persistentProof) read(key [sha256.Size]byte, count int) ([]bool, bool) {
	if count > predicateMemoResultBytes || !p.current() {
		return nil, false
	}
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return nil, false
	}
	defer func() { _ = root.Close() }()
	if validateStageDirectory(root, filepath.Dir(p.path)) != nil {
		return nil, false
	}
	file, err := openCacheFile(root, p.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, persistentProofMaxBytes+1))
	if err != nil || len(data) > persistentProofMaxBytes || !p.identity() || validateCacheJSON(data) != nil {
		return nil, false
	}
	var envelope cacheArtifactEnvelope
	if decodeStrictCacheJSON(data, &envelope) != nil || envelope.Schema != 1 {
		return nil, false
	}
	digest := sha256.Sum256(envelope.Body)
	if hex.EncodeToString(digest[:]) != envelope.Checksum {
		return nil, false
	}
	var body persistentProofBody
	if decodeStrictCacheJSON(envelope.Body, &body) != nil {
		return nil, false
	}
	canonical, err := json.Marshal(body)
	if err != nil || !bytes.Equal(canonical, envelope.Body) || body.Namespace != p.namespace || body.Key != key || body.SDK == nil || *body.SDK != *p.sdk || len(body.Results) != count || !p.current() {
		return nil, false
	}
	testCacheProbeAt("BORK_TEST_PROOF_CACHE_PROBE", "hit")
	return slices.Clone(body.Results), true
}

func (p *persistentProof) write(key [sha256.Size]byte, results []bool) {
	if len(results) > predicateMemoResultBytes || !p.current() || !p.identity() {
		return
	}
	body, err := json.Marshal(persistentProofBody{p.namespace, key, p.sdk, results})
	if err != nil {
		return
	}
	digest := sha256.Sum256(body)
	data, err := json.Marshal(cacheArtifactEnvelope{Schema: 1, Body: body, Checksum: hex.EncodeToString(digest[:])})
	if err != nil || len(data) > persistentProofMaxBytes {
		return
	}
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	if validateStageDirectory(root, filepath.Dir(p.path)) != nil {
		return
	}
	pending := filepath.Join(filepath.Dir(p.path), "proof-v1.next")
	_ = root.Remove(pending)
	file, err := openCacheFile(root, pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer func() { _ = root.Remove(pending) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil && p.current() && root.Rename(pending, p.path) == nil {
		testCacheProbeAt("BORK_TEST_PROOF_CACHE_PROBE", "published")
	}
}
