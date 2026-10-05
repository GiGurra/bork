package driver

import (
	"github.com/GiGurra/bork/internal/diag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneDeriveChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"duplicate inline", "type T={} derive(codec.Decode)\nderive codec.Decode for T\nfn main(){}", "instance TDecode is already declared"},
		{"duplicate alias", "type T={}\ntype Alias=T\nderive codec.Decode for T\nderive codec.Decode for Alias\nfn main(){}", "already derived"},
		{"duplicate standalone", "type T={}\nderive codec.Encode for T\nderive codec.Encode for T\nfn main(){}", "already declared"},
		{"unsupported type", "derive codec.Decode for Int\nfn main(){}", "only records and sealed types"},
		{"structural tuple", "type Pair=(Int,String)\nderive codec.Decode for Pair\nfn main(){}", "standalone derive requires a named record or sealed target"},
		{"inline tuple codecs", "type Pair=(Int,String) derive(codec.Decode,codec.Encode)\nfn main(){}", ""},
		{"missing class", "type T={}\nderive Unknown for T\nfn main(){}", "unknown class Unknown"},
		{"unknown target", "derive codec.Decode for Missing\nfn main(){}", "unknown type Missing"},
		{"free type parameter", "type T[A]={a:A}\nderive codec.Decode for T[A]\nfn main(){}", "unknown type A"},
		{"GoStruct specialization", "type T[A]={a:A}\nderive GoStruct for T[Int]\nfn main(){}", "cannot derive GoStruct for a specialization"},
		{"missing field instance", "type T={callback:(Int)=>Int}\nderive codec.Encode for T\nfn main(){}", "has no Encode instance"},
		{"GoStruct tags", "type T={value:Int go{json:\"value\"}}\nderive GoStruct for T\nfn main(){}", ""},
		{"Show rule", "type T={}\nderive Show for T\nfn main(){}", "cannot be derived"},
		{"Eq rule", "type T={}\nderive Eq for T\nfn main(){}", "Eq is built in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPreludeSource(t, "import \"bork/codec\"\nuse codec.Defaults\n"+tc.source, tc.want)
		})
	}
}

func TestStandaloneDeriveOwnership(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for path, source := range map[string]string{
		"bork.mod":           "module example.com/derive\n",
		"foreign/types.bork": "type T={value:Int}\n",
		"main.bork":          "import \"bork/codec\"\nimport \"example.com/derive/foreign\"\nuse codec.Defaults\ntype Alias=foreign.T\nderive codec.Decode for Alias\nfn main(){}",
	} {
		name := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := Check(dir)
	if err == nil || !strings.Contains(err.Error(), "declare it in the type's package example.com/derive/foreign") {
		t.Fatalf("ownership: %v", err)
	}
}

func TestStandaloneDeriveConstrainedAlias(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
import "bork/json"
use codec.Defaults
type T={n:Int}
pred positive(t:T){t.n>0}
type Positive=T where positive
derive codec.Decode for Positive
fn main(){println(json.Decode[Positive]("{\"n\":1}"));println(json.Decode[Positive]("{\"n\":0}"))}`
	dir := validatorFixture(t, source)
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, data)
	}
	output := string(data)
	if !strings.Contains(output, "T { n: 1 }") || !strings.Contains(output, "DecodeError") {
		t.Fatalf("decode: %s", output)
	}
}

func TestStandaloneDeriveTooling(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, "import \"bork/codec\"\nuse codec.Defaults\ntype Box[T]={value:T}\nderive codec.Decode for Box\nfn main(){}")
	path := filepath.Join(dir, "main.bork")
	analysis, err := NewSession().Analyze(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := analysis.ReferenceAt(diag.Pos{File: path, Line: 4, Col: 25})
	if ref == nil || ref.Definition.Line != 3 {
		t.Fatalf("type reference: %+v", ref)
	}
	class := analysis.ReferenceAt(diag.Pos{File: path, Line: 4, Col: 14})
	if class == nil || !strings.Contains(class.Definition.File, "classes.bork") && !strings.Contains(class.Definition.File, "codec.bork") {
		t.Fatalf("class reference: %+v", class)
	}
	result, err := Describe(path+":4:25", "")
	if err != nil || result.Definition == nil || result.Definition.Line != 3 {
		t.Fatalf("describe: %+v, %v", result, err)
	}
	doc, err := Doc(dir, DocOptions{})
	if err != nil || !strings.Contains(string(doc), "BoxDecode[T: codec.Decode]: codec.Decode[Box[T]]") {
		t.Fatalf("API: %s, %v", doc, err)
	}
}

func TestStandaloneDeriveGoStructConstrainedAlias(t *testing.T) {
	t.Parallel()
	source := `type T={n:Int}
pred positive(t:T){t.n>0}
type Positive=T where positive
derive GoStruct for Positive
fn Read[A:GoStruct]():A|GoValueError unsafe go {
 out,errs:=_d_A_GoStruct.FromGo(_d_A_GoStruct.New())
 if len(errs)>0 {return errs[0]}
 return out
}
fn main(){println(Read[Positive]())}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "GoValueError") || !strings.Contains(string(data), "positive") {
		t.Fatalf("conversion: %s", data)
	}
}
