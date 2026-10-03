package driver

import (
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/gen"
)

func TestGoContextFreezesSavedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goenv")
	if err := os.WriteFile(path, []byte("GO111MODULE=on\nGOAMD64=v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", path)
	t.Setenv("GO111MODULE", "")
	t.Setenv("GOAMD64", "")
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	if err := os.WriteFile(path, []byte("GO111MODULE=off\nGOAMD64=v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := ctx.command("env", "GO111MODULE", "GOAMD64").Output()
	if err != nil || string(output) != "on\nv1\n" {
		t.Fatalf("saved settings changed: %q, %v", output, err)
	}
	fresh := captureGoContext()
	if fresh.err != nil || fresh.values["GO111MODULE"] != "off" || fresh.values["GOAMD64"] != "v3" || fresh.namesCache {
		t.Fatalf("fresh settings: mode=%q, amd64=%q, %v", fresh.values["GO111MODULE"], fresh.values["GOAMD64"], fresh.err)
	}
	if ctx.namespace == fresh.namespace {
		t.Fatal("effective config namespace did not change")
	}
}

func TestGoContextRetainedForBuildAndPredicates(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BORK_CONTEXT_TEST", "captured")
	for name, data := range map[string]string{ModFile: "module example.com/context\nunsafe \"example.com/context\"\n", "main.bork": `fn EnvOK() uses io: Bool unsafe go {
  import "os"
  return os.Getenv("BORK_CONTEXT_TEST") == "captured"
}
fn main() {}
`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	program, err := checkProgramObserved(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	originalArch := program.context.values["GOARCH"]
	t.Setenv("BORK_CONTEXT_TEST", "changed")
	alternate := "386"
	if originalArch == alternate {
		alternate = "amd64"
	}
	t.Setenv("GOARCH", alternate)
	query := check.Query{Pred: program.info.Funcs["EnvOK"]}
	results, err := evaluatorWithContext(program.files, program.info, program.module, program.context)([]check.Query{query})
	if err != nil || len(results) != 1 || !results[0] {
		t.Fatalf("predicate environment changed: %v, %v", results, err)
	}
	source, err := gen.Package(program.files, program.info)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := buildGoWithContext(program.files, source, exe, program.module, program.context); err != nil {
		t.Fatal(err)
	}
	metadata, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	arch := ""
	for _, setting := range metadata.Settings {
		if setting.Key == "GOARCH" {
			arch = setting.Value
		}
	}
	if arch != originalArch {
		t.Fatalf("build GOARCH = %s, captured %s", arch, originalArch)
	}
}

func TestGoContextMetadataEnvironmentIsolation(t *testing.T) {
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	env := ctx.metadataEnv()
	env[0] = "corrupt"
	if ctx.env[0] == "corrupt" {
		t.Fatal("metadata caller mutated context")
	}
	for _, assignment := range []string{"GOENV=off", "GOWORK=off", "GOFLAGS=-mod=readonly"} {
		found := false
		for _, value := range ctx.metadataEnv() {
			if value == assignment {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing metadata override %s", assignment)
		}
	}
}

func TestGoContextMissingGoRemainsDeferred(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	ctx := captureGoContext()
	if ctx.err == nil || ctx.namesCache {
		t.Fatal("missing tool accepted for namespace reuse")
	}
	if _, _, err := Check(root); err != nil {
		t.Fatalf("pure check requires Go: %v", err)
	}
	if err := Build(root, filepath.Join(t.TempDir(), "program")); err == nil || !strings.Contains(err.Error(), "Go installed") {
		t.Fatalf("build without Go: %v", err)
	}
}

func TestGoContextMetadataUsesCapturedLauncher(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	t.Setenv("PATH", t.TempDir())
	gp := goPackages{context: ctx}
	if got := gp.Names([]string{"fmt"})["fmt"]; got != "fmt" {
		t.Fatalf("metadata ignored captured launcher: %q", got)
	}
	pkgs, errs := gp.Load([]string{"strings"})
	if pkgs["strings"] == nil {
		t.Fatalf("types ignored captured launcher: %v", errs)
	}
}

func TestGoContextFreezesImplicitDriverChoice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell driver")
	}
	dir := t.TempDir()
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := captureGoContext()
	if ctx.err != nil || !ctx.namesCache {
		t.Fatalf("builtin context capture: %v", ctx.err)
	}
	driver := filepath.Join(dir, "gopackagesdriver")
	response := fmt.Sprintf(`{"Compiler":"gc","Arch":%q,"Roots":["fmt"],"Packages":[{"ID":"fmt","PkgPath":"fmt","Name":"poison"}]}`, runtime.GOARCH)
	if err := os.WriteFile(driver, []byte("#!/bin/sh\nprintf '%s\\n' '"+response+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := (goPackages{context: ctx}).Names([]string{"fmt"})["fmt"]; got != "fmt" {
		t.Fatalf("late implicit driver changed metadata: %q", got)
	}
	if value, ok := standardGoNames.Load(standardGoNameKey{ctx.namespace, "fmt"}); !ok || value != "fmt" {
		t.Fatalf("late driver poisoned namespace: %v", value)
	}
	fresh := captureGoContext()
	if fresh.err != nil || fresh.namesCache || fresh.driver != driver {
		t.Fatalf("fresh implicit driver capture: %v", fresh.err)
	}
	if got := (goPackages{context: fresh}).Names([]string{"fmt"})["fmt"]; got != "poison" {
		t.Fatalf("fresh driver ignored: %q", got)
	}
}

func TestGoContextExternalNamesRemainFresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/contextnames\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := goModuleHook
	t.Cleanup(func() { goModuleHook = saved })
	goModuleHook = func(mod []byte) []byte {
		return fmt.Appendf(mod, "\nrequire example.com/contextnames v0.0.0\nreplace example.com/contextnames => %s\n", root)
	}
	t.Setenv("GO111MODULE", "on")
	t.Setenv("GOPACKAGESDRIVER", "off")
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(root, "name.go"), []byte("package "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := (goPackages{context: ctx}).Names([]string{"example.com/contextnames"})["example.com/contextnames"]; got != name {
			t.Fatalf("external name = %q, want %q", got, name)
		}
	}
	if _, ok := standardGoNames.Load(standardGoNameKey{ctx.namespace, "example.com/contextnames"}); ok {
		t.Fatal("external metadata entered standard name cache")
	}
}

func TestGoContextLauncherShimPreservesAuxiliaryPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell and symlink PATH fixture")
	}
	original, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	first := t.TempDir()
	toolDir := t.TempDir()
	if err := os.Symlink(original, filepath.Join(toolDir, "go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for dir, value := range map[string]string{first: "original", toolDir: "shadow"} {
		if err := os.WriteFile(filepath.Join(dir, "bork-context-aux"), []byte("#!/bin/sh\nprintf '%s' '"+value+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("PATH", first+string(os.PathListSeparator)+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	env, err := ctx.driverEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "bork-context-aux")
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil || string(output) != "original" {
		t.Fatalf("auxiliary PATH changed: %q, %v", output, err)
	}
}

func TestGoContextExternalDriverEnvironmentAndFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell driver")
	}
	driver := filepath.Join(t.TempDir(), "driver")
	if err := os.WriteFile(driver, []byte("#!/bin/sh\nif [ \"$GOPACKAGESDRIVER\" != \"$0\" ]; then exit 23; fi\nprintf '%s\\n' '{\"NotHandled\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPACKAGESDRIVER", driver)
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	t.Setenv("PATH", t.TempDir())
	if got := (goPackages{context: ctx}).Names([]string{"fmt"})["fmt"]; got != "fmt" {
		t.Fatalf("driver env or fallback launcher changed: %q", got)
	}
	pkgs, errs := (goPackages{context: ctx}).Load([]string{"strings"})
	if pkgs["strings"] == nil {
		t.Fatalf("driver type fallback: %v", errs)
	}
}

func TestGoContextCapturesGoDespiteMissingDriver(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", filepath.Join(t.TempDir(), "missing"))
	ctx := captureGoContext()
	if ctx.driverErr == nil || ctx.err != nil || ctx.tool == "" || len(ctx.values) == 0 {
		t.Fatalf("independent Go snapshot missing: Go=%v, driver=%v", ctx.err, ctx.driverErr)
	}
	original := ctx.values["GOARCH"]
	t.Setenv("GOARCH", "invalid-after-capture")
	output, err := ctx.command("env", "GOARCH").Output()
	if err != nil || strings.TrimSpace(string(output)) != original {
		t.Fatalf("bad driver bypassed Go snapshot: %q, %v", output, err)
	}
	pkgs, errs := (goPackages{context: ctx}).Load([]string{"strings"})
	if pkgs["strings"] != nil || errs["strings"] == nil {
		t.Fatal("missing driver metadata succeeded")
	}
}
