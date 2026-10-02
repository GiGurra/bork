package format

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

var update = flag.Bool("update", false, "rewrite formatter golden files")

func TestSource(t *testing.T) {
	inputs, err := filepath.Glob("testdata/rules/*.input")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no formatter rule fixtures")
	}
	for _, path := range inputs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Source(path, src)
			if err != nil {
				t.Fatal(err)
			}
			stable(t, src, got)
			golden := strings.TrimSuffix(path, ".input") + ".golden"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("format %s\ngot  %q\nwant %q", path, got, want)
			}
		})
	}
	if _, err := Source("test.bork", []byte("fn f() { @ }")); err == nil {
		t.Fatal("expected lexical error")
	}
}

func stable(t *testing.T, src, got []byte) {
	t.Helper()
	again, err := Source("test.bork", got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, again) {
		t.Fatalf("not idempotent:\n%s\nthen:\n%s", got, again)
	}
	lex := func(s []byte) ([]syntax.Token, []syntax.Comment) {
		d := &diag.List{}
		ts, cs := syntax.Lex("test.bork", s, d)
		if d.Len() != 0 {
			t.Fatal(d.Error())
		}
		for i := range ts {
			ts[i].Pos = diag.Pos{}
		}
		for i := range cs {
			cs[i].Pos = diag.Pos{}
		}
		return ts, cs
	}
	before, commentsBefore := lex(src)
	after, commentsAfter := lex(got)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("token stream changed:\nbefore: %+v\nafter: %+v", before, after)
	}
	if !reflect.DeepEqual(commentsBefore, commentsAfter) {
		t.Fatal("comments changed")
	}
}

func TestCorpusClean(t *testing.T) {
	for _, root := range []string{"testdata", "examples"} {
		err := filepath.WalkDir(filepath.Join("../..", root), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".bork") {
				return nil
			}
			rel, err := filepath.Rel("../..", path)
			if err != nil {
				return err
			}
			t.Run(rel, func(t *testing.T) {
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				got, err := Source(path, src)
				if err != nil {
					d := &diag.List{}
					syntax.Lex(path, src, d)
					if d.Len() != 0 {
						if _, err := Files([]string{path}, false); err == nil {
							t.Fatal("invalid fixture was accepted")
						}
						if got != nil {
							t.Fatal("invalid source produced formatted output")
						}
						unchanged, readErr := os.ReadFile(path)
						if readErr != nil || !bytes.Equal(src, unchanged) {
							t.Fatal("invalid source was modified")
						}
						return
					}
					t.Fatal(err)
				}
				stable(t, src, got)
				if !bytes.Equal(src, got) {
					t.Fatalf("%s is not fmt-clean; run bork fmt testdata examples", rel)
				}
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "main.bork")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	src := []byte("fn main(){println(1)}")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Files([]string{dir, path}, true)
	if err != nil || len(changed) != 1 {
		t.Fatalf("check: %v, %v", changed, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, src) {
		t.Fatal("check wrote source")
	}
	if _, err := Files([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	changed, err = Files([]string{dir}, true)
	if err != nil || len(changed) != 0 {
		t.Fatalf("still needs formatting: %v, %v", changed, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("changed file permissions")
	}
	bad := filepath.Join(dir, "z-invalid.bork")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("@"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Files([]string{path, bad}, false); err == nil {
		t.Fatal("expected error")
	}
	unchanged, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, src) {
		t.Fatal("invalid file allowed partial edits")
	}
	for _, explicit := range []string{bad, dir + "/nested/../z-invalid.bork"} {
		if _, err := Files([]string{dir, explicit}, false); err == nil {
			t.Fatal("explicit invalid file accepted with directory")
		}
		unchanged, err = os.ReadFile(path)
		if err != nil || !bytes.Equal(unchanged, src) {
			t.Fatal("directory overrode explicit file protection")
		}
	}
	if _, err := Files([]string{dir}, false); err == nil {
		t.Fatal("directory should report invalid source")
	}
	formatted, err := os.ReadFile(path)
	if err != nil || bytes.Equal(formatted, src) {
		t.Fatal("directory did not format valid source")
	}
	unchanged, err = os.ReadFile(bad)
	if err != nil || string(unchanged) != "@" {
		t.Fatal("directory changed invalid source")
	}
}

func TestDirectorySkips(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".git", "vendor"} {
		path := filepath.Join(dir, name, "invalid.bork")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("@"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	external := filepath.Join(t.TempDir(), "external.bork")
	if err := os.WriteFile(external, []byte("@"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.bork")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Files([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Files([]string{link}, false); err == nil {
		t.Fatal("explicit symlink accepted")
	}
}
