package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func embedRequest(root, kind, path string) *check.Embedded {
	return &check.Embedded{Pos: diag.Pos{File: filepath.Join(root, "main.bork")}, Kind: kind, Path: path}
}
func TestEmbedInputsFrozenBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := newEmbedSnapshot(newSourceSnapshot())
	request := embedRequest(root, "ReadBytes", "asset")
	files, err := inputs.capture(request)
	if err != nil {
		t.Fatal(err)
	}
	files[0].Data[0] = '!'
	before := inputs.dependencies()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	files, err = inputs.capture(request)
	if err != nil || string(files[0].Data) != "old" {
		t.Fatalf("frozen data: %v, %v", files, err)
	}
	if !reflect.DeepEqual(before, inputs.dependencies()) || inputs.current() {
		t.Fatal("frozen inventory changed or same-mtime edit missed")
	}
	fresh, err := newEmbedSnapshot(newSourceSnapshot()).capture(request)
	if err != nil || string(fresh[0].Data) != "new" {
		t.Fatalf("fresh data: %v, %v", fresh, err)
	}
}

func TestEmbedInputsDirectoryMembership(t *testing.T) {
	root := t.TempDir()
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "file"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := embedRequest(root, "Directory", "assets")
	inputs := newEmbedSnapshot(newSourceSnapshot())
	files, err := inputs.capture(request)
	if err != nil || len(files) != 1 || files[0].Name != "file" {
		t.Fatalf("capture: %v, %v", files, err)
	}
	if !inputs.current() {
		t.Fatal("unchanged directory invalidated")
	}
	if err := os.Mkdir(filepath.Join(assets, "empty", "new-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("empty directory membership change missed")
	}
	replay, err := inputs.capture(request)
	if err != nil || !reflect.DeepEqual(files, replay) {
		t.Fatalf("frozen membership: %v, %v", replay, err)
	}
	fresh := newEmbedSnapshot(newSourceSnapshot())
	if _, err := fresh.capture(request); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(assets, "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(assets, "file"), 0o755); err != nil {
		t.Fatal(err)
	}
	if fresh.current() {
		t.Fatal("file-to-directory replacement missed")
	}
}

func TestEmbedInputsNegativeAndSymlinkReads(t *testing.T) {
	root := t.TempDir()
	request := embedRequest(root, "ReadString", "missing")
	inputs := newEmbedSnapshot(newSourceSnapshot())
	if _, err := inputs.capture(request); err == nil {
		t.Fatal("missing read succeeded")
	}
	if err := os.WriteFile(filepath.Join(root, "missing"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inputs.capture(request); err == nil {
		t.Fatal("negative replay changed")
	}
	if inputs.current() {
		t.Fatal("new file missed")
	}
	fresh := newEmbedSnapshot(newSourceSnapshot())
	if _, err := fresh.capture(request); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "missing"), filepath.Join(root, "real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "missing")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if fresh.current() {
		t.Fatal("same-byte symlink replacement missed")
	}
	if _, err := newEmbedSnapshot(newSourceSnapshot()).capture(request); err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("symlink read: %v", err)
	}
}

func TestEmbedInputsConcurrentReplay(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "asset"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := newEmbedSnapshot(newSourceSnapshot())
	request := embedRequest(root, "ReadBytes", "asset")
	var tasks sync.WaitGroup
	for range 8 {
		tasks.Go(func() {
			files, err := inputs.capture(request)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(files[0].Data, []byte("old")) {
				t.Error("shared mutation")
			}
			files[0].Data[0] = '!'
		})
	}
	tasks.Wait()
}

func TestEmbedInputsRetryDuringCapture(t *testing.T) {
	for _, continuous := range []bool{false, true} {
		name := "once"
		if continuous {
			name = "continuous"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "asset")
			if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			request := embedRequest(root, "ReadBytes", "asset")
			info := &check.Info{Embeds: []*check.Embedded{request}}
			diags := &diag.List{}
			captures := 0
			inputs := captureEmbedsFrom(info, diags, newSourceSnapshot(), func(sources *sourceSnapshot) *embedSnapshot {
				captures++
				inputs := newEmbedSnapshot(sources)
				edit := continuous || captures == 1
				inputs.read = func(key embedReadKey) embedRead {
					value := readEmbed(key)
					if edit {
						edit = false
						next := "new"
						if string(value.files[0].Data) == next {
							next = "old"
						}
						if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
							t.Error(err)
						}
					}
					return value
				}
				return inputs
			})
			if captures != 2 {
				t.Fatalf("captures = %d", captures)
			}
			if continuous {
				if inputs != nil || diags.Len() != 1 || request.Files != nil {
					t.Fatal("continuous edits published assets")
				}
				return
			}
			if inputs == nil || diags.Len() != 0 || string(request.Files[0].Data) != "new" {
				t.Fatalf("retry: %v, %v", request.Files, diags)
			}
		})
	}
}

func TestEmbedInputsPreserveErrorPaths(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, file := range []string{"main.bork", "missing-root/main.bork"} {
		request := &check.Embedded{Pos: diag.Pos{File: file}, Kind: "ReadBytes", Path: "missing"}
		_, want := captureEmbed(request)
		_, got := newEmbedSnapshot(newSourceSnapshot()).capture(request)
		if errorText(got) != errorText(want) {
			t.Fatalf("error path changed: %v; want %v", got, want)
		}
	}
}

func TestEmbedInputsInventoryInvalidUTF8(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "invalid")
	if err := os.WriteFile(path, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := newEmbedSnapshot(newSourceSnapshot())
	request := embedRequest(root, "ReadString", "invalid")
	if _, err := inputs.capture(request); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid read: %v", err)
	}
	if err := os.WriteFile(path, []byte{0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("different invalid bytes omitted from inventory")
	}
}

func TestCompilationRetainsAssetInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "asset"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(`import "bork/embed"
fn main() { println(embed.ReadString("asset")) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	program, err := checkProgramObserved(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if program.assets == nil || len(program.assets.dependencies()) != 1 {
		t.Fatal("asset inventory not retained")
	}
	request := program.info.Embeds[0]
	request.Files[0].Data[0] = '!'
	if !program.assets.current() {
		t.Fatal("caller mutated captured inventory")
	}
	if err := os.WriteFile(filepath.Join(root, "asset"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if program.assets.current() {
		t.Fatal("asset content edit missed")
	}
	files, err := program.assets.capture(request)
	if err != nil || string(files[0].Data) != "old" || files[0].StagePath != "" {
		t.Fatalf("request replay: %v, %v", files, err)
	}
}
