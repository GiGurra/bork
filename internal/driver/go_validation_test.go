package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSessionGoContextContentValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses full configuration capture")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	file := filepath.Join(t.TempDir(), "go.env")
	before := []byte("GOARCH=amd64\n")
	if err := os.WriteFile(file, before, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", file)
	first := captureSessionGoContext(nil)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if !supportedGoVersion(first.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	if first.validation == nil {
		t.Fatal("expected captured configuration inventory")
	}
	if hit := captureSessionGoContext(first); hit != first {
		t.Fatal("unchanged configuration did not reuse capture")
	}
	stat, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, bytes.ReplaceAll(before, []byte("amd64"), []byte("arm64")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(first)
	if changed == first || changed.values["GOARCH"] != "arm64" || changed.namespace != captureGoContext().namespace {
		t.Fatal("equal-size/equal-mtime saved setting edit was not refreshed")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	missing := captureSessionGoContext(changed)
	if missing == changed || missing.namespace != captureGoContext().namespace {
		t.Fatal("removed saved setting was not refreshed")
	}
	// The missing file itself is an input: creating it must invalidate too.
	if err := os.WriteFile(file, before, 0o644); err != nil {
		t.Fatal(err)
	}
	created := captureSessionGoContext(missing)
	if created == missing || created.values["GOARCH"] != "amd64" {
		t.Fatal("new saved setting was not refreshed")
	}
}

func TestSessionGoContextNegativeModuleInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses full configuration capture")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOENV", "off")
	root := t.TempDir()
	t.Chdir(root)
	first := captureSessionGoContext(nil)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if !supportedGoVersion(first.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	if first.validation == nil {
		t.Fatal("expected captured configuration inventory")
	}
	if captureSessionGoContext(first) != first {
		t.Fatal("unchanged context did not reuse capture")
	}
	module := filepath.Join(root, "go.mod")
	if err := os.WriteFile(module, []byte("module example.test/session\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(first)
	if changed == first || changed.values["GOMOD"] != module || changed.namespace != captureGoContext().namespace {
		t.Fatal("new nearer module was not refreshed")
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := captureSessionGoContext(changed)
	if workspace == changed || workspace.validation != nil || workspace.values["GOWORK"] != filepath.Join(root, "go.work") {
		t.Fatal("workspace must refresh and retain full capture")
	}
	if captureSessionGoContext(workspace) == workspace {
		t.Fatal("workspace unexpectedly reused configuration capture")
	}
}

func TestSessionGoContextSDKSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires SDK symlinks")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "")
	original := captureGoContext()
	if original.err != nil {
		t.Fatal(original.err)
	}
	if !supportedGoVersion(original.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	sdk := t.TempDir()
	entries, err := os.ReadDir(original.values["GOROOT"])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "go.env" {
			continue
		}
		if err := os.Symlink(filepath.Join(original.values["GOROOT"], entry.Name()), filepath.Join(sdk, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(sdk, "go.env")
	data := []byte("GOTOOLCHAIN=auto\n")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOROOT", sdk)
	first := captureSessionGoContext(nil)
	if first.validation == nil || captureSessionGoContext(first) != first {
		t.Fatal("SDK configuration capture was not reusable")
	}
	stat, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, bytes.ReplaceAll(data, []byte("auto"), []byte("path")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(first)
	if changed == first || changed.values["GOTOOLCHAIN"] != "path" || changed.namespace != captureGoContext().namespace {
		t.Fatal("SDK saved setting edit was not refreshed")
	}
}

func TestSessionGoContextUnknownLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell launcher")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	first := captureGoContext()
	if first.err != nil {
		t.Fatal(first.err)
	}
	dir := t.TempDir()
	// Launcher dependencies cannot be inferred merely from its reported version.
	script := "#!/bin/sh\nexec '" + first.tool + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	wrapped := captureSessionGoContext(nil)
	if wrapped.err != nil {
		t.Fatal(wrapped.err)
	}
	if wrapped.validation != nil || captureSessionGoContext(wrapped) == wrapped {
		t.Fatal("unknown launcher must reload configuration")
	}
	if captureStandardNameInputs(wrapped, []string{"fmt"}) != nil {
		t.Fatal("unknown launcher must reload name metadata")
	}
}

func TestSessionGoContextLauncherRelocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires SDK symlinks")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOROOT", "")
	original := captureGoContext()
	if original.err != nil {
		t.Fatal(original.err)
	}
	if !supportedGoVersion(original.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	data, err := os.ReadFile(original.tool)
	if err != nil {
		t.Fatal(err)
	}
	sdk := func() string {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bin", "go"), data, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"src", "pkg", "lib", "go.env"} {
			if err := os.Symlink(filepath.Join(original.values["GOROOT"], name), filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	firstSDK, secondSDK := sdk(), sdk()
	dir := t.TempDir()
	launcher := filepath.Join(dir, "go")
	if err := os.Symlink(filepath.Join(firstSDK, "bin", "go"), launcher); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	first := captureSessionGoContext(nil)
	if first.validation == nil || first.values["GOROOT"] != firstSDK {
		t.Fatalf("first SDK: %v, %v", first.err, first.values["GOROOT"])
	}
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secondSDK, "bin", "go"), launcher); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(first)
	if changed == first || changed.values["GOROOT"] != secondSDK || changed.namespace != captureGoContext().namespace {
		t.Fatal("identical launcher bytes at a relocated SDK retained old GOROOT")
	}
}

func TestSessionGoContextTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses full configuration capture")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTMPDIR", dir)
	first := captureSessionGoContext(nil)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if !supportedGoVersion(first.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	if first.validation == nil {
		t.Fatal("expected reusable configuration")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	modeChange := captureSessionGoContext(first)
	if modeChange == first {
		t.Fatal("temporary-directory permission edit was not observed")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(modeChange)
	if changed == modeChange || changed.err == nil || captureGoContext().err == nil {
		t.Fatal("unavailable temporary directory retained successful Go context")
	}
}

func TestSessionGoContextTelemetrySettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses XDG_CONFIG_HOME")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	for _, override := range []bool{false, true} {
		name := "default"
		if override {
			name = "override"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			telemetryDir := filepath.Join(dir, "go", "telemetry")
			if override {
				telemetryDir = t.TempDir()
				t.Setenv("TEST_TELEMETRY_DIR", telemetryDir)
			}
			mode := filepath.Join(telemetryDir, "mode")
			if err := os.MkdirAll(filepath.Dir(mode), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(mode, []byte("local\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			first := captureSessionGoContext(nil)
			if first.err != nil {
				t.Fatal(first.err)
			}
			if !supportedGoVersion(first.values["GOVERSION"]) {
				t.Skip("unrecognized Go inventory version")
			}
			if first.validation == nil {
				t.Fatal("expected reusable configuration")
			}
			if err := os.WriteFile(mode, []byte("off\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			changed := captureSessionGoContext(first)
			if changed == first || changed.values["GOTELEMETRY"] != "off" || changed.namespace != captureGoContext().namespace {
				t.Fatal("telemetry mode retained stale effective configuration")
			}
		})
	}

}

func TestSessionGoContextFIPSBypass(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOFIPS140", "latest")
	first := captureSessionGoContext(nil)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.validation != nil || captureSessionGoContext(first) == first {
		t.Fatal("FIPS modes need full configuration capture")
	}
	if captureStandardNameInputs(first, []string{"fmt"}) != nil {
		t.Fatal("FIPS metadata inputs are not inventoried")
	}
}

func TestSessionGoContextInvalidCgoSetting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell compiler")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("CGO_ENABLED", "bogus")
	t.Setenv("CC", "")
	original := captureGoContext()
	if original.err != nil {
		t.Fatal(original.err)
	}
	if !supportedGoVersion(original.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	dir := t.TempDir()
	if err := os.Symlink(original.tool, filepath.Join(dir, "go")); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(dir, original.values["CC"])
	if filepath.Dir(compiler) != dir {
		t.Skip("SDK has absolute/custom default compiler")
	}
	if err := os.WriteFile(compiler, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	first := captureSessionGoContext(nil)
	if first.err != nil || first.validation == nil {
		t.Fatalf("first context: %v", first.err)
	}
	if len(first.validation.compilers) == 0 {
		t.Fatal("invalid CGO_ENABLED omitted default compiler lookup")
	}
	if err := os.Remove(compiler); err != nil {
		t.Fatal(err)
	}
	changed := captureSessionGoContext(first)
	if changed == first || changed.namespace != captureGoContext().namespace {
		t.Fatal("compiler removal under invalid CGO_ENABLED retained stale configuration")
	}
}

func TestStandardNameInputsKeepMetadataDriverEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps full name reloads")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	ctx := captureSessionGoContext(nil)
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	if !supportedGoVersion(ctx.values["GOVERSION"]) {
		t.Skip("unrecognized Go inventory version")
	}
	data, err := os.ReadFile(ctx.self)
	if err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(t.TempDir(), "metadata-driver")
	if err := os.WriteFile(copy, data, 0o755); err != nil {
		t.Fatal(err)
	}
	// This executable is not the running inode, so it exercises the byte-hash
	// fallback as well as the name inventory's retained configuration evidence.
	ctx.self = copy
	ctx.validation = captureGoContextValidation(ctx)
	if ctx.validation == nil || !ctx.validation.accepts(ctx) {
		t.Fatal("expected captured metadata driver")
	}
	usage := &goUsage{}
	names := (goPackages{context: ctx, usage: usage}).Names([]string{"fmt"})
	if names["fmt"] != "fmt" || len(usage.names) != 1 || usage.names[0].inputs == nil {
		t.Fatal("expected positive standard-name inventory")
	}
	input := usage.names[0].inputs
	if !input.current() {
		t.Fatal("unchanged metadata driver was not valid")
	}
	namespace := ctx.namespace
	if err := os.Chmod(copy, 0o644); err != nil {
		t.Fatal(err)
	}
	if input.current() {
		t.Fatal("unchanged SDK files hid an unavailable metadata driver")
	}
	if ctx.namespace != namespace {
		t.Fatal("test must retain equal effective Go namespace")
	}
	if fresh := (goPackages{context: ctx, usage: &goUsage{}}).Names([]string{"fmt"}); len(fresh) != 0 {
		t.Fatal("fresh metadata unexpectedly succeeded without executable driver")
	}
}

func TestMetadataContentHashRejectsSpecialFiles(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix special file")
	}
	if _, err := goFileDigest(os.DevNull, make([]byte, 32*1024)); err == nil {
		t.Fatal("special files must use full name reloads")
	}
}
