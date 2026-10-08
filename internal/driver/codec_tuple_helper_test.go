package driver

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestCodecSourceTupleDeriveHelper(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `import "bork/codec"
import "bork/json"
use codec.Defaults
fn pair[A: codec.Encode, B: codec.Encode](left: A, right: B): String | json.JsonError { json.Encode((left, right)) }
encoded: codec.Value = comptime { codec.encode((1, "compile")) }
fn main() {
 println(pair(2, "runtime"))
 println(json.Render(encoded))
 println(json.Encode(((3,), ("nested", true))))
}`)
	loaded, module, err := loadCompilationInputs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Inject an arbitrary runtime helper into the source template, independently
	// of the library's current wire-name validation branches.
	helper := syntax.Parse("tuple-helper.bork", []byte(`derive fn tupleIdentity[T](value: T): T { value }
fn probe[T](x: T) { _ = tupleIdentity[T](x) }`), loaded.Diags)
	helper.Package = "bork/codec"
	inserted := false
	for _, file := range loaded.Files {
		if file.Package != helper.Package {
			continue
		}
		for _, template := range file.Templates {
			if template.Name != "encodeShape" {
				continue
			}
			for _, method := range template.Methods {
				method.Body.Stmts = append(helper.Funcs[0].Body.Stmts, method.Body.Stmts...)
				inserted = true
			}
		}
	}
	if !inserted {
		t.Fatal("missing codec Encode template")
	}
	helper.Funcs = nil
	loaded.Files = append(loaded.Files, helper)
	program, err := checkLoadedProgramObserved(loaded, module, captureGoContext(), captureEmbedsSnapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	source, err := gen.Package(program.files, program.info)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "tuple-helper")
	if err := buildGoWithContext(program.files, source, exe, program.module, program.context, program.info.Embeds...); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(output) != "[2,\"runtime\"]\n[1,\"compile\"]\n[[3],[\"nested\",true]]\n" {
		t.Fatalf("tuple helper: %s, %v", output, err)
	}
}
