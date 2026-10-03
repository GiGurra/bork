package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComptimeBuildInputs(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		ModFile:      "module example.com/buildinputs\n",
		"config.txt": "hello\n",
		"raw.bin":    string([]byte{0, 255, 7}),
		"lib/lib.bork": `import "bork/build"
fn Text() uses build: String{build.ReadString("config.txt")}`,
		"main.bork": `import "bork/build"
import "example.com/buildinputs/lib"
fn main(){a=comptime{lib.Text()};b=comptime{build.ReadBytes("raw.bin")};println(a);println(b)}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	program, err := checkProgramObserved(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if program.inputs.rooted == nil || len(program.inputs.rooted.dependencies()) != 2 {
		t.Fatal("missing rooted read inventory")
	}
	if !program.inputs.current() {
		t.Fatal("fresh snapshot is stale")
	}
	session := NewSession()
	first, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if program.inputs.current() {
		t.Fatal("equal-size edit did not invalidate snapshot")
	}
	second, err := session.Emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("frozen output did not change")
	}
	if stats := session.Stats(); stats.Hits != 0 || stats.Bypasses != 2 {
		t.Fatalf("unexpected reuse: %+v", stats)
	}
}

func TestComptimeBuildInputRejections(t *testing.T) {
	for _, tc := range []struct{ name, source, path, data, want string }{
		{"runtime", `fn main(){println(build.ReadString("x"))}`, "x", "hi", "build effects may run only inside comptime"},
		{"runtime explicit", `fn main() uses build{_=build.ReadString("x")}`, "x", "hi", "build effects may run only inside comptime"},
		{"parameter", `fn read(p:String) uses build:String{build.ReadString(p)}
fn main(){println(comptime{read("x")})}`, "x", "hi", "constant String path"},
		{"function value", `fn main(){_=comptime{f=build.ReadString;f("x")}}`, "x", "hi", "cannot be used as a function value"},
		{"computed path", `fn main(){p=comptime{"x"};println(comptime{build.ReadString(p)})}`, "x", "hi", "constant String path"},
		{"missing guarded", `fn main(){println(comptime{if(false){build.ReadString("missing")}else{"ok"}})}`, "x", "hi", "cannot read build input"},
		{"parent escape", `fn main(){println(comptime{build.ReadString("../x")})}`, "x", "hi", "relative to the module root"},
		{"binary string", `fn main(){println(comptime{build.ReadString("x")})}`, "x", string([]byte{255}), "not valid UTF-8"},
		{"file limit", `fn main(){println(comptime{build.ReadString("x")})}`, "x", strings.Repeat("a", buildFileLimit+1), "exceeds 16 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{ModFile: "module example.com/buildinputs\n", "main.bork": "import \"bork/build\"\n" + tc.source, tc.path: tc.data} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := Check(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildReadInventory(t *testing.T) {
	dir := t.TempDir()
	key := buildReadKey{dir, "ReadBytes", "file"}
	missing := &buildSnapshot{reads: map[buildReadKey]buildRead{key: readBuildInput(key)}}
	if !missing.current() {
		t.Fatal("stable negative read changed")
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if missing.current() {
		t.Fatal("new file failed to invalidate negative read")
	}
	value := readBuildInput(key)
	if value.value.err != nil {
		t.Fatal(value.value.err)
	}
	inputs := &buildSnapshot{reads: map[buildReadKey]buildRead{key: value}}
	stat, err := os.Stat(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "file"), stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("equal-mtime edit failed to invalidate content")
	}
	if string(value.value.files[0].Data) != "one" {
		t.Fatal("frozen bytes changed")
	}
	limited := readBuildInputLimit(key, 1)
	if limited.value.err == nil || !strings.Contains(limited.value.err.Error(), "64 MiB") || len(limited.value.contents) != 0 {
		t.Fatalf("aggregate limit retained payload: %+v", limited.value)
	}
	if err := os.Remove(filepath.Join(dir, "file")); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("removal failed to invalidate read")
	}
	if err := os.Symlink("elsewhere", filepath.Join(dir, "file")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if result := readBuildInput(key); result.value.err == nil || !strings.Contains(result.value.err.Error(), "symbolic links") {
		t.Fatalf("symlink accepted: %v", result.value.err)
	}
}

func TestBuildReadEstablishedRootLink(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "real")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "root")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	key := buildReadKey{link, "ReadString", "file"}
	value := readBuildInput(key)
	if value.value.err != nil {
		t.Fatal(value.value.err)
	}
	inputs := &buildSnapshot{reads: map[buildReadKey]buildRead{key: value}}
	if !inputs.current() {
		t.Fatal("established root link unstable")
	}
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "file"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("redirected root with same bytes failed to invalidate")
	}
}

func TestComptimeBuildNativeEntrypoint(t *testing.T) {
	for _, source := range []string{
		"fn main() uses build unsafe go {}",
		`import "bork/build"
fn text() uses build:String{build.ReadString("x")}
fn main() uses io + build unsafe go {fmt.Println(text())}`,
	} {
		dir := t.TempDir()
		for name, data := range map[string]string{ModFile: "module example.com/buildinputs\nunsafe \"example.com/buildinputs\"\n", "main.bork": source, "x": "frozen"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, _, err := Check(dir)
		if err == nil || !strings.Contains(err.Error(), "build effects may run only inside comptime") {
			t.Fatalf("native build entrypoint accepted: %v", err)
		}
	}
}

func TestComptimeBuildInputPreflight(t *testing.T) {
	for _, tc := range []struct{ name, source, data, want string }{
		{"string", `pred good(s:String){s=="hello"}
fn must(s:String where good):Int{42}
fn main(){println(comptime{must(build.ReadString("input"))})}`, "hello", ""},
		{"bytes", `pred good(xs:List[Byte]){xs==[toByte(1),toByte(2)]}
fn must(xs:List[Byte] where good):Int{42}
fn main(){println(comptime{must(build.ReadBytes("input"))})}`, string([]byte{1, 2}), ""},
		{"empty rejected", `pred nonEmpty(s:String){s!=""}
fn must(s:String where nonEmpty):Int{panic("must not execute")}
fn main(){println(comptime{must(build.ReadString("input"))})}`, "", "nonEmpty(\"\") is false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"main.bork": "import \"bork/build\"\n" + tc.source, "input": tc.data} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := Check(dir)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "must not execute") {
				t.Fatalf("want preflight %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildReadFrozenInventory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("frozen"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := buildReadKey{dir, "ReadString", "file"}
	inputs := &buildSnapshot{reads: map[buildReadKey]buildRead{key: readBuildInput(key)}}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	data, err := inputs.frozen(key)
	if err != nil || string(data) != "frozen" {
		t.Fatalf("frozen replay reread disk: %q %v", data, err)
	}
	if _, err := inputs.frozen(buildReadKey{dir, "ReadString", "absent"}); err == nil || !strings.Contains(err.Error(), "absent from the captured inventory") {
		t.Fatalf("unrecorded key accepted: %v", err)
	}
}

func TestBuildReadComponentReplacement(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := buildReadKey{dir, "ReadString", "sub/file"}
	inputs := &buildSnapshot{reads: map[buildReadKey]buildRead{key: readBuildInput(key)}}
	old := filepath.Join(dir, "old")
	if err := os.Rename(sub, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Preserve the file identity and bytes; only its parent component changes.
	if err := os.Link(filepath.Join(old, "file"), filepath.Join(sub, "file")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if inputs.current() {
		t.Fatal("component replacement with unchanged file identity was ignored")
	}
}

func TestFailedBuildReadAttemptRetainsTrigger(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(`import "bork/build"
fn main(){println(comptime{build.ReadString("missing")})}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = checkLoadedProgramTracked(loaded, module, captureGoContext(), captureEmbedsSnapshot, &goUsage{}, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot read build input") {
		t.Fatalf("missing read accepted: %v", err)
	}
	if loaded.Inputs.rooted == nil || len(loaded.Inputs.rooted.dependencies()) != 1 || !loaded.Inputs.current() {
		t.Fatal("stable failed read was not retained as an attempted input")
	}
	if err := os.WriteFile(filepath.Join(dir, "missing"), []byte("now present"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loaded.Inputs.current() {
		t.Fatal("new file did not invalidate failed attempt")
	}
	if _, _, err := Check(dir); err != nil {
		t.Fatalf("fresh attempt did not recover: %v", err)
	}
}
