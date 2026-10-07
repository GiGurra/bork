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
