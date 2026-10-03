package driver

import (
	"encoding/json"
	"fmt"
	"github.com/GiGurra/bork/internal/describe"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDescribeAssembly(t *testing.T) {
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

func TestDescribeProviderBundle(t *testing.T) {
	source := `ambient trace: String
type Config = {}
type Db = {}
type Server = {}
fn config(): Config { Config {} }
fn fake(): Config { Config {} }
fn db(c: Config, s: Scope): Db { Db {} }
fn server(d: Db) needs trace: Server { if (trace == "") { Server {} } else { Server {} } }
providers Wiring = { config: config, database: db, server: server }
fn scenario(s: Scope) needs trace: Server { assemble[Server](s, Wiring(config: fake)) }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	query := func(text string, shift int) *describe.Result {
		t.Helper()
		offset := strings.Index(source, text) + shift
		line := strings.Count(source[:offset], "\n") + 1
		column := offset - strings.LastIndex(source[:offset], "\n")
		result, err := Describe(fmt.Sprintf("%s:%d:%d", path, line, column), "")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	declaration := query("providers Wiring", len("providers "))
	for _, result := range []*describe.Result{declaration, query("Wiring(config", 0), query("Wiring(config", len("Wiring"))} {
		bundle := result.ProviderBundle
		if result.Type != "provider bundle" || bundle == nil || len(bundle.Entries) != 3 || bundle.Name != "Wiring" {
			t.Fatalf("unexpected bundle description: %+v", result)
		}
		if bundle.Entries[1].Product != "Db" || fmt.Sprint(bundle.Entries[1].Dependencies) != "[Config Scope]" {
			t.Fatalf("missing bundle contracts: %+v", bundle.Entries)
		}
		if fmt.Sprint(bundle.Entries[2].Needs) != "[trace]" {
			t.Fatalf("missing ambient requirement: %+v", bundle.Entries[2])
		}
		replaced := result != declaration
		if bundle.Entries[0].Replaced != replaced {
			t.Fatalf("incorrect replacement metadata: %+v", bundle.Entries)
		}
		if replaced && bundle.Entries[0].Function != "fake" {
			t.Fatalf("original contract leaked into specialized bundle: %+v", bundle.Entries)
		}
		data, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(data), `"provider_bundle":`) {
			t.Fatalf("missing bundle JSON: %s (%v)", data, err)
		}
	}
	graph := query("assemble[Server]", 0).Assembly
	if graph == nil || fmt.Sprint(graph.Order) != "[1 2 3]" || graph.Providers[0].Bundle != "Wiring" || graph.Providers[0].Entry != "config" || graph.Providers[0].EntryPosition == nil || !strings.Contains(graph.Tree, "Wiring.config (fake)") {
		t.Fatalf("missing flattened bundle graph metadata: %+v", graph)
	}
}
