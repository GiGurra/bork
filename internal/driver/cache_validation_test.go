package driver

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
)

func TestCacheValidationOwnedResult(t *testing.T) {
	body, original := cacheArtifactFixture(t)
	body.Warnings = []cacheArtifactWarning{{Pos: diag.Pos{File: body.Request.Path, Line: 1, Col: 1}, Message: "warning", Severity: "warning", Fixes: []diag.Fix{{Message: "fix", Edits: []diag.TextEdit{{Replacement: "owned"}}}}}}
	result, err := body.validate(body.Request, body.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.goSource, original.goSrc) || !reflect.DeepEqual(result.sourcePaths, body.SourcePaths) || result.context.namespace != original.context.namespace {
		t.Fatal("validated result differs")
	}
	if result.warnings[0].End.File != "" || result.warnings[0].Code != "" {
		t.Fatal("warning normalized")
	}
	result.goSource[0] ^= 1
	result.module.mod[0] ^= 1
	result.sourcePaths[0] = "changed"
	result.warnings[0].Fixes[0].Edits[0].Replacement = "changed"
	if bytes.Equal(result.goSource, body.GoSource) || bytes.Equal(result.module.mod, body.Module.Mod) || body.Warnings[0].Fixes[0].Edits[0].Replacement != "owned" {
		t.Fatal("result aliases receipt")
	}
}

func TestCacheValidationRejectsChanges(t *testing.T) {
	for _, change := range []string{"equal-mtime-source", "directory-membership", "negative-appearance", "environment", "request", "namespace"} {
		t.Run(change, func(t *testing.T) {
			body, original := cacheArtifactFixture(t)
			request, namespace := body.Request, body.Namespace
			switch change {
			case "equal-mtime-source":
				before, err := os.Stat(request.Path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(request.Path, []byte("fn main() { let x = 1 }\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(request.Path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "directory-membership":
				if _, err := original.inputs.directory(filepath.Dir(request.Path)); err != nil {
					t.Fatal(err)
				}
				receipt, err := original.inputs.receipt()
				if err != nil {
					t.Fatal(err)
				}
				body.Source = receipt
				if err := os.WriteFile(filepath.Join(filepath.Dir(request.Path), "added.bork"), []byte("fn added() {}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "negative-appearance":
				path := filepath.Join(filepath.Dir(request.Path), "absent-input")
				if _, err := original.inputs.readFile(path); !os.IsNotExist(err) {
					t.Fatalf("negative read: %v", err)
				}
				receipt, err := original.inputs.receipt()
				if err != nil {
					t.Fatal(err)
				}
				body.Source = receipt
				if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			case "environment":
				t.Setenv("BORK_CACHE_VALIDATION_CHANGED", "yes")
			case "request":
				request.Path += string(filepath.Separator) + "."
			case "namespace":
				namespace = sha256.Sum256([]byte("another compiler"))
			}
			if _, err := body.validate(request, namespace); err == nil {
				t.Fatal("changed input accepted")
			}
		})
	}
}

func TestCacheValidationMetadataInventory(t *testing.T) {
	body, original := cacheArtifactFixture(t)
	usage := &goUsage{}
	names := (goPackages{context: original.context, usage: usage}).Names([]string{"fmt"})
	if names["fmt"] != "fmt" || len(usage.names) != 1 {
		t.Fatalf("names: %v", names)
	}
	receipt, err := usage.names[0].receipt()
	if err != nil {
		t.Fatal(err)
	}
	body.Names = []*goNameReceipt{receipt}
	if _, err := body.validate(body.Request, body.Namespace); err != nil {
		t.Fatal(err)
	}
	// Keep the inventory structurally complete while changing a content digest.
	// This exercises fresh metadata validation rather than schema rejection.
	for path, digest := range receipt.Files {
		digest[0] ^= 1
		receipt.Files[path] = digest
		break
	}
	if !body.valid() {
		t.Fatal("fixture must remain structurally valid")
	}
	if _, err := body.validate(body.Request, body.Namespace); err == nil {
		t.Fatal("changed metadata digest accepted")
	}
}
