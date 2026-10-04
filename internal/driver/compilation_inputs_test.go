package driver

import (
	"bytes"
	"fmt"
	"go/constant"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GiGurra/bork/internal/check"
)

func TestCompilationFreezesGoManifests(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{ModFile, "main.bork", "go-deps.mod", "go-deps.sum"} {
		data, err := os.ReadFile(filepath.Join("../../testdata/cases/go_user_deps", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "main.bork" {
			data = append(data, []byte("\nfn Positive(x: Int): Bool { x > 0 }\n")...)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	program, source, err := emitProgramObserved(root, func(phase string) {
		if phase != "check" {
			return
		}
		// This edit happens after capture, before Go type/name metadata loading.
		if err := os.WriteFile(filepath.Join(root, "go-deps.mod"), []byte("replace invalid => ../invalid\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "go-deps.sum")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module changed.example/root\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatalf("metadata/check with captured manifests: %v", err)
	}
	if program.inputs.current() {
		t.Fatal("manifest edit missing from inventory")
	}
	gp := goPackages{files: program.files, module: program.module}
	if name := gp.Names([]string{"github.com/google/uuid"})["github.com/google/uuid"]; name != "uuid" {
		t.Fatalf("captured package name = %q", name)
	}
	pkgs, errs := gp.Load([]string{"github.com/google/uuid"})
	if pkgs["github.com/google/uuid"] == nil {
		t.Fatalf("captured types: %v", errs)
	}
	query := check.Query{Pred: program.info.Funcs["Positive"], Args: []constant.Value{constant.MakeInt64(1)}}
	if query.Pred == nil {
		t.Fatal("missing predicate")
	}
	query.Params = query.Pred.Params
	results, err := evaluatorWithModule(program.files, program.info, program.module)([]check.Query{query})
	if err != nil || len(results) != 1 || !results[0] {
		t.Fatalf("captured evaluator: %v, %v", results, err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := buildGoWithModule(program.files, source, exe, program.module, program.info.Embeds...); err != nil {
		t.Fatalf("captured final build: %v", err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || !strings.HasPrefix(string(output), "Ok\nGoError") {
		t.Fatalf("built program: %s, %v", output, err)
	}
	if _, _, err := Check(root); err == nil {
		t.Fatal("fresh compilation ignored invalid current manifests")
	}
}

func TestCompilationInventoriesAbsentManifest(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{ModFile: "module example.com/absent\n", "main.bork": "fn main() {}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	program, err := checkProgramObserved(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, input := range program.inputs.dependencies() {
		if input.Kind == "file" && input.Path == filepath.Join(root, "go-deps.mod") {
			found = true
		}
	}
	if !found {
		t.Fatal("absent manifest lookup missing from inventory")
	}
	if err := os.WriteFile(filepath.Join(root, "go-deps.mod"), []byte("replace invalid => ../invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if program.inputs.current() {
		t.Fatal("new manifest did not invalidate inventory")
	}
}

func TestFrozenGoModuleStaging(t *testing.T) {
	t.Parallel()
	inputs, err := captureGoModule(nil, diskSources{})
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(inputs.mod)
	hook := func(mod []byte) []byte { mod[0] = '!'; return mod }
	var tasks sync.WaitGroup
	for range 8 {
		dir := t.TempDir()
		tasks.Go(func() {
			if _, err := inputs.write(dir, hook); err != nil {
				t.Error(err)
				return
			}
			mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err != nil || mod[0] != '!' {
				t.Errorf("staging: %q, %v", mod, err)
			}
		})
	}
	tasks.Wait()
	if !bytes.Equal(original, inputs.mod) {
		t.Fatal("staging hook mutated frozen bytes")
	}
}

func TestCompilationReportsSyntaxBeforeManifest(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{ModFile: "module example.com/errors\n", "main.bork": "invalid @\n", "go-deps.mod": "replace invalid => ../invalid\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := Check(root); err == nil || strings.Contains(err.Error(), "go-deps") {
		t.Fatalf("syntax ordering: %v", err)
	}
}

type manifestEditingSources struct {
	diskSources
	manifest, target string
	edit             bool
}

func (s *manifestEditingSources) readFile(path string) ([]byte, error) {
	data, err := s.diskSources.readFile(path)
	if path == s.manifest && s.edit && err == nil {
		s.edit = false
		current, err := os.ReadFile(s.target)
		if err != nil {
			return nil, err
		}
		next := "// changed during manifest capture\n"
		if strings.Contains(string(current), "changed during") {
			next = "// original\n"
		}
		if err := os.WriteFile(s.target, []byte(next), 0o644); err != nil {
			return nil, err
		}
	}
	return data, err
}

func TestCompilationRetriesCombinedCapture(t *testing.T) {
	for _, sourceEdit := range []bool{false, true} {
		for _, continuous := range []bool{false, true} {
			name := fmt.Sprintf("source=%t/continuous=%t", sourceEdit, continuous)
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				for name, data := range map[string]string{ModFile: "module example.com/retry\n", "main.bork": "// original\n", "go-deps.mod": "// original\n", "go-deps.sum": ""} {
					if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				manifest := filepath.Join(root, "go-deps.mod")
				target := manifest
				if sourceEdit {
					target = filepath.Join(root, "main.bork")
				}
				captures := 0
				loaded, _, err := loadCompilationInputsFrom(root, nil, func() *sourceSnapshot {
					captures++
					inputs := newSourceSnapshot()
					inputs.disk = &manifestEditingSources{manifest: manifest, target: target, edit: continuous || captures == 1}
					return inputs
				})
				if captures != 2 {
					t.Fatalf("captures = %d", captures)
				}
				if continuous {
					if err == nil || !strings.Contains(err.Error(), "inputs changed") {
						t.Fatalf("continuous edits: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := loaded.Inputs.readFile(target)
				if err != nil || !strings.Contains(string(data), "changed during") {
					t.Fatalf("retry captured stale bytes: %q, %v", data, err)
				}
				if sourceEdit && loaded.Files[len(loaded.Files)-1].Source != string(data) {
					t.Fatal("retry parsed stale source")
				}
			})
		}
	}
}
