package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

func TestGoContextOwnedSettings(t *testing.T) {
	t.Parallel()
	ambient := os.Environ()
	settings := []string{"GOFLAGS=-tags=owned", "GOPACKAGESDRIVER=off"}
	ctx := resolveGoContextWithOptions(goContextOptions{settings: settings})
	settings[0] = "GOFLAGS=-tags=changed"
	if ctx.processValue("GOFLAGS") != "-tags=owned" || ctx.driver != "off" {
		t.Fatalf("settings were not captured: %s, %s", ctx.processValue("GOFLAGS"), ctx.driver)
	}
	if !slices.Equal(ambient, os.Environ()) {
		t.Fatal("capture changed process environment")
	}
	cmd := ctx.command("env")
	cmd.Env[0] = "changed"
	if !slices.Equal(ctx.env, ctx.processEnv) {
		t.Fatal("command environment aliases its owner")
	}
}

func TestGoContextOwnedModuleStaging(t *testing.T) {
	t.Parallel()
	module := &goModuleInputs{mod: []byte("module example.com/owned\n"), sum: []byte("captured checksums\n")}
	original := bytes.Clone(module.mod)
	options := goContextOptions{moduleHook: func(mod []byte) []byte { return append(mod, []byte("// first\n")...) }}
	first := resolveGoContextWithOptions(options)
	options.moduleHook = func(mod []byte) []byte { mod[0] = '!'; return append(mod, []byte("// second\n")...) }
	second := resolveGoContextWithOptions(options)
	source := []byte("package main\nfunc main(){}\n")
	for name, ctx := range map[string]*goContext{"first": first, "second": second} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Each context stages freshly under its own root and retains its selected
			// hook even after the options that created it were reassigned.
			root := t.TempDir()
			path := filepath.Join(root, "main.bork")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			dir, pinned, release, err := stageGo([]*syntax.File{{Path: path}}, source, module, ctx, "owned-hook-test", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if !pinned {
				t.Fatal("lost captured checksums")
			}
			staged, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			want := ctx.moduleHook(bytes.Clone(original))
			if err != nil || !bytes.Equal(staged, want) {
				t.Fatalf("owned stage: %q, %v", staged, err)
			}
			fallback := t.TempDir()
			if _, err := writeGoStage(fallback, source, module, nil, ctx.moduleHook); err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(filepath.Join(fallback, "go.mod"))
			if err != nil || !bytes.Equal(copied, staged) {
				t.Fatalf("fallback differs: %q, %v", copied, err)
			}
			if !bytes.Equal(module.mod, original) {
				t.Fatal("hook mutated frozen module bytes")
			}
		})
	}
}
