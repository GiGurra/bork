// Command bork is the bork compiler and toolchain.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/spf13/cobra"
)

type pathParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
}

type diagnosticParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	JSON bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
}

type fmtParams struct {
	Paths []string `positional:"true" optional:"true" descr:".bork files or directories (default: .); directories are visited recursively"`
	Check bool     `optional:"true" descr:"report files needing formatting without writing them"`
}

type buildParams struct {
	JSON   bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
	Path   string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Output string `short:"o" optional:"true" descr:"output executable (default: the file or directory name)"`
}

// testParams pins its short flags (no automatic ones, so they don't
// shift as flags are added); --auto-properties has none on purpose.
type testParams struct {
	JSON           bool   `short:"j" optional:"true" descr:"report diagnostics as JSON Lines"`
	Path           string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Update         bool   `short:"u" optional:"true" descr:"write the snapshots assertSnapshot finds missing or different, instead of failing"`
	AutoProperties bool   `optional:"true" descr:"also property-test the functions whose promises are trusted (unsafe go, or trust), on generated arguments"`
	Seed           int64  `short:"s" optional:"true" descr:"the seed of every property test (default: one from the test's name)"`
	Cases          int    `short:"c" optional:"true" descr:"how many cases each property test runs (default 100)"`
	Hermetic       bool   `optional:"true" descr:"fail, without running them, the tests that can reach the network (net in Go code) with no mock in force"`
	Parallel       int    `short:"p" optional:"true" descr:"how many tests run at a time, each on goroutines of its own (default 1); the report keeps their order"`
}

