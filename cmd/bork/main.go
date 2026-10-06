// Command bork is the bork compiler and toolchain.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/describe"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/driver"
	borkformat "github.com/GiGurra/bork/internal/format"
	"github.com/GiGurra/bork/internal/lsp"
	"github.com/GiGurra/bork/internal/project"
	"github.com/GiGurra/bork/internal/toolchain"
	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
)

type newParams struct {
	Name     string `positional:"true" descr:"new project directory"`
	Template string `optional:"true" default:"default" descr:"project template: default, cli, http, or lib"`
	Module   string `optional:"true" descr:"module import path (default: example.com/<directory name>)"`
}

type cleanParams struct {
	All bool `optional:"true" descr:"remove artifacts from all compiler and staging versions"`
}

type pathParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
}

type pathJSONParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file or package directory"`
	JSON bool   `short:"j" optional:"true" descr:"report diagnostics as JSON Lines"`
}

type checkParams struct {
	Path  string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	JSON  bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
	Watch bool   `optional:"true" descr:"keep checking when tracked inputs change (SIGHUP forces a check on Unix)"`
}

type fmtParams struct {
	Paths []string `positional:"true" optional:"true" descr:".bork files or directories (default: .); directories are visited recursively"`
	Check bool     `optional:"true" descr:"report files needing formatting without writing them"`
}

type installParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	JSON bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
}

type buildParams struct {
	Fast    bool   `optional:"true" descr:"accept reuse with untracked external build inputs"`
	Rebuild bool   `optional:"true" descr:"recheck all build inputs and rebuild"`
	JSON    bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
	Path    string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Output  string `short:"o" optional:"true" descr:"output executable (default: the file or directory name)"`
}

type debugBuildParams struct {
	JSON   bool   `optional:"true" descr:"report diagnostics as JSON Lines"`
	Path   string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Output string `short:"o" optional:"true" descr:"output executable (default: the file or directory name)"`
}

type debugDAPParams struct {
	Listen string `optional:"true" default:"127.0.0.1:0" descr:"loopback DAP listener (port 0 selects a free port)"`
	Delve  string `optional:"true" descr:"path to an optional debugger executable"`
}

// testParams pins its short flags (no automatic ones, so they don't
// shift as flags are added); --auto-properties has none on purpose.
type testParams struct {
	JSON           bool   `short:"j" optional:"true" descr:"report test results as JSON Lines; diagnostics go to stderr"`
	Filter         string `optional:"true" descr:"run tests with this exact declaration name"`
	Path           string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Update         bool   `short:"u" optional:"true" descr:"write the snapshots assertSnapshot finds missing or different, instead of failing"`
	AutoProperties bool   `optional:"true" descr:"also property-test the functions whose promises are trusted (unsafe go, or trust), on generated arguments"`
	Seed           int64  `short:"s" optional:"true" descr:"the seed of every property test (default: one from the test's name)"`
	Cases          int    `short:"c" optional:"true" descr:"how many cases each property test runs (default 100)"`
	Hermetic       bool   `optional:"true" descr:"fail, without running them, the tests that can reach the network (net in Go code) with no mock in force"`
	Parallel       int    `short:"p" optional:"true" descr:"how many tests run at a time, each on goroutines of its own (default 1); the report keeps their order"`
}

type runParams struct {
	Fast    bool     `optional:"true" descr:"accept reuse with untracked external build inputs"`
	Rebuild bool     `optional:"true" descr:"recheck all build inputs and rebuild"`
	Path    string   `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Args    []string `positional:"true" optional:"true" descr:"arguments passed to the program (put them after --)"`
}

type describeParams struct {
	Position string `positional:"true" descr:"source position as file:line:column (one-based byte columns)"`
	Where    string `optional:"true" descr:"ask whether this where clause is proven for the selected value"`
	JSON     bool   `optional:"true" descr:"write the compiler description as JSON"`
}

type docParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"package directory, source file, standard package or pinned library package"`
	All  bool   `optional:"true" descr:"document every package below the target in its module"`
	HTML bool   `optional:"true" descr:"write one standalone HTML page instead of Markdown"`
}

type depsParams struct {
	Path string `optional:"true" default:"." descr:"directory in the bork module"`
}

type depsGetParams struct {
	Packages []string `positional:"true" descr:"Go package or Go/Bork module queries (path@version, path@latest, or path@none)"`
	Path     string   `optional:"true" default:"." descr:"directory in the bork module"`
}

