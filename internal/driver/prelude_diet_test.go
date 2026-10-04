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
		{"json codecs", `import "bork/json"
type Config = { count: Int } derive (Decode, Encode)
fn main() { println(json.Decode[Config](json.Encode(Config { count: 2 })), json.Render(Json.Null), json.Parse("null")) }`, ""},
		{"process", `import "bork/process"
fn main() { println(process.Args()); if (false) { process.Exit(1) } }`, ""},
		{"cancellable sleep", `import "bork/time"
fn main() { scope s { cancel(s); println(time.Sleep(s, time.Nanoseconds(0))) } }`, ""},
		{"rune methods", `fn main() { println('1'.isDigit(), 'a'.isLetter(), ' '.isSpace(), 'A'.isUpper(), 'a'.isLower(), toString('a')) }`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPreludeSource(t, tc.source, tc.want) })
	}
	for _, name := range []string{"args", "exit", "sleep", "parseJson", "renderJson", "decodeJson", "encodeJson", "jsonKind", "decodeMismatch", "atPath", "decodeItems", "decodeFields", "jsonFieldPut", "jsonFieldsOf", "isDigit", "isLetter", "isSpace", "isUpper", "isLower"} {
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
