package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
)

// This is the comparison baseline for future Session hits: independent parsing
// and semantic graphs must produce identical output for the same input capture.
// External Go metadata and evaluator effects still execute on both paths.
func TestCompilationReplayMatchesClean(t *testing.T) {
	cases := []struct {
		name string
		fail bool
	}{
		{"generics", false}, {"generic_types", false},
		{"field_defaults_imported_fail", true},
		{"embed", false}, {"embed_tests", false},
		{"embed_missing", true}, {"embed_invalid", true},
		{"mocks_generic", false}, {"lazy_bindings", false}, {"lazy_mocks", false},
		{"go_user_deps", false},
		{"comptime_literal", false}, {"comptime_data_shapes", false}, {"comptime_build_inputs", false},
	}
	for _, fixture := range cases {
		name := fixture.name
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("../../testdata/cases", name)
			if name == "embed" || name == "comptime_build_inputs" {
				copy := t.TempDir()
				if err := os.CopyFS(copy, os.DirFS(path)); err != nil {
					t.Fatal(err)
				}
				path = copy
			}
			loaded, module, err := loadCompilationInputs(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			context := captureGoContext()
			var assets *embedSnapshot
			clean, cleanErr := checkLoadedProgramObserved(loaded, module, context, func(info *check.Info, diags *diag.List, sources *sourceSnapshot) *embedSnapshot {
				assets = captureEmbedsSnapshot(info, diags, sources)
				return assets
			}, nil)
			if (cleanErr != nil) != fixture.fail {
				t.Fatalf("clean compilation failure = %v, want failure %v", cleanErr, fixture.fail)
			}
			want := replayOutput(t, clean, cleanErr)
			inventory := loaded.Inputs.dependencies()
			var assetInventory []sourceDependency
			if assets != nil {
				assetInventory = assets.dependencies()
			}
			if name == "embed" || name == "comptime_build_inputs" {
				// Replaying captured bytes must survive deletion of both source
				// and asset inputs; a fresh request must observe that deletion.
				for _, operand := range []string{"main.bork", "assets", "config.txt"} {
					if err := os.RemoveAll(filepath.Join(path, operand)); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := Check(path); err == nil {
					t.Fatal("fresh compilation ignored deleted workspace inputs")
				}
			}
			files, root, diags, err := loadFrom(path, loaded.Inputs)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) > 0 && files[0] == loaded.Files[0] {
				t.Fatal("replay shared parsed syntax")
			}
			replayedModule, err := captureGoModule(files, loaded.Inputs)
			if err != nil {
				t.Fatal(err)
			}
			replay, replayErr := checkLoadedProgramObserved(&loadedSources{files, root, diags, loaded.Inputs}, replayedModule, context, func(info *check.Info, diags *diag.List, _ *sourceSnapshot) *embedSnapshot {
				if assets == nil {
					t.Fatal("replay reached asset capture absent from clean run")
				}
				captured := make([][]check.EmbeddedFile, len(info.Embeds))
				failures := make([]error, len(info.Embeds))
				for i, request := range info.Embeds {
					captured[i], failures[i] = assets.capture(request)
				}
				installCapturedEmbeds(info, diags, captured, failures)
				return assets
			}, nil)
			got := replayOutput(t, replay, replayErr)
			if !reflect.DeepEqual(want, got) {
				t.Fatal("captured-input replay differs from clean compilation")
			}
			if !reflect.DeepEqual(inventory, loaded.Inputs.dependencies()) || assets != nil && !reflect.DeepEqual(assetInventory, assets.dependencies()) {
				t.Fatal("replay discovered inputs absent from clean capture")
			}
		})
	}
}

type compilationOutput struct {
	Go, Tests, Module, Sum, Diagnostics []byte
	Assets                              map[string]check.EmbeddedFile
	Error                               string
}

func replayOutput(t *testing.T, program *compiledProgram, err error) compilationOutput {
	t.Helper()
	var out compilationOutput
	if err != nil {
		out.Error = err.Error()
		var diagnostic *DiagError
		if errors.As(err, &diagnostic) {
			out.Diagnostics, err = json.Marshal(diagnostic.Diags.Sorted())
			if err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	out.Go, err = gen.Package(program.files, program.info)
	if err != nil {
		t.Fatal(err)
	}
	out.Tests, err = gen.Tests(program.files, program.info, false)
	if err != nil {
		t.Fatal(err)
	}
	out.Module = bytes.Clone(program.module.mod)
	out.Sum = bytes.Clone(program.module.sum)
	out.Assets = map[string]check.EmbeddedFile{}
	for _, request := range program.info.Embeds {
		for _, file := range request.Files {
			if _, exists := out.Assets[file.StagePath]; exists {
				t.Fatalf("duplicate staged asset %s", file.StagePath)
			}
			file.Data = bytes.Clone(file.Data)
			out.Assets[file.StagePath] = file
		}
	}
	return out
}
