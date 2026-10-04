package driver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func roundtripSourceReceipt(t *testing.T, inputs *sourceSnapshot) (*sourceReceipt, *sourceSnapshot) {
	t.Helper()
	receipt, err := inputs.receipt()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded sourceReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	replay, err := decoded.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs.dependencies(), replay.dependencies()) {
		t.Fatal("receipt changed observations")
	}
	return &decoded, replay
}

func TestSourceReceiptReplaysLoader(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main.bork")
	if err := os.WriteFile(main, []byte("fn main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSnapshot(root)
	if err != nil || loaded.Diags.Len() != 0 {
		t.Fatalf("load: %v", err)
	}
	_, replay := roundtripSourceReceipt(t, loaded.Inputs)
	if !replay.current() {
		t.Fatal("unchanged receipt not current")
	}
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	files, rootPath, diags, err := loadFrom(root, replay)
	if err != nil || diags.Len() != 0 || rootPath != loaded.Root || !reflect.DeepEqual(files, loaded.Files) {
		t.Fatalf("frozen replay differs: %v", err)
	}
	if replay.current() {
		t.Fatal("deletion not observed")
	}
}

func TestSourceReceiptContentMembershipAndMissing(t *testing.T) {
	for _, change := range []string{"content", "membership", "missing"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "main.bork")
			missing := filepath.Join(root, "missing")
			if err := os.WriteFile(file, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			stat, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			inputs := newSourceSnapshot()
			if _, err := inputs.readFile(file); err != nil {
				t.Fatal(err)
			}
			if _, err := inputs.directory(root); err != nil {
				t.Fatal(err)
			}
			if _, err := inputs.isDirectory(root); err != nil {
				t.Fatal(err)
			}
			if _, err := inputs.readFile(missing); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			receipt, replay := roundtripSourceReceipt(t, inputs)
			// Encoded and restored buffers must not alias the original inventory.
			for i := range receipt.Reads {
				if len(receipt.Reads[i].Data) != 0 {
					receipt.Reads[i].Data[0] = 'x'
				}
			}
			bytes, err := replay.readFile(file)
			if err != nil || string(bytes) != "old" {
				t.Fatalf("owned replay %q: %v", bytes, err)
			}
			switch change {
			case "content":
				if err := os.WriteFile(file, []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, stat.ModTime(), stat.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "membership":
				if err := os.WriteFile(filepath.Join(root, "extra"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.WriteFile(missing, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if replay.current() {
				t.Fatal("changed receipt considered current")
			}
		})
	}
}

func TestSourceReceiptRawPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix path resolution")
	}
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("target/sub", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("target/value", []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("value", []byte("root"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target/sub", "link"); err != nil {
		t.Fatal(err)
	}
	inputs := newSourceSnapshot()
	for _, name := range []string{"link/../value", "absent/../value", ""} {
		_, _ = inputs.readFile(name)
	}
	_, replay := roundtripSourceReceipt(t, inputs)
	bytes, err := replay.readFile("link/../value")
	if err != nil || string(bytes) != "target" {
		t.Fatalf("raw path %q: %v", bytes, err)
	}
	if _, err := replay.readFile("absent/../value"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if !replay.current() {
		t.Fatal("raw paths changed through serialization")
	}
}

func TestSourceReceiptUnsupported(t *testing.T) {
	for _, mutation := range []func(*sourceSnapshot){
		func(s *sourceSnapshot) { s.driveContext = true },
		func(s *sourceSnapshot) { s.rooted = &buildSnapshot{} },
		func(s *sourceSnapshot) { s.cwdErr = fs.ErrPermission },
		func(s *sourceSnapshot) {
			s.reads[sourceReadKey{"file", filepath.Join(s.cwd, "bad")}] = sourceRead{err: fs.ErrNotExist}
		},
	} {
		s := newSourceSnapshot()
		mutation(s)
		if _, err := s.receipt(); !errors.Is(err, errUnsupportedSourceReceipt) {
			t.Fatalf("unsupported receipt: %v", err)
		}
	}
	for _, mutation := range []func(*sourceReceipt){
		func(r *sourceReceipt) { r.Schema++ }, func(r *sourceReceipt) { r.Platform = "unknown" },
		func(r *sourceReceipt) { r.Cwd = "relative" },
		func(r *sourceReceipt) { r.Reads = []sourceReceiptRead{{Kind: "file", Path: "relative"}} },
		func(r *sourceReceipt) { r.Reads = []sourceReceiptRead{{Kind: "unknown"}} },
		func(r *sourceReceipt) { r.Reads = []sourceReceiptRead{{Kind: "file"}, {Kind: "file"}} },
		func(r *sourceReceipt) { r.Reads = []sourceReceiptRead{{Kind: "file", Directory: true}} },
		func(r *sourceReceipt) {
			r.Reads = []sourceReceiptRead{{Kind: "directory", Entries: []sourceReceiptEntry{{Name: "../bad"}}}}
		},
		func(r *sourceReceipt) {
			r.Reads = []sourceReceiptRead{{Kind: "file", Missing: &sourceReceiptMissing{Op: "open", Errno: 0}}}
		},
	} {
		r := &sourceReceipt{Schema: sourceReceiptSchema, Platform: runtime.GOOS, Cwd: t.TempDir()}
		mutation(r)
		if _, err := r.snapshot(); !errors.Is(err, errUnsupportedSourceReceipt) {
			t.Fatalf("invalid receipt: %v", err)
		}
	}
}

func TestSourceReceiptInvalidUTF8Declines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix byte filenames")
	}
	root := t.TempDir()
	invalid := filepath.Join(root, "bad"+string([]byte{0xff}))
	normalized := filepath.Join(root, "bad\uFFFD")
	for _, path := range []string{invalid, normalized} {
		if err := os.WriteFile(path, []byte("same"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, observe := range []func(*sourceSnapshot){
		func(s *sourceSnapshot) { _, _ = s.readFile(invalid) },
		func(s *sourceSnapshot) { _, _ = s.directory(root) },
		func(s *sourceSnapshot) { s.cwd = invalid },
	} {
		s := newSourceSnapshot()
		observe(s)
		if _, err := s.receipt(); !errors.Is(err, errUnsupportedSourceReceipt) {
			t.Fatalf("invalid UTF-8 accepted: %v", err)
		}
	}
}
