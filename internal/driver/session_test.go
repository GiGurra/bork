package driver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestSessionEmitHitAndSourceEdit(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`fn main() { println("old") }`)
	session := NewSession()
	first, err := session.Emit(root)
	if err != nil {
		t.Fatal(err)
	}
	hit, err := session.Emit(root)
	if err != nil || !bytes.Equal(first, hit) || session.Stats().Hits != 1 {
		t.Fatalf("hit: %v, %+v", err, session.Stats())
	}
	hit[0] = '!'
	owned, err := session.Emit(root)
	if err != nil || !bytes.Equal(first, owned) {
		t.Fatal("caller modified retained output")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(`fn main() { println("new") }`)
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := session.Emit(root)
	if err != nil || bytes.Equal(first, changed) {
		t.Fatalf("source edit was missed: %v", err)
	}
	clean, err := Emit(root)
	if err != nil || !bytes.Equal(changed, clean) {
		t.Fatalf("changed output differs from clean: %v", err)
	}
	if stats := session.Stats(); stats.Hits != 2 || stats.Misses != 2 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestSessionBypassesEvaluationAndTypes(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	for _, fixture := range []struct{ name, reason string }{
		{"embed", "compile-time evaluator"},
		{"go_user_deps", "Go type metadata"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			session := NewSession()
			path := filepath.Join("../../testdata/cases", fixture.name)
			for range 2 {
				if _, err := session.Emit(path); err != nil {
					t.Fatal(err)
				}
			}
			if stats := session.Stats(); stats.Hits != 0 || stats.Bypasses != 2 || stats.Reason != fixture.reason {
				t.Fatalf("stats: %+v", stats)
			}
		})
	}
}

func TestSessionCheckWarningsOwned(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() { lazy x = 1; println(x) }"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	want, err := session.Check(root)
	if err != nil || len(want) == 0 {
		t.Fatalf("warnings: %v, %v", want, err)
	}
	got, err := session.Check(root)
	if err != nil || !reflect.DeepEqual(want, got) || session.Stats().Hits != 1 {
		t.Fatalf("hit warnings: %v, %v, %+v", got, err, session.Stats())
	}
	got[0].Msg = "mutated"
	got[0].Fixes[0].Edits[0].Replacement = "mutated"
	got, err = session.Check(root)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatal("caller modified retained warning")
	}
}

