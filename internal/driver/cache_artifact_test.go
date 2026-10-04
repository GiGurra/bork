package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func cacheArtifactFixture(t *testing.T) (*cacheArtifactBody, *sessionArtifact) {
	t.Helper()
	_ = receiptGoContext(t)
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Emit(path); err != nil {
		t.Fatal(err)
	}
	if session.last == nil {
		t.Fatalf("ineligible fixture: %+v", session.Stats())
	}
	files, _, diags, err := loadFrom(path, session.last.inputs)
	if err != nil || diags.Len() != 0 {
		t.Fatalf("fixture replay: %v", err)
	}
	body, err := cacheArtifactFrom(session.last, sourcePaths(files), sha256.Sum256([]byte("fixture compiler")))
	if err != nil {
		t.Fatal(err)
	}
	return body, session.last
}

func TestCacheArtifactRoundtrip(t *testing.T) {
	body, original := cacheArtifactFixture(t)
	body.Warnings = []cacheArtifactWarning{{Pos: diag.Pos{File: body.Request.Path, Line: 1, Col: 2}, Message: "warn", Severity: "warning", Fixes: []diag.Fix{{Message: "fix", Edits: []diag.TextEdit{{Start: diag.Pos{File: body.Request.Path, Line: 1, Col: 2}, Replacement: "new"}}}}}}
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, body.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body, restored) {
		t.Fatal("artifact changed during roundtrip")
	}
	if restored.RootSource != original.rootSource || restored.RootSource == "" {
		t.Fatal("staging origin missing from owned artifact")
	}
	// The public diagnostic JSON normalizes these empty fields; cache JSON must not.
	if restored.Warnings[0].End.File != "" || restored.Warnings[0].Code != "" {
		t.Fatal("warning normalized")
	}
	restored.GoSource[0] ^= 1
	restored.SourcePaths[0] = "changed"
	restored.Module.Mod[0] ^= 1
	if bytes.Equal(restored.GoSource, body.GoSource) || bytes.Equal(restored.Module.Mod, body.Module.Mod) || original.module.mod[0] != body.Module.Mod[0] {
		t.Fatal("artifact does not own output bytes")
	}
	if bytes.Equal(original.goSrc, restored.GoSource) {
		t.Fatal("restored bytes alias retained result")
	}
}

