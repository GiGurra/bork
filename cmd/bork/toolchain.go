package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/toolchain"
	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
)

var compilerSelection = toolchain.Selection{Reason: "local compiler"}

func selectToolchain(cmd *cobra.Command, path string) error {
	setting, err := toolenv.Value("BORKTOOLCHAIN")
	if err != nil {
		return err
	}
	compilerSelection, err = toolchain.Select(version(), setting, path)
	if err != nil {
		return err
	}
	if selected := os.Getenv(toolchain.SelectedEnv); selected != "" {
		if selected != version() || compilerSelection.Query != "" {
			return fmt.Errorf("compiler switching loop: selected %s, running %s", selected, version())
		}
		compilerSelection.Reason = os.Getenv(toolchain.ReasonEnv)
		// These identify one compiler handoff, not subprocesses launched by it.
		if err := os.Unsetenv(toolchain.SelectedEnv); err != nil {
			return err
		}
		if err := os.Unsetenv(toolchain.ReasonEnv); err != nil {
			return err
		}
	}
	if compilerSelection.Query == "" {
		return nil
	}
	cache, err := toolenv.Value("BORKCACHE")
	if err != nil {
		return err
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()
	var child *exec.Cmd
	err = toolchain.WithCompiler(ctx, cache, compilerSelection.Query, cmd.ErrOrStderr(), func(binary, selected string) error {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "bork %s (%s)\n", selected, compilerSelection.Reason); err != nil {
			return err
		}
		child = exec.CommandContext(ctx, binary, os.Args[1:]...)
		child.Env = append(os.Environ(), toolchain.SelectedEnv+"="+selected, toolchain.ReasonEnv+"="+compilerSelection.Reason)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		return child.Start()
	})
	if err != nil {
		return err
	}
	if err := child.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
			os.Exit(exit.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil
}

// Select before local flag/parameter validation: the requested compiler may
// introduce new commands or flags the launcher does not understand.
func earlyToolchainTarget(root *cobra.Command, raw []string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	cmd, args, err := root.Find(raw)
	if err != nil {
		return ".", true
	}
	var positional []string
	depsPath := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "help" || name == "h" {
			return "", false
		}
		if cmd.Name() == "env" && (name == "write" || name == "w" || name == "unset" || name == "u") {
			return "", false
		}
		flag := cmd.Flags().Lookup(name)
		if !strings.HasPrefix(arg, "--") && len(name) != 0 {
			flag = cmd.Flags().ShorthandLookup(name[:1])
			if len(name) > 1 {
				value, hasValue = name[1:], true
			}
		}
		if flag != nil && flag.NoOptDefVal == "" && !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		if name == "path" {
			depsPath = value
		}
	}
	if cmd.Parent() != nil && cmd.Parent().Name() == "deps" && depsPath != "" {
		return depsPath, true
	}
	if cmd == root {
		return ".", true
	}
	return toolchainTarget(cmd, positional)
}

func toolchainTarget(cmd *cobra.Command, args []string) (string, bool) {
	name := cmd.Name()
	if cmd.Parent() != nil && cmd.Parent().Name() == "deps" {
		name = "deps"
	}
	switch name {
	case "env":
		write, _ := cmd.Flags().GetBool("write")
		unset, _ := cmd.Flags().GetBool("unset")
		return ".", !write && !unset
	case "version", "lsp":
		return ".", true
	case "deps":
		path, _ := cmd.Flags().GetString("path")
		if path == "" {
			path = "."
		}
		return path, true
	case "describe":
		if len(args) == 0 {
			return ".", true
		}
		path := args[0]
		for range 2 {
			colon := strings.LastIndexByte(path, ':')
			if colon < 0 {
				break
			}
			if _, err := strconv.Atoi(path[colon+1:]); err != nil {
				break
			}
			path = path[:colon]
		}
		return path, true
	case "check", "build", "run", "install", "test", "emit", "script":
		if len(args) != 0 {
			return args[0], true
		}
		return ".", true
	default:
		return "", false
	}
}
