package gen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArtifactGuardsBoundNonterminatingHelper(t *testing.T) {
	runtimeSource, err := os.ReadFile("../stdvalidators/runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`package main
func validateInterpolation(){ helper() }
func helper(){ nested:=func(){for{}};nested() }
func recurse(){ recurse() }
`)
	output, err := guardArtifact(source, runtimeSource)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	testSource := `package stdvalidators
import("testing";"time";"fmt";"strings")
func TestDeadline(t *testing.T){
 nativeDeadline=time.Now().Add(time.Millisecond)
 nativeSteps=0;nativeDepth=0
 start:=time.Now()
 defer func(){v:=recover();if v==nil||!strings.Contains(fmt.Sprint(v),"deadline"){t.Fatalf("deadline guard: %v",v)};if time.Since(start)>time.Second{t.Fatal("deadline late")}}()
 validateInterpolation()
}
func TestDepth(t *testing.T){
 nativeDeadline=time.Now().Add(time.Second);nativeSteps=0;nativeDepth=0
 defer func(){v:=recover();if v==nil||!strings.Contains(fmt.Sprint(v),"recursion"){t.Fatalf("depth guard: %v",v)}}()
 recurse()
}
`
	for name, content := range map[string][]byte{"go.mod": []byte("module artifactfixture\ngo 1.26\n"), "runtime.go": runtimeSource, "generated.go": output, "artifact_test.go": []byte(testSource)} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-run", "^Test(Deadline|Depth)$", "-count=1")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("guard fixture: %v\n%s", err, out)
	}
}

func TestArtifactRejectsBlockingOperation(t *testing.T) {
	runtimeSource, err := os.ReadFile("../stdvalidators/runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	_, err = guardArtifact([]byte(`package main
func validateInterpolation(){select{}}
`), runtimeSource)
	if err == nil || !strings.Contains(err.Error(), "unsupported blocking") {
		t.Fatalf("blocking artifact accepted: %v", err)
	}
}

func TestArtifactRejectsUnboundedNativeOperations(t *testing.T) {
	runtimeSource, err := os.ReadFile("../stdvalidators/runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`_ = new([1<<40]byte)`, `s:="x";for{s+=s}`, `_ = [1<<40]byte{}`, `var x [1<<40]byte;_ = x`, `m:=map[string]any{};m["self"]=m;_ = fmt.Sprintf("%v",m)`, `var x any="x";_ = fmt.Sprintf("%v",x)`, `_ = strings.Repeat("x",1<<40)`, `_ = fmt.Sprintf("%1000000s", "x")`, `f:="%s";_ = fmt.Sprintf(f,"x")`, `s:="x";_ = []byte(s)`, `xs:=[]byte{1};_ = string(xs)`, `f:=fmt.Sprintf;_ = f("%1000000s","x")`, `f:=strings.ReplaceAll;_ = f("x","x","x")`, `type S string;s:=S("x");for{s+=s}`, `type B []byte;s:="x";_ = B(s)`, `type B []byte;xs:=B{1};_ = string(xs)`, `println("x")`, `print("x")`} {
		source := []byte("package main\nimport(\"fmt\";\"strings\")\nvar _=fmt.Sprintf;var _=strings.Index\nfunc validateInterpolation(){" + body + "}\n")
		if _, err := guardArtifact(source, runtimeSource, "validateInterpolation"); err == nil {
			t.Errorf("unbounded native operation accepted: %s", body)
		}
	}
}

func TestArtifactRejectsUnknownGeneratedHelper(t *testing.T) {
	runtimeSource, err := os.ReadFile("../stdvalidators/runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`package main
func _showList(values []string)string {var result string;for _,value:=range values{result+=value};return result}
func validator(){_ = _showList([]string{"hello"})}
`)
	if _, err := guardArtifact(source, runtimeSource, "validator"); err == nil || !strings.Contains(err.Error(), "unsupported generated artifact helper") {
		t.Fatalf("unknown generated formatter accepted: %v", err)
	}
}
