// genstdvalidators derives compiler-owned artifacts from embedded standard code.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir, err := os.MkdirTemp("", "bork-artifact-generation-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var source strings.Builder
	number := 0
	for _, pkg := range std.Packages() {
		paths, contents, _ := std.Sources(pkg)
		diags := &diag.List{}
		files := syntax.ParseFiles(paths, contents, false, diags)
		for _, file := range files {
			for _, instance := range file.Instances {
				if instance.Class != "InterpolationValidator" || len(instance.TypeParams) != 0 || len(instance.Type.Args) != 0 {
					continue
				}
				fmt.Fprintf(&source, "import artifact%d %q\nfn touch%d(value:artifact%d.%s){}\n", number, pkg, number, number, instance.Type.Name)
				number++
			}
		}
	}
	source.WriteString("fn main(){}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source.String()), 0600); err != nil {
		return err
	}
	files, info, err := driver.Check(dir)
	if err != nil {
		return err
	}
	runtimePath := "internal/stdvalidators/runtime.go"
	if len(os.Args) > 1 {
		runtimePath = os.Args[1]
	}
	runtimeSource, err := os.ReadFile(runtimePath)
	if err != nil {
		return err
	}
	output, err := gen.StdValidatorArtifact(files, info, runtimeSource)
	if err != nil {
		return err
	}
	path := "internal/stdvalidators/generated.go"
	if len(os.Args) > 2 {
		path = os.Args[2]
	}
	return os.WriteFile(path, output, 0644)
}