func printDescription(result *describe.Result) {
	if result.Expression != "" {
		fmt.Println("expression:", result.Expression)
	}
	fmt.Println("type:", result.Type)
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
	if result.Ownership != "" {
		fmt.Println("ownership:", result.Ownership)
	}
	if result.Async != nil {
		fmt.Println("async: scope", result.Async.Scope, "(read awaits)")
		fmt.Println("initializer effects:", result.Async.Effects)
		if len(result.Async.Captures) > 0 {
			fmt.Println("captures:", strings.Join(result.Async.Captures, ", "))
		}
	}
	if result.Lazy != nil {
		fmt.Println("lazy:", result.Lazy.Kind, "(first read forces)")
		fmt.Println("initializer effects:", result.Lazy.Effects)
		if len(result.Lazy.Captures) > 0 {
			fmt.Println("captures:", strings.Join(result.Lazy.Captures, ", "))
		}
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
	if tc := result.TailCall; tc != nil {
		if tc.Jump {
			fmt.Println("tail call: compiled as a jump to the top of the function")
		} else {
			fmt.Println("recursive call, not a tail call:", tc.Reason)
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

var releaseVersion string // Set by release builds; go install carries module metadata.

func version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Sum != "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func main() {
	if runUpdateWorker(os.Args) {
		return
	}
	driver.EnableCLICache()
	if len(os.Args) > 1 && os.Args[1] == "env" {
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "-json" {
				os.Args[i] = "--json"
			}
		}
	}
	command := boa.CmdT[boa.NoParams]{
		Use:   "bork",
		Short: "the bork compiler: a pragmatic backend language of guarantees",
		SubCmds: boa.SubCmds(
			boa.CmdT[newParams]{
				Use: "new", Short: "create a project from a bundled template",
				RunFunc: func(p *newParams, _ *cobra.Command, _ []string) {
					if err := project.New(p.Name, p.Template, p.Module); err != nil {
						fail(err)
					}
					fmt.Printf("Created %s (%s).\n\nNext steps:\n%s", p.Name, p.Template, project.NextSteps(p.Name, p.Template))
				},
			},
			boa.CmdT[boa.NoParams]{
				Use: "lsp", Short: "serve the Language Server Protocol over stdio",
				RunFunc: func(_ *boa.NoParams, _ *cobra.Command, _ []string) {
					if err := lsp.ServeWithVersion(os.Stdin, os.Stdout, version()); err != nil {
						fail(err)
					}
				},
			},
			boa.CmdT[cleanParams]{
				Use: "clean", Short: "remove bork compiler caches",
				RunFunc: func(p *cleanParams, cmd *cobra.Command, _ []string) {
					ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
					defer stop()
					if p.All {
						cache, err := toolenv.Value("BORKCACHE")
						if err != nil {
							fail(err)
						}
						if err := toolchain.Clean(ctx, cache); err != nil {
							fail(err)
						}
					}
					report, err := driver.Clean(ctx, p.All)
					if err != nil {
						fail(err)
					}
					fmt.Printf("Removed %d results, %d staged trees and %d temporary files (%d bytes).\n", report.Results, report.Stages, report.Temporaries, report.Bytes)
					if report.Dependencies != 0 {
						fmt.Printf("Removed %d resolved script dependency graphs.\n", report.Dependencies)
					}
				},
			},
			boa.CmdT[boa.NoParams]{
				Use:   "deps",
				Short: "manage pinned Go and bork dependencies",
				SubCmds: boa.SubCmds(
					boa.CmdT[depsParams]{
						Use: "init", Short: "initialize bork.sum and generated go.mod",
						RunFunc: func(p *depsParams, cmd *cobra.Command, args []string) {
							if err := driver.DepsWithOutput(p.Path, "init", args, cmd.OutOrStdout()); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[depsGetParams]{
						Use: "get", Short: "add, update, or remove dependencies",
						RunFunc: func(p *depsGetParams, cmd *cobra.Command, _ []string) {
							if err := driver.DepsWithOutput(p.Path, "get", p.Packages, cmd.OutOrStdout()); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[depsParams]{
						Use: "migrate", Short: "move legacy go-deps manifests into bork.mod and bork.sum",
						RunFunc: func(p *depsParams, cmd *cobra.Command, args []string) {
							if err := driver.DepsWithOutput(p.Path, "migrate", args, cmd.OutOrStdout()); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[depsParams]{
						Use: "download", Short: "download pinned dependencies and fill in checksums",
						RunFunc: func(p *depsParams, cmd *cobra.Command, args []string) {
							if err := driver.DepsWithOutput(p.Path, "download", args, cmd.OutOrStdout()); err != nil {
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
			boa.CmdT[docParams]{
				Use:   "doc",
				Short: "render checked public package APIs as Markdown or HTML",
				RunFunc: func(p *docParams, cmd *cobra.Command, _ []string) {
					text, err := driver.Doc(p.Path, driver.DocOptions{All: p.All, HTML: p.HTML})
					if err != nil {
						fail(err)
					}
					if _, err := cmd.OutOrStdout().Write(text); err != nil {
						fail(err)
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
			boa.CmdT[boa.NoParams]{
				Use: "debug", Short: "build and debug bork programs",
				SubCmds: boa.SubCmds(
					boa.CmdT[debugBuildParams]{
						Use: "build", Short: "build with bork source locations and inspectable variables",
						RunFunc: func(p *debugBuildParams, _ *cobra.Command, _ []string) {
							out := p.Output
							if out == "" {
								out = driver.DefaultOutput(p.Path)
							}
							if err := driver.BuildDebug(p.Path, out); err != nil {
								failDiagnostics(err, p.JSON, os.Stderr)
							}
						},
					},
					boa.CmdT[boa.NoParams]{
						Use: "setup", Short: "install the pinned optional debugger into BORKCACHE",
						RunFunc: func(_ *boa.NoParams, cmd *cobra.Command, _ []string) {
							ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
							defer stop()
							if err := driver.SetupDelve(ctx, cmd.OutOrStdout()); err != nil {
								fail(err)
							}
						},
					},
					boa.CmdT[debugDAPParams]{
						Use: "dap", Short: "serve a debug adapter on loopback TCP",
						RunFunc: func(p *debugDAPParams, cmd *cobra.Command, _ []string) {
							ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
							defer stop()
							if err := driver.DebugDAP(ctx, p.Delve, p.Listen, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
								fail(err)
							}
						},
					},
				),
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
					if err := driver.Build(p.Path, out, driver.BuildOptions{Fast: p.Fast, Rebuild: p.Rebuild}); err != nil {
						failDiagnostics(err, p.JSON, os.Stderr)
					}
				},
			},
			boa.CmdT[installParams]{
				Use:   "install",
				Short: "compile and install a program in BORKBIN",
				ValidArgsFunc: func(p *installParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *installParams, _ *cobra.Command, _ []string) {
					if err := driver.Install(p.Path); err != nil {
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
					code, err := driver.RunCLI(p.Path, p.Args, driver.BuildOptions{Fast: p.Fast, Rebuild: p.Rebuild})
					if err != nil {
						fail(err)
					}
					os.Exit(code)
				},
			},
			boa.CmdT[runParams]{
				Use: "script", Short: "compile and run a single .bork script with an implicit main",
				RunFunc: func(p *runParams, _ *cobra.Command, _ []string) {
					code, err := driver.RunScriptCLI(p.Path, p.Args, driver.BuildOptions{Fast: p.Fast, Rebuild: p.Rebuild})
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
				RunFunc: func(p *testParams, cmd *cobra.Command, _ []string) {
					if p.Cases < 0 {
						fail(errors.New("--cases must be positive"))
					}
					if p.Parallel < 0 {
						fail(errors.New("--parallel must be positive"))
					}
					code, err := driver.Test(p.Path, os.Stdout, driver.TestOptions{JSON: p.JSON, Filter: p.Filter, FilterSet: cmd.Flags().Changed("filter"), Update: p.Update, AutoProperties: p.AutoProperties, Seed: p.Seed, Cases: p.Cases, Parallel: p.Parallel, Hermetic: p.Hermetic})
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stderr)
					}
					os.Exit(code)
				},
			},
			boa.CmdT[pathJSONParams]{
				Use: "lint", Short: "check a package for unused code and needless expressions",
				RunFunc: func(p *pathJSONParams, _ *cobra.Command, _ []string) {
					warnings, err := driver.Lint(p.Path)
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stdout)
					}
					for _, warning := range warnings {
						if p.JSON {
							if err := json.NewEncoder(os.Stdout).Encode(warning); err != nil {
								fail(err)
							}
						} else {
							fmt.Println(warning)
						}
					}
				},
			},
			boa.CmdT[checkParams]{
				Use:   "check",
				Short: "type-check a bork program without building it",
				ValidArgsFunc: func(p *checkParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *checkParams, cmd *cobra.Command, _ []string) {
					if p.Watch {
						if err := runCheckWatch(cmd.Context(), p.Path, p.JSON, os.Stdout); err != nil {
							fail(err)
						}
						return
					}
					warnings, err := driver.CheckWarnings(p.Path)
					if err != nil {
						failDiagnostics(err, p.JSON, os.Stdout)
					}
					if p.JSON {
						encoder := json.NewEncoder(os.Stdout)
						for _, warning := range warnings {
							if err := encoder.Encode(warning); err != nil {
								fail(err)
							}
						}
					} else {
						for _, warning := range warnings {
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
					fmt.Printf("bork %s (%s)\n", version(), compilerSelection.Reason)
				},
			},
		),
	}
	command.SubCmds = append(command.SubCmds, envCommand(), upgradeCommand(), editorCommand())
	root := command.ToCobra()
	if path, enabled := earlyToolchainTarget(root, os.Args[1:]); enabled {
		if err := selectToolchain(root, path); err != nil {
			fail(err)
		}
	}
	root.PersistentPostRun = func(cmd *cobra.Command, _ []string) { maybeUpdateNotice(cmd) }
	root.SilenceUsage, root.SilenceErrors = true, true
	if err := root.Execute(); err != nil {
		fail(err)
	}
}
