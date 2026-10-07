package driver

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLIParentDefaultSelectedDecoder(t *testing.T) {
	t.Parallel()
	source := `import "bork/cli"
import "bork/codec"
import "bork/json"
use codec.Defaults
type Token={text:String}
instance tokenDecode:codec.Decode[Token]{
 metadata codec.FieldSchema=codec.FieldSchema{kind:"string",optional:false}
 metadata codec.DefaultInput[Token]=codec.Input[Token](value=>codec.Value.String{value:"encoded:"+value.text})
 fn decode(input:codec.Value):Token|codec.DecodeError{
  match(input){
   codec.Value.String{value}=>if(value.startsWith("encoded:")){Token{text:value.replaceAll("encoded:","")}}else{codec.DecodeError{path:"",message:"token needs encoded prefix"}}
   _=>codec.DecodeError{path:"",message:"expected token string"}
  }
 }
}
type Raw={value:Token codec {aliases:["oldValue"]}}derive(codec.Decode)
type RawText={value:String="leaf"}derive(codec.Decode)
type Custom={value:Token}
instance customDecode:codec.Decode[Custom]{
 metadata Option[codec.RecordSchema]=codec.Schema[Raw]()
 metadata codec.DefaultInput[Custom]=codec.Input[Custom](value=>codec.Value.Object{fields:[.{name:"oldValue",value:.String{value:"encoded:"+value.value.text}},.{name:"marker",value:.String{value:"selected"}}]})
 fn decode(input:codec.Value):Custom|codec.DecodeError{
  match(json.Field(input,"marker")){
   Option.Some(codec.Value.String{value:"selected"})=>match(codec.decode[Raw](input)){value:Raw=>Custom{value:value.value},error:codec.DecodeError=>error}
   _=>codec.DecodeError{path:".marker",message:"selected record decoder needs marker"}
  }
 }
}
type WithCustom={custom:Custom=.{value:.{text:"default"}}}derive(codec.Decode)
type Unsupported={text:String}
instance unsupported:codec.Decode[Unsupported]{
 metadata codec.FieldSchema=codec.FieldSchema{kind:"string",optional:false}
 fn decode(input:codec.Value):Unsupported|codec.DecodeError{Unsupported{text:"decoded"}}
}
type Payload=sealed{Named{token:Unsupported},Positional(Unsupported)}derive(codec.Decode)
type DeepMissing={named:Payload}derive(codec.Decode)
type WithDeepMissing={db:DeepMissing=.{named:.Named{token:.{text:"default"}}}}derive(codec.Decode)
type WithPositionalMissing={db:DeepMissing=.{named:.Positional(.{text:"default"})}}derive(codec.Decode)
type TupleMissing={tuple:(Unsupported,String)}derive(codec.Decode)
type WithTupleMissing={db:TupleMissing=.{tuple:(.{text:"default"},"two")}}derive(codec.Decode)
type ListMissing={values:List[Unsupported]}derive(codec.Decode)
type WithListMissing={db:ListMissing=.{values:[.{text:"default"}]}}derive(codec.Decode)
type MapMissing={values:Map[String,Unsupported]}derive(codec.Decode)
type WithMapMissing={db:MapMissing=.{values:{"key":.{text:"default"}}}}derive(codec.Decode)
type MissingLeaf={value:Unsupported}derive(codec.Decode)
type WithMissing={db:MissingLeaf=.{value:.{text:"default"}}}derive(codec.Decode)
type Bad={value:String="leaf"}
instance bad:codec.Decode[Bad]{
 metadata Option[codec.RecordSchema]=codec.Schema[RawText]()
 metadata codec.DefaultInput[Bad]=codec.Input[Bad](value=>codec.Value.String{value:"invalid"})
 fn decode(input:codec.Value):Bad|codec.DecodeError{Bad{}}
}
type WithBad={db:Bad=.{}}derive(codec.Decode)
type NoProvider={value:String="leaf"}
instance noProvider:codec.Decode[NoProvider]{
 metadata Option[codec.RecordSchema]=codec.Schema[RawText]()
 fn decode(input:codec.Value):NoProvider|codec.DecodeError{match(codec.decode[RawText](input)){value:RawText=>NoProvider{value:value.value},error:codec.DecodeError=>error}}
}
type WithNoProvider={db:NoProvider=.{}}derive(codec.Decode)
fn main(){
 println(cli.Parse[WithCustom]("app","Custom",[]))
 println(cli.Parse[WithCustom]("app","Custom",["--custom-value","encoded:cli"]))
 println(cli.Parse[WithCustom]("app","Custom",["--custom-value","invalid"]))
 println(cli.Parse[WithMissing]("app","Missing",["--help"],configFiles:["missing.json"]))
 println(cli.Parse[WithDeepMissing]("app","Missing",["--help"]))
 println(cli.Parse[WithPositionalMissing]("app","Missing",["--help"]))
 println(cli.Parse[WithTupleMissing]("app","Missing",["--help"]))
 println(cli.Parse[WithListMissing]("app","Missing",["--help"]))
 println(cli.Parse[WithMapMissing]("app","Missing",["--help"]))
 println(cli.Parse[WithBad]("app","Bad",["--help"]))
 println(cli.Parse[WithNoProvider]("app","Missing",["--help"]))
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`text: "default"`, `text: "cli"`, `token needs encoded prefix`, `path: "db.value"`, `path: "db.named.token"`, `path: "db.named.values[0]"`, `path: "db.tuple[0]"`, `path: "db.values[0]"`, `path: "db.values.key"`, `codec.DefaultInput`, `codec.Input`, `flattened parent default input must be an object or null`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
	for _, absent := range []string{"missing.json", "selected record decoder needs marker", "internal error"} {
		if strings.Contains(string(out), absent) {
			t.Fatalf("output %q includes %q", out, absent)
		}
	}
}
