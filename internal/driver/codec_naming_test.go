package driver

import "testing"

func TestCodecWireNameChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"direct record helper collision", `class DecodeOnly[T] { fn decodeOnly(x: codec.Value): T | codec.DecodeError }
derive instance decoder[T]: DecodeOnly[T] {
  fn decodeOnly(x: codec.Value): T | codec.DecodeError { codec.DecodeRecord[T](x) }
}
type T={a:Int codec{name:"same"},b:Int codec{name:"same"}} derive(DecodeOnly)`, "duplicate wire names"},
		{"own alias collision", `type T={value:Int codec{aliases:["value"]}} derive(codec.Decode)`, "duplicate wire names"},
		{"other canonical collision", `type T={a:Int codec{aliases:["b"]},b:Int} derive(codec.Decode)`, "duplicate wire names"},
		{"alias collision", `type T={a:Int codec{aliases:["old"]},b:Int codec{aliases:["old"]}} derive(codec.Encode)`, "duplicate wire names"},
		{"variant alias collision", `type T=sealed{One codec{aliases:["old"]},Two codec{aliases:["old"]}} derive(codec.Decode)`, "duplicate wire names"},
		{"discriminator alias", `type T=sealed{Item{value:Int codec{aliases:["type"]}}} derive(codec.Decode)`, "collides with the codec discriminator"},
		{"empty alias", `type T={value:Int codec{aliases:[""]}} derive(codec.Decode)`, "invalid alias"},
		{"merge alias", `type T={value:Int codec{aliases:["<<"]}} derive(codec.Decode)`, "invalid alias"},
		{"yaml numeric alias", `type T=sealed{One codec{aliases:["1"]}} derive(codec.Decode)`, "invalid YAML alias"},
		{"fallback metadata", `type T=sealed{One,Other(String) codec{fallback:true}} derive(codec.Decode)`, ""},
		{"omit non option", `type T={value:Int codec{omit:codec.Omit.None}} derive(codec.Encode)`, "not round-trip safe"},
		{"omit some default", `type T={value:Option[Int]=Option.Some(1) codec{omit:codec.Omit.None}} derive(codec.Encode)`, "not round-trip safe"},
		{"omit default missing", `type T={value:Int codec{omit:codec.Omit.Default}} derive(codec.Encode)`, "not round-trip safe"},
		{"omit lazy", `type T={lazy value:Int=1 codec{omit:codec.Omit.Default}} derive(codec.Encode)`, "not round-trip safe"},
		{"omit default equality", `type T={value:(Int)=>Int=(x:Int)=>x codec{omit:codec.Omit.Default}} derive(codec.Decode)`, "not round-trip safe"},
		{"omit none or default missing", `type T={value:Option[Int] codec{omit:codec.Omit.NoneOrDefault}} derive(codec.Encode)`, "not round-trip safe"},
		{"field collision", `type T={userId:Int,userID:Int} codec{naming:codec.Naming.Snake} derive(codec.Encode)`, "duplicate wire names"},
		{"variant collision", `type T=sealed{UserId,UserID} derive(codec.Decode)`, "duplicate wire names"},
		{"empty name", `type T={value:Int codec{name:""}} derive(codec.Encode)`, "empty wire name"},
		{"merge key", `type T={value:Int codec{name:"<<"}} derive(codec.Decode)`, "YAML merge key"},
		{"discriminator rename", `type T=sealed{Item{value:Int codec{name:"type"}}} derive(codec.Encode,codec.Decode)`, "collides with the codec discriminator"},
		{"null enum", `type T=sealed{Null} derive(codec.Encode)`, "invalid YAML wire name"},
		{"numeric enum", `type T=sealed{Item codec{name:"1e3"}} derive(codec.Decode)`, "invalid YAML wire name"},
		{"bool enum", `type T=sealed{Item codec{name:"True"}} derive(codec.Encode)`, "invalid YAML wire name"},
		{"yaml11 enum", `type T=sealed{On,Off,Yes} derive(codec.Encode,codec.Decode)`, ""},
		{"mixed verbatim", `type T=sealed{Item{userName:String},Empty} derive(codec.Encode,codec.Decode)`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPreludeSource(t, "import \"bork/codec\"\nuse codec.Defaults\n"+tc.source+"\nfn main(){}", tc.want)
		})
	}
}
