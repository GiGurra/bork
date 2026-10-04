package driver

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func receiptGoContext(t *testing.T) *goContext {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("running image persistence needs Linux or macOS")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	ctx := captureSessionGoContext(nil)
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	if !supportedGoVersion(ctx.values["GOVERSION"]) {
		t.Skip("unsupported Go inventory version")
	}
	if ctx.validation == nil {
		t.Fatal("missing supported configuration inventory")
	}
	return ctx
}

func roundtripGoReceipt(t *testing.T, ctx *goContext) *goContextReceipt {
	t.Helper()
	receipt, err := ctx.receipt()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded goContextReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return &decoded
}

func TestGoReceiptRoundtrip(t *testing.T) {
	t.Setenv("GOENV", "off")
	ctx := receiptGoContext(t)
	receipt := roundtripGoReceipt(t, ctx)
	restored, err := receipt.restore(resolveGoContext())
	if err != nil {
		t.Fatal(err)
	}
	if restored.namespace != ctx.namespace || !reflect.DeepEqual(restored.env, ctx.env) || !maps.Equal(restored.values, ctx.values) || restored.namesCache != ctx.namesCache {
		t.Fatal("restored Go context differs")
	}
	if restored.validation.toolEvidence == nil || restored.validation.toolEvidence == ctx.validation.toolEvidence {
		t.Fatal("launcher evidence was not established afresh")
	}
	// Owning contexts must not alias receipt maps or the initial capture.
	receipt.Values["GOARCH"] = "different"
	if restored.values["GOARCH"] != ctx.values["GOARCH"] {
		t.Fatal("restored values alias encoded map")
	}
	if !restored.validation.current() {
		t.Fatal("restored inventory not current")
	}
}

func TestGoReceiptRejectsChangedInputs(t *testing.T) {
	for _, change := range []string{"saved-env", "process-env", "module", "cache-mode", "schema", "values"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			config := filepath.Join(root, "go.env")
			if err := os.WriteFile(config, []byte("GOARCH=amd64\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOENV", config)
			ctx := receiptGoContext(t)
			receipt := roundtripGoReceipt(t, ctx)
			switch change {
			case "saved-env":
				before, err := os.Stat(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte("GOARCH=arm64\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(config, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "process-env":
				t.Setenv("BORK_RECEIPT_CHANGED", "yes")
			case "module":
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/receipt\n\ngo 1.26\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cache-mode":
				// Replace the receipt's required cache mode, without touching the real cache.
				receipt.DirectoryModes[receipt.Cache] ^= 0100
			case "schema":
				receipt.Schema++
			case "values":
				receipt.Values["GOFLAGS"] = "-race"
			}
			if _, err := receipt.restore(resolveGoContext()); err == nil {
				t.Fatal("changed receipt accepted")
			}
		})
	}
}

func TestGoNameReceiptRoundtrip(t *testing.T) {
	t.Setenv("GOENV", "off")
	ctx := receiptGoContext(t)
	usage := &goUsage{}
	names := (goPackages{context: ctx, usage: usage}).Names([]string{"fmt"})
	if len(usage.names) != 1 || names["fmt"] != "fmt" {
		t.Fatalf("names: %v", names)
	}
	receipt, err := usage.names[0].receipt()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded goNameReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, err := decoded.restore(ctx.validation)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(restored.names, names) || !restored.inputs.current() {
		t.Fatal("name receipt differs")
	}
	decoded.Names["fmt"] = "changed"
	if restored.names["fmt"] != "fmt" {
		t.Fatal("names alias receipt")
	}
	// Removing any immediate file destroys the complete positive origin inventory.
	for path := range decoded.Files {
		delete(decoded.Files, path)
		break
	}
	if _, err := decoded.restore(ctx.validation); !errors.Is(err, errUnsupportedGoReceipt) {
		t.Fatalf("incomplete file inventory accepted: %v", err)
	}
}

func TestGoReceiptLauncherEqualMtimeEdit(t *testing.T) {
	t.Setenv("GOENV", "off")
	original := receiptGoContext(t)
	bin := t.TempDir()
	launcher := filepath.Join(bin, "go")
	data, err := os.ReadFile(original.tool)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOROOT", original.values["GOROOT"])
	ctx := captureSessionGoContext(nil)
	if ctx.validation == nil {
		t.Fatal("copied launcher not inventoried")
	}
	receipt := roundtripGoReceipt(t, ctx)
	stat, err := os.Stat(launcher)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(launcher, data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(launcher, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.restore(resolveGoContext()); !errors.Is(err, errUnsupportedGoReceipt) {
		t.Fatalf("changed launcher accepted: %v", err)
	}
}

func TestGoNameReceiptDetectsSDKChanges(t *testing.T) {
	t.Setenv("GOENV", "off")
	ctx := receiptGoContext(t)
	for _, change := range []string{"content", "membership"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "src", "fixture")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "ignored_test.go")
			if err := os.WriteFile(file, []byte("package old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			stat, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the receipt inventory against an isolated SDK fixture. Origin
			// proof itself is covered above by the real fmt query.
			fixtureContext := *ctx
			fixtureContext.values = maps.Clone(ctx.values)
			fixtureContext.values["GOROOT"] = root
			fixtureValidation := *ctx.validation
			fixtureValidation.root = root
			fixtureContext.validation = &fixtureValidation
			inputs := captureStandardNameInputs(&fixtureContext, []string{"fixture"})
			if inputs == nil {
				t.Fatal("no fixture inventory")
			}
			input := goNameInput{paths: []string{"fixture"}, names: map[string]string{"fixture": "old"}, standard: true, inputs: inputs}
			receipt, err := input.receipt()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := receipt.restore(&fixtureValidation)
			if err != nil {
				t.Fatal(err)
			}
			if !restored.inputs.current() {
				t.Fatal("unchanged fixture not current")
			}
			if change == "content" {
				if err := os.WriteFile(file, []byte("package new\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, stat.ModTime(), stat.ModTime()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(dir, "ignored.s"), []byte("// change\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if restored.inputs.current() {
				t.Fatal("changed SDK receipt not observed")
			}
		})
	}
}

// Owned cache fixtures select the same controls as receiptGoContext without
// changing process state. Launcher discovery still uses the current PATH.
func ownedReceiptGoContext(t *testing.T) *goContext {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("running image persistence needs Linux or macOS")
	}
	ctx := captureSessionGoContextWithSettings(nil, []string{"GOPACKAGESDRIVER=off", "GOTOOLCHAIN=local"})
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	if !supportedGoVersion(ctx.values["GOVERSION"]) {
		t.Skip("unsupported Go inventory version")
	}
	if ctx.validation == nil {
		t.Fatal("missing supported configuration inventory")
	}
	return ctx
}

func resolveOwnedReceiptGoContext() *goContext {
	return resolveGoContextWithOptions(goContextOptions{settings: []string{"GOPACKAGESDRIVER=off", "GOTOOLCHAIN=local"}, moduleHook: goModuleHook})
}

func validateOwnedCacheFixture(body *cacheArtifactBody, request cacheArtifactRequest, namespace [32]byte) (*cachedCompilation, error) {
	return body.validateWithContext(request, namespace, func(receipt *goContextReceipt) (*goContext, error) {
		return receipt.restore(resolveOwnedReceiptGoContext())
	})
}
