package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodecPackageMigration(t *testing.T) {
	t.Parallel()
	source := `import other "example.com/validator/codec"
import "bork/codec"
import "bork/json"
use codec.Defaults
type Value={n:Int}
type Field={name:String}
type DecodeError={message:String}
type Json={n:Int} derive(codec.Decode,codec.Encode)
type Record={n:Int} derive(codec.Decode,codec.Encode)
fn main(){println(other.Token{});println(json.Decode[Record]("{\"n\":2}"));println(json.Encode(Record{n:3}));println(codec.encode(4));println(json.Encode(Json{n:5}));println(json.Decode[Json]("{\"n\":6}"))}`
	dir := validatorFixture(t, source)
	pkgDir := filepath.Join(dir, "codec")
	if err := os.Mkdir(pkgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "token.bork"), []byte("type Token={}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, data)
	}
	if !strings.Contains(string(data), "Record { n: 2 }") || !strings.Contains(string(data), `{"n":3}`) {
		t.Fatalf("output: %s", data)
	}
}

func TestGoStructWithoutCodecImport(t *testing.T) {
	t.Parallel()
	source := `type Plain={n:Int} derive(GoStruct)
fn Read[T:GoStruct]():T|GoValueError unsafe go {
 out,errs:=_d_T_GoStruct.FromGo(_d_T_GoStruct.New())
 if len(errs)>0{return errs[0]}
 return out
}
fn main(){println(Read[Plain]())}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	data, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, data)
	}
	if string(data) != "Plain { n: 0 }\n" {
		t.Fatalf("output: %s", data)
	}
}

func TestCodecExplicitSelection(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/codec"
type T={n:Int} derive(codec.Decode)
fn main(){}`, "use codec.Defaults")
	for _, name := range []string{"Json", "JsonField", "DecodeError", "JsonError"} {
		t.Run(name, func(t *testing.T) {
			checkPreludeSource(t, "fn missing(value:"+name+"){}\nfn main(){}", "unknown type "+name)
		})
	}
	for _, name := range []string{"Encode", "Decode"} {
		t.Run(name, func(t *testing.T) {
			checkPreludeSource(t, "type T={} derive("+name+")\nfn main(){}", "unknown class "+name)
		})
	}
}

func TestCodecNumericWidths(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
import "bork/json"
use codec.Defaults
fn main(){
 println(json.Decode[Int8]("127"));println(json.Decode[Int8]("128"))
 println(json.Decode[Uint8]("255"));println(json.Decode[Uint8]("-1"))
 println(json.Decode[Uint64]("18446744073709551615"));println(json.Decode[Uint64]("18446744073709551616"))
 println(json.Decode[Float32]("3.5"));println(json.Decode[Float32]("1e100"))
 println(json.Encode(toInt16(-32768)));println(json.Encode(toUint32(4294967295)))
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	text := string(output)
	if strings.Count(text, "DecodeError") != 4 || !strings.Contains(text, "18446744073709551615\n") || !strings.Contains(text, "-32768\n4294967295\n") {
		t.Fatalf("numeric codecs: %s", output)
	}
}

func TestCodecSameNamedClassBounds(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
use codec.Defaults
class Decode[T]{fn read(x:T):Int}
instance mine:Decode[Int]{fn read(x:Int):Int{x}}
fn Both[T:Decode+codec.Decode](x:T):Int{read(x)}
fn main(){println(Both(1))}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	if string(output) != "1\n" {
		t.Fatalf("output: %s", output)
	}
}
