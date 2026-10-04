package manifest

import "testing"

func TestCompilerVersion(t *testing.T) {
	for _, text := range []string{"0.4", "0.4.2", "v0.4.2-rc.1"} {
		version, err := CompilerVersion([]byte("module example.com/app\nbork " + text + " // compiler\nrequire example.com/lib v1.0.0\n"))
		if err != nil || version.Query == "" || version.Minimum == "" {
			t.Fatalf("%q: %+v (%v)", text, version, err)
		}
	}
	for _, text := range []string{"bork", "bork 0", "bork latest", "bork 0.4\nbork 0.5", "bork 0.4 extra", "bork 0.4.2+build"} {
		if _, err := CompilerVersion([]byte(text)); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
	version, err := CompilerVersion([]byte("module example.com/app\nfuture syntax\n"))
	if err != nil || version.Query != "" {
		t.Fatalf("unrelated syntax: %+v (%v)", version, err)
	}
}
