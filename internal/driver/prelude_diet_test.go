package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreludeDiet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"json codecs", `import codec "bork/codec"

import "bork/json"
use codec.Defaults
type Config = { count: Int } derive (codec.Decode, codec.Encode)
fn main() { println(json.Decode[Config](match (json.Encode(Config { count: 2 })) { text: String => text; _: json.JsonError => "" }), json.Render(codec.Value.Null), json.Parse("null")) }`, ""},
		{"byte conversion methods", `import "bork/encoding"
fn main() { println([].toBytes(), [toByte(65)].toBytes().toList(), encoding.ParseUtf8(encoding.Utf8("hé"))) }`, ""},
		{"user byte functions", `fn bytes(n: Int): Int { n }
fn utf8Bytes(n: Int): Int { n }
fn utf8String(n: Int): Int { n }
fn main() { println(bytes(1), utf8Bytes(2), utf8String(3)) }`, ""},
		{"process", `import "bork/process"
fn main() { println(process.Args()); if (false) { process.ExitNow(1) } }`, ""},
		{"cancellable sleep", `import "bork/time"
fn main() { scope s { cancel(s); println(time.Sleep(s, time.Nanoseconds(0))) } }`, ""},
		{"rune methods", `fn main() { println('1'.isDigit(), 'a'.isLetter(), ' '.isSpace(), 'A'.isUpper(), 'a'.isLower(), toString('a')) }`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPreludeSource(t, tc.source, tc.want) })
	}
	for _, name := range []string{"bytes", "utf8Bytes", "utf8String", "args", "exit", "sleep", "parseJson", "renderJson", "decodeJson", "encodeJson", "jsonKind", "decodeMismatch", "atPath", "decodeItems", "decodeFields", "jsonFieldPut", "jsonFieldsOf", "isDigit", "isLetter", "isSpace", "isUpper", "isLower"} {
		t.Run("removed "+name, func(t *testing.T) {
			checkPreludeSource(t, "fn main() { println("+name+") }", "undefined")
		})
	}
	for _, name := range []string{"_jsonKind", "_decodeMismatch", "_atPath", "_decodeItems", "_decodeFields", "_jsonFieldPut", "_jsonFieldsOf"} {
		t.Run("reserved "+name, func(t *testing.T) {
			checkPreludeSource(t, "fn main() { println("+name+") }", "reserved for the compiler")
		})
	}
}

func checkPreludeSource(t *testing.T, source, want string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(dir)
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
	} else if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q; got %v", want, err)
	}
}
