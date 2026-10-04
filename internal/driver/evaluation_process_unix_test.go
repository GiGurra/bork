//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

// Run the test executable as an evaluator descendant. If group cancellation
// fails, the child leaves observable evidence and exits on its own, so even a
// failing regression does not leave an indefinitely running process.
func TestEvaluationDescendantHelper(t *testing.T) {
	marker := os.Getenv("BORK_EVALUATION_CHILD_MARKER")
	if marker == "" {
		t.Skip("subprocess helper")
	}
	if err := os.WriteFile(marker+".ready", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestComptimeTimeoutKillsDescendants(t *testing.T) {
	t.Parallel()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"recipe", "proof"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			marker := filepath.Join(dir, "child-survived")
			body := fmt.Sprintf(`import "os"
import "os/exec"
cmd:=exec.Command(%s,"-test.run=^TestEvaluationDescendantHelper$")
cmd.Env=append(os.Environ(),"BORK_EVALUATION_CHILD_MARKER="+%s)
cmd.Stdout=os.Stdout
cmd.Stderr=os.Stderr
if err:=cmd.Start();err!=nil{panic(err)}
for{}`, strconv.Quote(testExecutable), strconv.Quote(marker))
			source := "fn spin():Int unsafe go{" + body + "}\nfn main(){println(comptime{spin()})}"
			want := "evaluation exceeded 1s"
			if mode == "proof" {
				source = "pred spin(n:Int) unsafe go{" + body + "}\nfn require(n:Int where spin):Int{n}\nfn main(){println(comptime{require(1)})}"
				want = "predicate evaluation exceeded 1s"
			}
			for name, contents := range map[string]string{
				ModFile:     "module example.com/cleanup\nunsafe \"example.com/cleanup\"\n",
				"main.bork": source,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			loaded, module, err := loadCompilationInputs(dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			context := captureGoContext()
			context.evalLimit = time.Second
			info := check.ProgramObserved(loaded.Files, loaded.Root, loaded.Diags, goPackages{files: loaded.Files, module: module, context: context}, nil)
			if loaded.Diags.Len() != 0 {
				t.Fatal(&DiagError{Diags: loaded.Diags})
			}
			// Exercise the executor directly: spawning a child is an obvious
			// io effect and is rejected by normal comptime effect checking.
			// Cancellation must still contain descendants of foreign code
			// whose declared purity was trusted by that check.
			evaluateComptimes(loaded.Files, info, loaded.Diags, module, context, nil)
			if loaded.Diags.Len() == 0 || !strings.Contains((&DiagError{Diags: loaded.Diags}).Error(), want) {
				t.Fatalf("expected timeout %q, got %v", want, &DiagError{Diags: loaded.Diags})
			}
			if _, err := os.Stat(marker + ".ready"); err != nil {
				t.Fatalf("descendant did not start before timeout: %v", err)
			}
			// The child's write deadline is at most two seconds after its
			// ready marker. Wait beyond it to distinguish a killed child from
			// one merely delayed by the canceled evaluator.
			time.Sleep(2200 * time.Millisecond)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("descendant survived evaluator timeout: %v", err)
			}
		})
	}
}
