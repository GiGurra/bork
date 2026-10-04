package driver

import (
	"os"
	"path/filepath"

	"testing"

	"github.com/GiGurra/bork/internal/gen"
)

func TestStandardInterpolationArtifactBinding(t *testing.T) {
	savedHook := goModuleHook
	goModuleHook = nil
	t.Cleanup(func() { goModuleHook = savedHook })
	dir := validatorFixture(t, `import "bork/sql"
 fn main(){println(sql.SQL"SELECT ${42}")}`)
	files, info, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	node := info.InterpolationBatches[0].Recipe
	ctx := captureGoContext()
	plan, ok := prepareNativeInterpolation(files, info, node, ctx)
	if !ok {
		t.Fatal("standard validator did not select compiler artifact")
	}
	if len(plan.calls) != 1 {
		t.Fatal("wrong artifact batch")
	}
	// A change to either source bytes or checked implementation declines the artifact.
	for _, file := range files {
		if file.Package == "bork/sql" {
			original := file.Source
			file.Source += "\n"
			if _, ok := prepareNativeInterpolation(files, info, node, ctx); ok {
				t.Fatal("changed source accepted")
			}
			file.Source = original
		}
	}
	helpers := gen.ComptimeFunctions(files, info, node)
	original := helpers[0].Decl.Name
	helpers[0].Decl.Name += "changed"
	if _, ok := prepareNativeInterpolation(files, info, node, ctx); ok {
		t.Fatal("changed definition accepted")
	}
	helpers[0].Decl.Name = original
	ctx.values["GOEXPERIMENT"] = "unknown"
	if _, ok := prepareNativeInterpolation(files, info, node, ctx); ok {
		t.Fatal("unknown native policy accepted")
	}
}

func TestStandardInterpolationAvoidsGoBuild(t *testing.T) {
	savedHook := goModuleHook
	goModuleHook = nil
	t.Cleanup(func() { goModuleHook = savedHook })
	dir := validatorFixture(t, `import "bork/sql"
 fn main(){a=sql.SQL"SELECT ${42}";b=sql.SQL"SELECT ${43}";println(a,b)}`)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContext()
	launcher := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx.tool = launcher
	if _, err := checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil); err != nil {
		t.Fatalf("artifact needed external Go execution: %v", err)
	}
}
