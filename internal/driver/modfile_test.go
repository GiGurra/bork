package driver

import (
	"strings"
	"testing"
)

func TestParseModFile(t *testing.T) {
	t.Parallel()
	mod, err := parseModFile("// shop\nmodule example.com/shop // the shop\n\nunsafe \"example.com/shop/ffi\" // bindings\nunsafe \"example.com/shop\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if mod.path != "example.com/shop" || !mod.unsafe["example.com/shop/ffi"] || !mod.unsafe["example.com/shop"] || len(mod.unsafe) != 2 {
		t.Fatalf("unexpected module: %+v", mod)
	}
	for text, want := range map[string]string{
		"":                                     "expected `module <path>`",
		"unsafe \"a\"\nmodule m\n":             "expected `module <path>`, found",
		"module m\nunsafe a\n":                 "expected `unsafe \"<package path>\"`",
		"module m\nrequire example.com/x v1\n": "expected `unsafe \"<package path>\"`",
		"module bork/app\n":                    "reserved for the standard library",
	} {
		if _, err := parseModFile(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", text, err, want)
		}
	}
}
