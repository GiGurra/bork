package driver

import "testing"

func TestCodecWireNameChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"direct record helper collision", `class DecodeOnly[T] { fn decodeOnly(x: codec.Value): T | codec.DecodeError }
derive instance decoder[T]: DecodeOnly[T] {
  fn decodeOnly(x: codec.Value): T | codec.DecodeError { codec.DecodeRecord[T](x) }
}
type T={a:Int codec{name:"same"},b:Int codec{name:"same"}} derive(DecodeOnly)`, "duplicate wire names"},
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
