package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeAssembly(t *testing.T) {
	t.Parallel()
	source := `type Config = {}
type Db = {}
type Server = {}
fn config(): Config { Config {} }
fn db(c: Config, s: Scope): Db { Db {} }
fn server(d: Db, c: Config): Server { Server {} }
fn scenario(s: Scope): Server { assemble[Server](s, server, db, config) }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "assemble[")
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	for _, col := range []int{column, column + len("assemble[Server]")} {
		result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, col), "")
		if err != nil {
			t.Fatal(err)
		}
		a := result.Assembly
		if a == nil || result.Type != "Server" || a.Mode != "assemble" || a.Effects != "nothing" {
			t.Fatalf("unexpected assembly description: %+v", result)
		}
		if fmt.Sprint(a.Order) != "[3 2 1]" || len(a.Providers) != 3 || len(a.Roots) != 1 || a.Roots[0].Provider != 1 {
			t.Fatalf("unexpected graph: %+v", a)
		}
		if a.Providers[0].Dependencies[0].Provider != 2 || a.Providers[0].Dependencies[1].Provider != 3 || !a.Providers[1].Dependencies[1].Scope {
			t.Fatalf("missing shared dependency or scope edge: %+v", a.Providers)
		}
		if !strings.Contains(a.Tree, "shared") || !strings.Contains(a.Tree, "s: Scope <- target scope") {
			t.Fatalf("missing tree context: %s", a.Tree)
		}
		data, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(data), `"assembly":`) {
			t.Fatalf("missing structured assembly: %s (%v)", data, err)
		}
	}
}

func TestDescribeTupleProviders(t *testing.T) {
	t.Parallel()
	source := `type Config = {}
type Db = {}
type Server = {}
fn config(): Config { Config {} }
fn db(c: Config, s: Scope): Db { Db {} }
fn server(d: Db): Server { Server {} }
Wiring = (config, db, server)
fn scenario(s: Scope): Server { assemble[Server](s, Wiring) }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "assemble[")
	line := strings.Count(source[:offset], "\n") + 1
	column := offset - strings.LastIndex(source[:offset], "\n")
	result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
	if err != nil {
		t.Fatal(err)
	}
	graph := result.Assembly
	if graph == nil || fmt.Sprint(graph.Order) != "[1 2 3]" || len(graph.Providers) != 3 {
		t.Fatalf("missing graph: %+v", graph)
	}
	for i, p := range graph.Providers {
		if p.Tuple != "Wiring" || p.Index == nil || *p.Index != i || p.ElementPosition == nil || p.Label != fmt.Sprintf("Wiring.%d", i) {
			t.Fatalf("missing tuple origin: %+v", p)
		}
	}
	if graph.Providers[1].Dependencies[0].Name != "c" {
		t.Fatalf("missing declared parameter name: %+v", graph.Providers[1])
	}
	data, err := json.Marshal(result)
	if err != nil || strings.Contains(string(data), "provider_bundle") || !strings.Contains(string(data), `"tuple":"Wiring"`) {
		t.Fatalf("unexpected graph JSON: %s (%v)", data, err)
	}
}

func TestTupleReplacementDocumentation(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "docs", "std", "test.md"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseDocPage(string(source), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range page.blocks {
		if block.lang != "bork" {
			continue
		}
		file := filepath.Join(t.TempDir(), "main.bork")
		if err := os.WriteFile(file, []byte(block.source), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Check(file); err != nil {
			t.Fatalf("line %d: %v", block.line, err)
		}
	}
}
