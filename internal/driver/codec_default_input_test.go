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
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`"widths":[2,3,1.5]`, `"rune":"x"`, `"tags":["LOUD","custom"]`, `"a":{"type":"Pair","values":[2,"two"]}`, `"b":{"type":"Named","text":"named"}`, `"c":{"type":"Empty"}`, `"d":null`, `"raw":true`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
	if strings.Contains(string(out), `"computed"`) {
		t.Fatalf("computed field materialized: %s", out)
	}
}

func TestCodecDefaultInputTypedProvider(t *testing.T) {
	t.Parallel()
	checkPreludeSource(t, `import "bork/codec"
use codec.Defaults
type Token = {text:String}
instance token:codec.Decode[Token] {
 metadata codec.DefaultInput[Token] = codec.Input[String](value=>codec.Value.String{value:value})
 fn decode(input:codec.Value):Token|codec.DecodeError {Token{text:"value"}}
}
fn main(){}`, "body produces codec.DefaultInput[String]")
	checkPreludeSource(t, `import "bork/codec"
use codec.Defaults
fn main(){provider=codec.Input[Int](value=>codec.Value.Null);_ = provider.convert("wrong")}`, "argument 1")
}

func TestCodecDefaultInputUnionTargets(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
import "bork/json"
import "bork/shape"
use codec.Defaults
instance union:codec.Decode[Int|String] {
 metadata codec.DefaultInput[Int|String] = codec.Input[Int|String](value=>match(value){i:Int=>codec.Value.Number{text:toString(i)},s:String=>codec.Value.String{value:s}})
 fn decode(input:codec.Value):Int|String|codec.DecodeError {
  match(input){codec.Value.Number{}=>codec.decode[Int](input),codec.Value.String{value}=>value,_=>codec.DecodeError{path:"",message:"expected number or string"}}
 }
}
type Box[T] = {value:T} derive(codec.Decode)
type Data = {number:Int|String=7, text:Box[Int|String]=.{value:"seven"}} derive(codec.Decode)
type Options = {data:Data=.{}} derive(codec.Decode)
fn main(){
 match(codec.Schema[Options]()){
  Option.Some(schema)=>schema.fields.forEach(field=>match(field.defaultInput){
   Option.Some(provider)=>match(provider()){value:codec.Value=>{println(json.Render(value));println(codec.decode[Data](value))},error:codec.DecodeError=>println(error)}
   Option.None=>println("missing provider")
  })
  Option.None=>println("missing schema")
 }
 match(shape.metadata[Int|String,codec.Decode,codec.DefaultInput[Int|String]]()){
  Option.Some(provider)=>println(provider.convert("direct"))
  Option.None=>println("missing union provider")
 }
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`{"number":7,"text":{"value":"seven"}}`, `number: 7`, `value: "seven"`, `value: "direct"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
}
