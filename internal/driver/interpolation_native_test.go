package driver

import (
	"os"
	"path/filepath"
	"strings"

	"testing"

	"github.com/GiGurra/bork/internal/gen"
)

func TestStandardInterpolationArtifactBinding(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `import "bork/sql"
 fn main(){println(sql.SQL"SELECT ${42}")}`)
	ctx := captureGoContextWithOptions(goContextOptions{})
	program, err := checkInterpolationFixture(dir, ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, info := program.files, program.info
	node := info.InterpolationBatches[0].Recipe
	plan, ok := prepareNativeInterpolation(files, info, node, ctx)
	if !ok {
		t.Fatal("standard validator did not select compiler artifact")
	}
	usage := &goUsage{}
	if _, err := runNativeInterpolation(plan, ctx, usage); err != nil {
		t.Fatal(err)
	}
	if !usage.evaluator || usage.execution == nil || len(usage.execution.invocations) != 1 || !usage.execution.invocations[0].complete {
		t.Fatal("native dispatch was not accounted once")
	}
	if _, certified := usage.execution.receipts(); certified {
		t.Fatal("native namespace certified a receipt")
	}
	if len(plan.calls) != 1 {
		t.Fatal("wrong artifact batch")
	}

	boundSources := map[string]bool{}
	for _, source := range plan.calls[0].Binding.Descriptor().Sources {
		boundSources[source.Path] = true
	}
	// Relevant source changes decline; unrelated prelude/SQL sources stay eligible.
	for _, file := range files {
		if !file.Prelude && file.Package != "bork/sql" {
			continue
		}
		original := file.Source
		file.Source += "\n"
		_, accepted := prepareNativeInterpolation(files, info, node, ctx)
		if accepted == boundSources[file.Path] {
			t.Fatalf("source %s accepted=%v bound=%v", file.Path, accepted, boundSources[file.Path])
		}
		file.Source = original
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
	t.Parallel()
	dir := validatorFixture(t, `import "bork/sql"
 fn main(){a=sql.SQL"SELECT ${42}";b=sql.SQL"SELECT ${43}";println(a,b)}`)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContextWithOptions(goContextOptions{})
	launcher := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx.tool = launcher
	if _, err := checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil); err != nil {
		t.Fatalf("artifact needed external Go execution: %v", err)
	}
}

func TestStandardInterpolationBudgetFallback(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `import "bork/sql"
 fn main(){println(sql.SQL"SELECT `+strings.Repeat(" ", 700_000)+`${42}")}`)
	if _, err := checkInterpolationFixture(dir, captureGoContextWithOptions(goContextOptions{})); err != nil {
		t.Fatalf("intrinsic budget changed valid SQL behavior: %v", err)
	}
}

func checkInterpolationFixture(path string, ctx *goContext) (*compiledProgram, error) {
	loaded, module, err := loadCompilationInputs(path, nil)
	if err != nil {
		return nil, err
	}
	return checkLoadedProgramObserved(loaded, module, ctx, captureEmbedsSnapshot, nil)
}