func TestSessionConcurrentRequests(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	want, err := session.Emit(root)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 4)
	for range 4 {
		go func() {
			got, err := session.Emit(root)
			if err == nil && !bytes.Equal(want, got) {
				err = fmt.Errorf("concurrent output differs")
			}
			if len(got) > 0 {
				got[0] = '!'
			}
			results <- err
		}()
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if stats := session.Stats(); stats.Hits != 4 || stats.Misses != 1 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestSessionWindowsDriveContextBypass(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive-relative resolution")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	session := NewSession()
	path := filepath.VolumeName(root) + "main.bork"
	for range 2 {
		if _, err := session.Emit(path); err != nil {
			t.Fatal(err)
		}
	}
	if stats := session.Stats(); stats.Hits != 0 || stats.Bypasses != 2 || stats.Reason != "uncaptured Windows drive context" {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestSessionMatchesClean(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	for _, fixture := range []struct{ name, path, source string }{
		{name: "hello", path: "../../examples/hello"},
		{name: "generics", source: "fn identity[T](x:T):T{x}\nfn main(){println(identity(1))}"},
		{name: "warnings", source: "fn main(){lazy x=1;println(x)}"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := fixture.path
			if path == "" {
				path = t.TempDir()
				if err := os.WriteFile(filepath.Join(path, "main.bork"), []byte(fixture.source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			session := NewSession()
			first, err := session.Emit(path)
			if err != nil {
				t.Fatal(err)
			}
			hit, err := session.Emit(path)
			if err != nil || session.Stats().Hits != 1 || !bytes.Equal(first, hit) {
				t.Fatalf("hit: %v, %+v", err, session.Stats())
			}
			artifact := session.last
			files, root, diags, err := loadFrom(path, artifact.inputs)
			if err != nil {
				t.Fatal(err)
			}
			module, err := captureGoModule(files, artifact.inputs)
			if err != nil {
				t.Fatal(err)
			}
			clean, err := checkLoadedProgramTracked(&loadedSources{files, root, diags, artifact.inputs}, module, artifact.context, func(info *check.Info, diags *diag.List, _ *sourceSnapshot) *embedSnapshot {
				captured := make([][]check.EmbeddedFile, len(info.Embeds))
				failures := make([]error, len(info.Embeds))
				for i, request := range info.Embeds {
					captured[i], failures[i] = artifact.assets.capture(request)
				}
				installCapturedEmbeds(info, diags, captured, failures)
				return artifact.assets
			}, &goUsage{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			output := replayOutput(t, clean, nil)
			cleanWarnings := check.DebugWarnings(clean.info)
			cleanWarnings.Append(check.LazyWarnings(clean.info))
			cleanWarnings.Append(check.MigrationWarnings(clean.info))
			if !reflect.DeepEqual(artifact.warnings, cleanWarnings.Sorted()) {
				t.Fatal("cached warnings differ from clean captured-input compilation")
			}
			if !bytes.Equal(hit, output.Go) || !bytes.Equal(artifact.module.mod, output.Module) || !bytes.Equal(artifact.module.sum, output.Sum) {
				t.Fatal("Session hit differs from clean captured-input compilation")
			}
		})
	}
}

func TestSessionRefreshesStandardPackageNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires SDK directory symlinks")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	context := captureGoContext()
	if context.err != nil {
		t.Fatal(context.err)
	}
	original := context.values["GOROOT"]
	sdk := t.TempDir()
	entries, err := os.ReadDir(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "src" {
			continue
		}
		if err := os.Symlink(filepath.Join(original, entry.Name()), filepath.Join(sdk, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(sdk, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(original, "src"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		target := filepath.Join(sdk, "src", entry.Name())
		if entry.Name() == "strings" {
			if err := os.CopyFS(target, os.DirFS(filepath.Join(original, "src", "strings"))); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink(filepath.Join(original, "src", entry.Name()), target); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOROOT", sdk)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() { println(1) }"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Emit(root); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Emit(root); err != nil || session.Stats().Hits != 1 {
		t.Fatalf("hit: %v, %+v", err, session.Stats())
	}
	if session.last.context.validation != nil {
		for _, input := range session.last.names {
			if input.inputs == nil {
				t.Fatal("expected content validation for proven SDK name inputs")
			}
		}
	}
	files, err := filepath.Glob(filepath.Join(sdk, "src", "strings", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.ReplaceAll(data, []byte("package strings"), []byte("package renamed"))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	got, gotErr := session.Emit(root)
	want, wantErr := Emit(root)
	if !bytes.Equal(got, want) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatal("SDK name edit differs from clean output")
	}
	if stats := session.Stats(); stats.Hits != 1 || stats.Misses != 2 || stats.Reason != "Go package names changed" {
		t.Fatalf("SDK edit: %+v", stats)
	}
}

func TestSessionAssetChanges(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.Mkdir(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(assets, "value")
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("import assets \"bork/embed\"\nfn main(){println(assets.ReadString(\"assets/value\"));println(assets.Directory(\"assets\").Paths())}"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Emit(root); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Emit(root); err != nil || session.Stats().Hits != 1 {
		t.Fatalf("hit: %v, %+v", err, session.Stats())
	}
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Emit(root); err != nil || session.Stats().Misses != 2 || session.Stats().Reason != "asset inputs changed" {
		t.Fatalf("asset edit: %v, %+v", err, session.Stats())
	}
	if err := os.WriteFile(filepath.Join(assets, "added"), []byte("another"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := session.Emit(root)
	if err != nil || session.Stats().Misses != 3 {
		t.Fatalf("membership edit: %v, %+v", err, session.Stats())
	}
	want, err := Emit(root)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("asset result differs from clean: %v", err)
	}
}

func TestSessionAddedInvalidDefaultAndRepair(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Check(root); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(root); err != nil || session.Stats().Hits != 1 {
		t.Fatalf("hit: %v, %+v", err, session.Stats())
	}
	added := filepath.Join(root, "added.bork")
	if err := os.WriteFile(added, []byte("pred positive(x:Int){x>0}\ntype Unused={value:Int where positive=0}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := session.Check(root)
	if err == nil {
		t.Fatal("cached success hid an unused invalid default")
	}
	_, _, cleanErr := Check(root)
	if fmt.Sprint(err) != fmt.Sprint(cleanErr) {
		t.Fatal("failure diagnostics differ from clean")
	}
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(root); err != nil || session.Stats().Misses != 3 {
		t.Fatalf("repair: %v, %+v", err, session.Stats())
	}
}

func TestSessionConfigurationAndModeChanges(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	if _, err := session.Check(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORK_SESSION_TEST", "changed")
	if _, err := session.Check(root); err != nil || session.Stats().Misses != 2 || session.Stats().Reason != "Go configuration changed" {
		t.Fatalf("configuration: %v, %+v", err, session.Stats())
	}
	if _, err := session.Emit(root); err != nil || session.Stats().Misses != 3 || session.Stats().Reason != "request path or mode changed" {
		t.Fatalf("mode: %v, %+v", err, session.Stats())
	}
	other := NewSession()
	if _, err := other.Check(root); err != nil || other.Stats().Misses != 1 || other.Stats().Hits != 0 {
		t.Fatalf("separate session: %v, %+v", err, other.Stats())
	}
}
