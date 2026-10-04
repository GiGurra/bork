package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// CI provisions an old launcher plus a cached supported SDK for this test.
func TestGoContextActualSwitch(t *testing.T) {
	launcher := os.Getenv("BORK_TEST_GO_LAUNCHER")
	if launcher == "" {
		t.Skip("set BORK_TEST_GO_LAUNCHER to a Go 1.21 launcher with Go 1.26.0 cached")
	}
	t.Setenv("PATH", filepath.Dir(launcher)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOROOT", "")
	t.Setenv("GOENV", "off")
	t.Setenv("GOTOOLCHAIN", "auto")
	ctx := captureGoContext()
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	wantTool := filepath.Join(ctx.values["GOROOT"], "bin", name)
	if ctx.tool != wantTool || ctx.tool == launcher {
		t.Fatalf("selected %s, root %s, launcher %s", ctx.tool, ctx.values["GOROOT"], launcher)
	}
	selectedDigest, err := goToolDigest(wantTool)
	if err != nil {
		t.Fatal(err)
	}
	launcherDigest, err := goToolDigest(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.toolDigest != selectedDigest || ctx.toolDigest == launcherDigest {
		t.Fatal("context identity used the launcher instead of selected SDK")
	}

	root := t.TempDir()
	for name, text := range map[string]string{ModFile: "module example.com/switch\nunsafe \"example.com/switch\"\n", "main.bork": `fn trimmed(): String unsafe go {
import "strings"
return strings.TrimSpace(" hi ")
}
fn main() uses io { println(trimmed()) }
`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	session := NewSession()
	if _, err := session.Analyze(root, nil); err != nil {
		t.Fatal(err)
	}
	if session.editorContext.err != nil {
		t.Fatal(session.editorContext.err)
	}
	if session.editorContext.tool != wantTool || session.editorContext.values["GOTOOLCHAIN"] != "local" {
		t.Fatalf("offline editor did not freeze selected SDK: %+v", session.editorContext.values)
	}
	if _, err := dependencyGoWithSettings(root, []string{"GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"}, "env", "GOVERSION"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTOOLCHAIN", "local")
	local := captureGoContext()
	if local.err == nil || !strings.Contains(local.err.Error(), "GOTOOLCHAIN=local") {
		t.Fatalf("explicit local: %v", local.err)
	}
}
