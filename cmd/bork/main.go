// Command bork is the bork compiler and toolchain.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/GiGurra/boa/pkg/boa"
	"github.com/GiGurra/bork/internal/driver"
	"github.com/spf13/cobra"
)

type pathParams struct {
	Path string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
}

type buildParams struct {
	Path   string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Output string `short:"o" optional:"true" descr:"output executable (default: the file or directory name)"`
}

type testParams struct {
	Path   string `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Update bool   `short:"u" optional:"true" descr:"write the snapshots assertSnapshot finds missing or different, instead of failing"`
}

type runParams struct {
	Path string   `positional:"true" optional:"true" default:"." descr:"a .bork file, or a directory of .bork files (one package)"`
	Args []string `positional:"true" optional:"true" descr:"arguments passed to the program (put them after --)"`
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
						fail(err)
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
				Use:   "test",
				Short: "run a bork program's tests, checking trusted facts as they run",
				ValidArgsFunc: func(p *testParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *testParams, _ *cobra.Command, _ []string) {
					code, err := driver.Test(p.Path, os.Stdout, driver.TestOptions{Update: p.Update})
					if err != nil {
						fail(err)
					}
					os.Exit(code)
				},
			},
			boa.CmdT[pathParams]{
				Use:   "check",
				Short: "type-check a bork program without building it",
				ValidArgsFunc: func(p *pathParams, cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return completeBorkPaths(p, cmd, args, toComplete)
				},
				RunFunc: func(p *pathParams, _ *cobra.Command, _ []string) {
					if _, _, err := driver.Check(p.Path); err != nil {
						fail(err)
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