type runParams struct {
	Path string   `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Args []string `positional:"true" optional:"true" descr:"arguments passed to the program (put them after --)"`
}

type describeParams struct {
	Position string `positional:"true" descr:"source position as file:line:column (one-based byte columns)"`
	Where    string `optional:"true" descr:"ask whether this where clause is proven for the selected value"`
	JSON     bool   `optional:"true" descr:"write the compiler description as JSON"`
}

type depsParams struct {
	Path string `optional:"true" default:"." descr:"directory in the bork module"`
}

type depsGetParams struct {
	Packages []string `positional:"true" descr:"Go package or module queries (path@version, path@latest, or path@none)"`
	Path     string   `optional:"true" default:"." descr:"directory in the bork module"`
}

func printDescription(result *describe.Result) {
	if result.Expression != "" {
		fmt.Println("expression:", result.Expression)
	}
	fmt.Println("type:", result.Type)
	if result.ProviderBundle != nil {
		for _, e := range result.ProviderBundle.Entries {
			fmt.Printf("provider %s: %s -> %s (uses %s)\n", e.Name, e.Function, e.Product, e.Effects)
			if len(e.Dependencies) > 0 {
				fmt.Println("  dependencies:", strings.Join(e.Dependencies, ", "))
			}
			if len(e.Failures) > 0 {
				fmt.Println("  failures:", strings.Join(e.Failures, " | "))
			}
			if len(e.Needs) > 0 {
				fmt.Println("  needs:", strings.Join(e.Needs, ", "))
			}
			if e.Replaced {
				fmt.Println("  replaced for this assembly")
			}
		}
	}
	if result.Assembly != nil {
		fmt.Println(result.Assembly.Tree)
		fmt.Println("invocation order:", result.Assembly.Order)
		fmt.Println("effects:", result.Assembly.Effects)
	}
	if result.Definition != nil {
		fmt.Println("defined at:", result.Definition)
	}
	if len(result.BelongsTo) > 0 {
		fmt.Println("belongs to:", strings.Join(result.BelongsTo, ", "))
	}
	if result.Callable != nil {
		fmt.Println("named arguments:", callableParameters(result.Callable), "(parameter names are API)")
		if len(result.Callable.Needs) > 0 {
			fmt.Println("needs:", strings.Join(result.Callable.Needs, " + "))
		}
		for _, req := range result.Callable.Requires {
			fmt.Println("requires:", req)
		}
	}
	for _, method := range result.Methods {
		if method.Ambiguity != "" {
			fmt.Println("method:", method.Ambiguity)
		} else {
			fmt.Printf("method: %s: %s (%s)", method.Name, method.Type, method.Definition)
			if method.Callable != nil {
				fmt.Printf(" named arguments (%s)", callableParameters(method.Callable))
				if len(method.Callable.Needs) > 0 {
					fmt.Printf(" needs %s", strings.Join(method.Callable.Needs, " + "))
				}
			}
			if len(method.Requires) > 0 {
				fmt.Printf(" requires %s", strings.Join(method.Requires, "; "))
			}
			fmt.Println()
		}
	}
	for _, fact := range result.Facts {
		if fact.Path == "" {
			fmt.Println("known where:", fact.Constraint)
		} else {
			fmt.Printf("known where: %s: %s\n", fact.Path, fact.Constraint)
		}
	}
	if result.Proof != nil {
		if result.Proof.Proven {
			fmt.Printf("proven: %s\n", result.Proof.Where)
		} else {
			fmt.Printf("not proven: %s\n%s\n", result.Proof.Where, result.Proof.Reason)
		}
	}
}

func callableParameters(callable *check.CallableDescription) string {
	var parts []string
	for _, p := range callable.Parameters {
		if p.Receiver {
			continue
		}
		text := p.Name + ": " + p.Type
		if p.Default != "" {
			text += " = " + p.Default
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ", ")
}

// completeBorkPaths completes .bork files and directories for the
// positional path argument.
func completeBorkPaths(_ any, _ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"bork"}, cobra.ShellCompDirectiveFilterFileExt
}

// fail reports err and exits. Compile errors are printed as-is
// (file:line:col: message), other errors with a "bork:" prefix.
func fail(err error) {
	var de *driver.DiagError
	if errors.As(err, &de) {
		fmt.Fprintln(os.Stderr, de.Error())
	} else {
		fmt.Fprintln(os.Stderr, "bork:", err)
	}
	os.Exit(1)
}

// failDiagnostics keeps diagnostic JSON separate from test program output.
func failDiagnostics(err error, asJSON bool, output io.Writer) {
	if !asJSON {
		fail(err)
	}
	var de *driver.DiagError
	var diags *diag.List
	if errors.As(err, &de) {
		diags = de.Diags
	} else {
		diags = &diag.List{}
		diags.AddCode(diag.Pos{}, "tool.error", "%s", err)
	}
	if writeErr := diags.WriteJSON(output); writeErr != nil {
		fmt.Fprintln(os.Stderr, "bork:", writeErr)
	}
	os.Exit(1)
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func main() {
	boa.CmdT[boa.NoParams]{
		Use:   "bork",
		Short: "the bork compiler: a pragmatic backend language of guarantees",
		SubCmds: boa.SubCmds(
			boa.CmdT[boa.NoParams]{
				Use:   "deps",
				Short: "manage pinned user Go dependencies beside bork.mod",
				SubCmds: boa.SubCmds(
					boa.CmdT[depsParams]{
						Use: "init", Short: "create empty go-deps.mod and go-deps.sum manifests",
						RunFunc: func(p *depsParams, _ *cobra.Command, args []string) {
							if err := driver.Deps(p.Path, "init", args); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[depsGetParams]{
						Use: "get", Short: "add, update, or remove Go package dependencies",
						RunFunc: func(p *depsGetParams, _ *cobra.Command, _ []string) {
							if err := driver.Deps(p.Path, "get", p.Packages); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[depsParams]{
						Use: "download", Short: "download pinned dependencies and fill in checksums",
						RunFunc: func(p *depsParams, _ *cobra.Command, args []string) {
							if err := driver.Deps(p.Path, "download", args); err != nil {
								fail(err)
							}
						},
					},
				),
			},
			boa.CmdT[fmtParams]{
				Use:   "fmt",
				Short: "format bork source files in place",
				RunFunc: func(p *fmtParams, _ *cobra.Command, _ []string) {
					changed, err := borkformat.Files(p.Paths, p.Check)
					for _, path := range changed {
						fmt.Println(path)
					}
					if err != nil {
						fail(err)
					}
					if p.Check && len(changed) > 0 {
						os.Exit(1)
					}
				},
			},
			boa.CmdT[describeParams]{
				Use:   "describe",
				Short: "query the compiler for types, definitions, methods, and proven facts",
				RunFunc: func(p *describeParams, _ *cobra.Command, _ []string) {
					result, err := driver.Describe(p.Position, p.Where)
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stderr)
					}
					if p.JSON {
						if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
							fail(err)
						}
					} else {
						printDescription(result)
					}
				},
			},
			boa.CmdT[buildParams]{
				Use:   "build",
				Short: "compile a bork program into an executable",
				ValidArgsFunc: func(p *buildParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *buildParams, _ *cobra.Command, _ []string) {
					out := p.Output
					if out == "" {
						out = driver.DefaultOutput(p.Path)
					}
					if err := driver.Build(p.Path, out); err != nil {
						failDiagnostics(err, p.JSON, os.Stderr)
					}
				},
			},
			boa.CmdT[runParams]{
				Use:   "run",
				Short: "compile and run a bork program",
				ValidArgsFunc: func(p *runParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *runParams, _ *cobra.Command, _ []string) {
					code, err := driver.Run(p.Path, p.Args)
					if err != nil {
						fail(err)
					}
					os.Exit(code)
				},
			},
			boa.CmdT[testParams]{
				Use:         "test",
				Short:       "run a bork program's tests, checking trusted facts as they run",
				ParamEnrich: boa.ParamEnricherCombine(boa.ParamEnricherName, boa.ParamEnricherBool),
				ValidArgsFunc: func(p *testParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *testParams, _ *cobra.Command, _ []string) {
					if p.Cases < 0 {
						fail(errors.New("--cases must be positive"))
					}
					if p.Parallel < 0 {
						fail(errors.New("--parallel must be positive"))
					}
					code, err := driver.Test(p.Path, os.Stdout, driver.TestOptions{Update: p.Update, AutoProperties: p.AutoProperties, Seed: p.Seed, Cases: p.Cases, Parallel: p.Parallel, Hermetic: p.Hermetic})
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stderr)
					}
					os.Exit(code)
				},
			},
			boa.CmdT[diagnosticParams]{
				Use:   "check",
				Short: "type-check a bork program without building it",
				ValidArgsFunc: func(p *diagnosticParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *diagnosticParams, _ *cobra.Command, _ []string) {
					_, info, err := driver.Check(p.Path)
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stdout)
					}
					warnings := check.DebugWarnings(info)
					if p.JSON {
						if err := warnings.WriteJSON(os.Stdout); err != nil {
							fail(err)
						}
					} else {
						for _, warning := range warnings.Sorted() {
							fmt.Println(warning)
						}
					}
				},
			},
			boa.CmdT[pathParams]{
				Use:   "emit",
				Short: "print the Go code generated for a bork program",
				ValidArgsFunc: func(p *pathParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *pathParams, _ *cobra.Command, _ []string) {
					src, err := driver.Emit(p.Path)
					if err != nil {
						fail(err)
					}
					_, _ = os.Stdout.Write(src)
				},
			},
			boa.CmdT[boa.NoParams]{
				Use:   "version",
				Short: "print the bork version",
				RunFunc: func(_ *boa.NoParams, _ *cobra.Command, _ []string) {
					fmt.Println("bork", version())
				},
			},
		),
	}.Run()
}
