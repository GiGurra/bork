package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPersistentProofReceipt(t *testing.T) {
	t.Parallel()
	ctx := captureSessionGoContext(nil)
	if ctx.validation == nil {
		t.Skip("supported installed Go context required")
	}
	root := t.TempDir()
	key := [sha256.Size]byte{1}
	stageKey := hex.EncodeToString(key[:])
	dir, _, release, err := stageGoStable(root, stageKey, []byte("package main\nfunc main(){}\n"), &goModuleInputs{mod: []byte("module proof\n")}, nil,
		goStageMetadata{Schema: goStageSchema, Program: root, Mode: "predicate"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	sdk := captureInstalledSDK(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
	if sdk == nil {
		t.Skip("installed SDK identity unavailable")
	}
	ctx.validation.installedSDK = sdk
	proof := &persistentProof{root: root, namespace: [32]byte{2}, sdk: sdk, context: ctx}
	if !proof.bind(dir) || proof.bind(t.TempDir()) {
		t.Fatal("stable/temporary stage classification")
	}
	results := []bool{true, false}
	proof.write(key, results)
	got, ok := proof.read(key, len(results))
	if !ok || !reflect.DeepEqual(got, results) {
		t.Fatalf("roundtrip: %v %v", got, ok)
	}
	got[0] = false
	if again, ok := proof.read(key, len(results)); !ok || !again[0] {
		t.Fatal("mutable result escaped")
	}
	if _, ok := proof.read(key, 1); ok {
		t.Fatal("wrong batch length accepted")
	}
	file := filepath.Join(root, proof.path)
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var envelope cacheArtifactEnvelope
	if err := json.Unmarshal(original, &envelope); err != nil {
		t.Fatal(err)
	}
	var body persistentProofBody
	if err := json.Unmarshal(envelope.Body, &body); err != nil {
		t.Fatal(err)
	}
	encode := func(body []byte) []byte {
		digest := sha256.Sum256(body)
		data, err := json.Marshal(cacheArtifactEnvelope{Schema: 1, Body: body, Checksum: hex.EncodeToString(digest[:])})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	changed := func(mutate func(*persistentProofBody)) []byte {
		copy := body
		copySDK := *body.SDK
		copy.SDK = &copySDK
		mutate(&copy)
		data, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		return encode(data)
	}
	cases := map[string][]byte{
		"truncated": original[:len(original)/2],
		"oversized": make([]byte, persistentProofMaxBytes+1),
		"checksum":  []byte(`{"schema":1,"body":{},"checksum":"wrong"}`),
		"compiler":  changed(func(b *persistentProofBody) { b.Namespace[0]++ }),
		"key":       changed(func(b *persistentProofBody) { b.Key[0]++ }),
		"sdk":       changed(func(b *persistentProofBody) { b.SDK.Tool.Inode++ }),
		"duplicate": encode([]byte(`{"key":[],"key":[]}`)),
		"unknown":   encode([]byte(`{"unexpected":true}`)),
		"nonbool":   encode([]byte(`{"results":["true",false]}`)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, ok := proof.read(key, len(results)); ok {
				t.Fatal("corrupt or mismatched receipt accepted")
			}
		})
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if _, ok := proof.read(key, len(results)); ok {
		t.Fatal("symlink receipt accepted")
	}
	// Atomic publication replaces the symlink, leaving its target untouched.
	proof.write(key, results)
	if _, ok := proof.read(key, len(results)); !ok {
		t.Fatal("failed to repair symlink sidecar")
	}
	if data, err := os.ReadFile(outside); err != nil || !reflect.DeepEqual(data, original) {
		t.Fatal("publication followed a symlink")
	}
	changedSDK := *sdk
	changedSDK.Tool.Inode++
	proof.sdk = &changedSDK
	if _, ok := proof.read(key, len(results)); ok {
		t.Fatal("stale SDK accepted")
	}
}

func TestPersistentProofLauncherEvidence(t *testing.T) {
	t.Parallel()
	ctx := captureGoContext()
	if ctx.err != nil || ctx.toolEvidence == nil || ctx.toolEvidence.identity == nil {
		t.Skip("portable native launcher evidence required")
	}
	sdk := captureInstalledSDK(ctx.tool, ctx.values["GOROOT"], ctx.values["GOVERSION"])
	if sdk == nil {
		t.Skip("installed SDK identity unavailable")
	}
	validation := captureGoContextValidationWithSDK(ctx, sdk)
	if validation == nil || !validation.accepts(ctx) || !validation.current() {
		t.Fatal("fresh launcher evidence did not qualify")
	}
	identity := *ctx.toolEvidence.identity
	identity.inode++
	ctx.toolEvidence = &goToolEvidence{identity: &identity}
	if captureGoContextValidationWithSDK(ctx, sdk) != nil {
		t.Fatal("changed launcher reused earlier hash evidence")
	}
}