func rechecksumArtifact(t *testing.T, encoded []byte, mutate func([]byte) []byte) []byte {
	t.Helper()
	var envelope cacheArtifactEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Body = mutate(envelope.Body)
	digest := sha256.Sum256(envelope.Body)
	envelope.Checksum = hex.EncodeToString(digest[:])
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCacheArtifactRejectsCorruption(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"truncated", encoded[:len(encoded)-1]},
		{"trailing", append(bytes.Clone(encoded), []byte(" {}")...)},
		{"duplicate-envelope", bytes.Replace(encoded, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)},
		{"unknown-envelope", bytes.Replace(encoded, []byte(`"checksum":`), []byte(`"unknown":0,"checksum":`), 1)},
		{"payload-checksum", bytes.Replace(encoded, []byte(`"schema":1`), []byte(`"schema":2`), 2)},
		{"unknown-body", rechecksumArtifact(t, encoded, func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"schema":1`), []byte(`"unknown":0,"schema":1`), 1)
		})},
		{"duplicate-body", rechecksumArtifact(t, encoded, func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)
		})},
		{"missing-receipt", rechecksumArtifact(t, encoded, func(data []byte) []byte {
			var value map[string]json.RawMessage
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			delete(value, "source")
			result, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			return result
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeCacheArtifact(bytes.NewReader(test.data), body.Namespace, body.Key); err == nil {
				t.Fatal("corrupt artifact accepted")
			}
		})
	}
	changed := body.Namespace
	changed[0] ^= 1
	if _, err := decodeCacheArtifact(bytes.NewReader(encoded), changed, body.Key); err == nil {
		t.Fatal("wrong compiler accepted")
	}
	changed = body.Key
	changed[0] ^= 1
	if _, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, changed); err == nil {
		t.Fatal("wrong request accepted")
	}
}

func TestCacheArtifactChecksumCoversReceipt(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	encoded, err := encodeCacheArtifact(body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope cacheArtifactEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	// This dependency removal leaves a well-formed receipt. Without whole-body
	// integrity, an edit to that now-unobserved source could yield a stale hit.
	body.Source.Reads = body.Source.Reads[1:]
	if !body.valid() {
		t.Fatal("dependency removal fixture no longer structurally valid")
	}
	changed, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Body = changed // deliberately retain the original checksum
	encoded, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeCacheArtifact(bytes.NewReader(encoded), body.Namespace, body.Key); err == nil {
		t.Fatal("receipt mutation escaped checksum")
	}
}

func TestCacheArtifactEncodingBudgets(t *testing.T) {
	body, _ := cacheArtifactFixture(t)
	body.GoSource = make([]byte, cacheArtifactMaxBytes)
	if _, err := encodeCacheArtifact(body); !errors.Is(err, errCacheArtifactBudget) {
		t.Fatalf("oversized encoding: %v", err)
	}
	reader := &cacheCountingReader{remaining: cacheArtifactMaxBytes + 100}
	if _, err := decodeCacheArtifact(reader, body.Namespace, body.Key); !errors.Is(err, errCacheArtifactBudget) {
		t.Fatalf("oversized read: %v", err)
	}
	if reader.read != cacheArtifactMaxBytes+1 {
		t.Fatalf("read %d bytes", reader.read)
	}
	if err := validateCacheJSON([]byte(strings.Repeat("[", cacheArtifactMaxDepth+2) + "0" + strings.Repeat("]", cacheArtifactMaxDepth+2))); !errors.Is(err, errCacheArtifactBudget) {
		t.Fatalf("depth budget: %v", err)
	}
	if err := validateCacheJSON([]byte("[" + strings.Repeat("0,", cacheArtifactMaxNodes) + "0]")); !errors.Is(err, errCacheArtifactBudget) {
		t.Fatalf("node budget: %v", err)
	}
}

type cacheCountingReader struct{ remaining, read int }

func (r *cacheCountingReader) Read(data []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	count := min(len(data), r.remaining)
	clear(data[:count])
	r.remaining -= count
	r.read += count
	return count, nil
}

func TestCacheArtifactDeclinesAssetsAndLossyStrings(t *testing.T) {
	body, artifact := cacheArtifactFixture(t)
	body.Warnings = []cacheArtifactWarning{{Severity: "warning", Message: string([]byte{0xff})}}
	if _, err := encodeCacheArtifact(body); !errors.Is(err, errInvalidCacheArtifact) {
		t.Fatalf("lossy warning: %v", err)
	}
	artifact.assets.reads[embedReadKey{}] = embedRead{}
	if _, err := cacheArtifactFrom(artifact, body.SourcePaths, body.Namespace); err == nil {
		t.Fatal("asset inventory persisted without identity receipt")
	}
	if _, err := cacheArtifactFrom(nil, nil, body.Namespace); err == nil {
		t.Fatal("failed/absent result persisted")
	}
}

func TestCacheArtifactCompositeCertification(t *testing.T) {
	for _, change := range []string{"source", "metadata"} {
		t.Run(change, func(t *testing.T) {
			body, artifact := cacheArtifactFixture(t)
			switch change {
			case "source":
				if err := os.WriteFile(body.Request.Path, []byte("fn main() { println(1) }\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if len(artifact.names) == 0 {
					t.Fatal("no metadata inventory")
				}
				for path, digest := range artifact.names[0].inputs.files {
					digest[0] ^= 1
					artifact.names[0].inputs.files[path] = digest
					break
				}
			}
			if _, err := cacheArtifactFrom(artifact, body.SourcePaths, body.Namespace); err == nil {
				t.Fatal("composite certification accepted changed input")
			}
		})
	}
}

func TestCachedBuildPreservesRootWithPreludePathCollision(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main.bork")
	// The embedded prelude's relative path also names an imported disk file.
	collision := "prelude/prelude.bork"
	files := []*syntax.File{{Path: collision, Prelude: true}, {Path: main}, {Path: collision, Package: "example.com/prelude"}}
	fresh, err := goStageProgramRoot(files)
	if err != nil {
		t.Fatal(err)
	}
	result := &cachedCompilation{rootSource: goStageRootSource(files), sourcePaths: sourcePaths(files)}
	cached, err := goStageProgramRoot(cachedBuildFiles(result))
	if err != nil || cached != fresh {
		t.Fatalf("staging root changed: fresh=%q cached=%q error=%v", fresh, cached, err)
	}
}
