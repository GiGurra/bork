package main

import (
	"bytes"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/GiGurra/bork/internal/gen"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedArtifactCurrent(t *testing.T) {
	const runtimePath = "../../../stdvalidators/runtime.go"
	const generatedPath = "../../../stdvalidators/generated.go"
	output := filepath.Join(t.TempDir(), "generated.go")
	savedArgs := os.Args
	os.Args = []string{"genstdvalidators", runtimePath, output}
	t.Cleanup(func() { os.Args = savedArgs })
	if err := run(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal(staleArtifactMessage(expected, actual))
	}
}

func artifactSourceDigests(source []byte) map[string]string {
	out := map[string]string{}
	file, err := parser.ParseFile(token.NewFileSet(), "artifact.go", source, 0)
	if err != nil {
		return out
	}
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "Sources" {
			return true
		}
		ast.Inspect(field.Value, func(node ast.Node) bool {
			record, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			values := map[string]string{}
			for _, entry := range record.Elts {
				pair, ok := entry.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok {
					continue
				}
				literal, ok := pair.Value.(*ast.BasicLit)
				if !ok {
					continue
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil {
					values[key.Name] = value
				}
			}
			if values["Path"] != "" {
				out[values["Path"]] = values["Digest"]
			}
			return true
		})
		return false
	})
	return out
}

func staleArtifactMessage(expected, actual []byte) string {
	old, new := artifactSourceDigests(expected), artifactSourceDigests(actual)
	changed := map[string]bool{}
	for path, digest := range old {
		if new[path] != digest {
			changed[path] = true
		}
	}
	for path, digest := range new {
		if old[path] != digest {
			changed[path] = true
		}
	}
	runtimeDigest := func(data []byte) string {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "// Runtime support SHA256:") {
				return line
			}
		}
		return ""
	}
	if runtimeDigest(expected) != runtimeDigest(actual) {
		changed["internal/stdvalidators/runtime.go"] = true
	}
	var names []string
	for path := range changed {
		names = append(names, path)
	}
	sort.Strings(names)
	detail := strings.Join(names, ", ")
	if detail == "" {
		detail = "artifact generator output (internal/gen)"
	}
	return "standard validator artifact is stale; changed sources: " + detail + "; run go generate ./internal/stdvalidators"
}

func TestStaleArtifactMessageNamesSourceAndFix(t *testing.T) {
	old := []byte(`package p;var x=[]Descriptor{{Sources:[]Source{{Path:"prelude/options.bork",Digest:"old"}}}}`)
	new := bytes.ReplaceAll(old, []byte(`Digest:"old"`), []byte(`Digest:"new"`))
	message := staleArtifactMessage(old, new)
	if !strings.Contains(message, "prelude/options.bork") || !strings.Contains(message, "go generate ./internal/stdvalidators") {
		t.Fatal(message)
	}
}

func TestUnrelatedPreludeSourceDoesNotRegenerate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(`import "bork/sql"
 fn touch(value:sql.Builder){}
 fn main(){}`), 0600); err != nil {
		t.Fatal(err)
	}
	files, info, err := driver.Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, err := os.ReadFile("../../../stdvalidators/runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	original, err := gen.StdValidatorArtifact(files, info, runtimeSource)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, file := range files {
		if file.Path == "prelude/runes.bork" {
			file.Source += "\n// unrelated edit\n"
			changed = true
		}
	}
	if !changed {
		t.Fatal("missing unrelated prelude fixture")
	}
	updated, err := gen.StdValidatorArtifact(files, info, runtimeSource)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, updated) {
		t.Fatal("unrelated rune source regenerated validator artifact")
	}
	for _, file := range files {
		if file.Path == "prelude/interpolation.bork" {
			file.Source += "\n// relevant edit\n"
		}
	}
	updated, err = gen.StdValidatorArtifact(files, info, runtimeSource)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(original, updated) {
		t.Fatal("relevant validator ABI source did not regenerate")
	}
	message := staleArtifactMessage(original, updated)
	if !strings.Contains(message, "prelude/interpolation.bork") {
		t.Fatal(message)
	}
}
