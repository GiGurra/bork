package driver

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCodecDefaultInput(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
import "bork/json"
use codec.Defaults
type Mode = sealed { Quiet, Loud, Other(String) codec {fallback:true} } derive(codec.Decode)
type Payload = sealed { Empty, Pair(Int, String), Named { text:String } } derive(codec.Decode)
type Data = { widths:(Int8,Uint64,Float32) = (2,3,1.5), rune:Rune='x', tags:List[Mode]=[.Loud,.Other("custom")], entries:Map[String,Option[Payload]]={"a":.Some(.Pair(2,"two")),"b":.Some(.Named{text:"named"}),"c":.Some(.Empty),"d":.None}, raw:codec.Value=.Bool{value:true}, lazy computed:Int=tags.length() } derive(codec.Decode)
type Options = { data:Data = .{} } derive(codec.Decode)
fn badBridge(provider:codec.DefaultInput):codec.Value|codec.DecodeError unsafe go {
 // Deliberately violate the private bridge contract; safe callers cannot do this.
 return provider.callback("wrong type")
}
fn main(){
 match(codec.Schema[Options]()){
  Option.Some(schema)=>schema.fields.forEach(field=>{
   match(field.defaultInput){
    Option.Some(provider)=>match(provider()){
     value:codec.Value=>{println(json.Render(value));println(codec.decode[Data](value))}
     error:codec.DecodeError=>println(error)
    }
    Option.None=>println("missing provider")
   }
  })
  Option.None=>println("missing schema")
 }
 println(badBridge(codec.Input[Int](value=>codec.Value.Number{text:toString(value)})))
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`"widths":[2,3,1.5]`, `"rune":"x"`, `"tags":["LOUD","custom"]`, `"a":{"type":"Pair","values":[2,"two"]}`, `"b":{"type":"Named","text":"named"}`, `"c":{"type":"Empty"}`, `"d":null`, `"raw":true`, `internal error: default input provider type mismatch`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
	if strings.Contains(string(out), `"computed"`) {
		t.Fatalf("computed field materialized: %s", out)
	}
}

func TestCodecDefaultInputBridgeIsPrivate(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/codec"
use codec.Defaults
fn main() {
 provider = codec.Input[Int](value => codec.Value.Number {text:toString(value)})
 _ = provider.callback(codec.Value.Null)
}`, "argument 1")
	checkPreludeSource(t, `import "bork/codec"
use codec.Defaults
fn main() {
 provider = codec.Input[Int](value => codec.Value.Number {text:toString(value)})
 _ = codec.Input(provider.callback)
}`, "no instance of Decode")
	checkPreludeSource(t, `import "bork/codec"
fn main() {
 _ = codec.DefaultInput {callback: value => codec.Value.Null, matches: value => true, supported:true}
}`, "package bork/codec controls its construction")
}

func TestCodecDefaultInputRejectsNestedErasedUnions(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
use codec.Defaults
instance one:codec.Decode[Int|String]{fn decode(input:codec.Value):Int|String|codec.DecodeError{1}}
instance two:codec.Decode[Bool|Float]{fn decode(input:codec.Value):Bool|Float|codec.DecodeError{true}}
type Box[T]={value:T}
instance box[T:codec.Decode]:codec.Decode[Box[T]]{
 metadata codec.DefaultInput=codec.Input[Box[Int|String]](box=>match(box.value){i:Int=>codec.Value.Number{text:toString(i)},s:String=>codec.Value.String{value:s}})
 fn decode(input:codec.Value):Box[T]|codec.DecodeError{match(codec.decode[T](input)){error:codec.DecodeError=>error,value:T=>Box{value:value}}}
}
type Db={box:Box[Bool|Float]}derive(codec.Decode)
type Options={db:Db=.{box:.{value:true}}}derive(codec.Decode)
fn main(){match(codec.Schema[Options]()){
 Option.Some(schema)=>schema.fields.forEach(field=>match(field.defaultInput){Option.Some(provider)=>println(provider()),Option.None=>println("none")})
 Option.None=>println("no schema")
}}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `path: ".box"`) || !strings.Contains(string(out), "union DefaultInput providers require generic metadata keys") {
		t.Fatalf("output: %s", out)
	}
}
