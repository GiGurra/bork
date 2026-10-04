package driver

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSourceSnapshotFrozenReads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	name := filepath.Join(root, "main.bork")
	if err := os.WriteFile(name, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := newSourceSnapshot()
	bytes, err := snapshot.readFile(name)
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 'x'
	entries, err := snapshot.directory(root)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].name = "changed"
	before := snapshot.dependencies()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Equal size and mtime must not make changed bytes appear current.
	if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	bytes, err = snapshot.readFile(name)
	if err != nil || string(bytes) != "old" {
		t.Fatalf("frozen file = %q, %v", bytes, err)
	}
	entries, err = snapshot.directory(root)
	if err != nil || entries[0].name != "main.bork" {
		t.Fatalf("frozen membership = %v, %v", entries, err)
	}
	if !reflect.DeepEqual(before, snapshot.dependencies()) {
		t.Fatal("frozen inventory changed")
	}
	if snapshot.current() {
		t.Fatal("same-size, same-mtime edit was missed")
	}
}

func TestSourceSnapshotMissingModuleAndMembership(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	snapshot := newSourceSnapshot()
	module := filepath.Join(root, ModFile)
	if _, err := snapshot.readFile(module); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := snapshot.directory(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte("module example.com/new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.readFile(module); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("negative read changed: %v", err)
	}
	entries, err := snapshot.directory(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("membership changed: %v, %v", entries, err)
	}
	if snapshot.current() {
		t.Fatal("new module/membership was missed")
	}
}

func TestSourceSnapshotReplaysLoad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	api := filepath.Join(root, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		filepath.Join(root, ModFile):     "module example.com/inputs\n",
		filepath.Join(root, "main.bork"): "import \"example.com/inputs/api\"\nfn main() { api.Do() }\n",
		filepath.Join(api, "api.bork"):   "fn Do() {}\n",
	}
	for path, source := range sources {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := loadSnapshot(root)
	if err != nil || loaded.Diags.Len() != 0 {
		t.Fatalf("capture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api, "api.bork"), []byte("fn Do() { _ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.bork"), []byte("fn Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module example.com/changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	replay, rootPath, diags, err := loadFrom(root, loaded.Inputs)
	if err != nil || diags.Len() != 0 || rootPath != loaded.Root || !reflect.DeepEqual(replay, loaded.Files) {
		t.Fatalf("snapshot replay changed: %v", err)
	}
	fresh, err := loadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Root == loaded.Root || fresh.Diags.Len() == 0 {
		t.Fatal("fresh capture ignored module/source changes")
	}
}

func TestSourceSnapshotErrorPaths(t *testing.T) {
	t.Chdir(t.TempDir())
	_, want := os.ReadFile("missing.bork")
	_, got := newSourceSnapshot().readFile("missing.bork")
	if got.Error() != want.Error() || !errors.Is(got, os.ErrNotExist) {
		t.Fatalf("path error changed: %v, want %v", got, want)
	}
}

type editingSources struct {
	diskSources
	path string
	edit bool
}

func (s *editingSources) readFile(path string) ([]byte, error) {
	bytes, err := s.diskSources.readFile(path)
	if path == s.path && s.edit && err == nil {
		s.edit = false
		next := "fn main() { _ = 1 }\n"
		if string(bytes) == next {
			next = "fn main() {}\n"
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return nil, err
		}
	}
	return bytes, err
}
func TestSourceSnapshotRetriesDuringCapture(t *testing.T) {
	t.Parallel()
	for _, continuous := range []bool{false, true} {
		name := "once"
		if continuous {
			name = "continuous"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "main.bork")
			if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			captures := 0
			loaded, err := loadStableSources(root, func() *sourceSnapshot {
				captures++
				snapshot := newSourceSnapshot()
				snapshot.disk = &editingSources{path: path, edit: continuous || captures == 1}
				return snapshot
			})
			if captures != 2 {
				t.Fatalf("captures = %d, want 2", captures)
			}
			if continuous {
				if err == nil || !strings.Contains(err.Error(), "source inputs changed") {
					t.Fatalf("continuous edits = %v", err)
				}
				return
			}
			if err != nil || loaded.Diags.Len() != 0 {
				t.Fatalf("retry: %v", err)
			}
			for _, file := range loaded.Files {
				if !file.Prelude && file.Source != "fn main() { _ = 1 }\n" {
					t.Fatalf("captured stale source: %q", file.Source)
				}
			}
		})
	}
}

func TestSourceSnapshotConcurrentReplay(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	failures := make(chan error, 8)
	var tasks sync.WaitGroup
	for range 8 {
		tasks.Go(func() {
			files, _, diags, err := loadFrom(root, loaded.Inputs)
			if err != nil {
				failures <- err
				return
			}
			if diags.Len() != 0 || !reflect.DeepEqual(files, loaded.Files) {
				failures <- errors.New("replay changed AST")
				return
			}
			files[len(files)-1].Funcs[0].Name = "privateMutation"
		})
	}
	tasks.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if loaded.Files[len(loaded.Files)-1].Funcs[0].Name != "main" {
		t.Fatal("replays share mutable ASTs")
	}
}

func TestSourceSnapshotPreservesIOPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "external", "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := []byte("fn main() {}\n")
	if err := os.WriteFile(filepath.Join(root, "external", "main.bork"), valid, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("invalid @\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(child, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Joining with filepath.Join would itself erase the relevant components.
	alias := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "main.bork"
	for _, path := range []string{"", alias, root + "/missing/../main.bork"} {
		want, wantErr := os.ReadFile(path)
		got, gotErr := newSourceSnapshot().readFile(path)
		if string(got) != string(want) || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("IO changed for %q: %q, %v; want %q, %v", path, got, gotErr, want, wantErr)
		}
	}
	loaded, err := loadSnapshot(alias)
	if err != nil || loaded.Diags.Len() != 0 {
		t.Fatalf("symlink parent load: %v", err)
	}
	if loaded.Files[len(loaded.Files)-1].Source != string(valid) {
		t.Fatal("compiled a different file than requested")
	}
}

func TestSourceSnapshotAbsoluteWindowsOperands(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive context")
	}
	snapshot := newSourceSnapshot()
	volume := filepath.VolumeName(snapshot.cwd)
	for _, name := range []string{`\project`, `/project`, volume + `project`, volume + `\project`} {
		want, wantErr := filepath.Abs(name)
		got, gotErr := snapshot.absolute(name)
		if got != want || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("absolute(%q) = %q, %v; want %q, %v", name, got, gotErr, want, wantErr)
		}
	}
}
