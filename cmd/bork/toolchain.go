package main

import (
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

func configureToolchain(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		configureToolchain(cmd)
	}
	previous := root.PreRunE
	root.PreRunE = func(cmd *cobra.Command, args []string) error {
		if previous != nil {
			if err := previous(cmd, args); err != nil {
				return err
			}
		}
		path, enabled := toolchainTarget(cmd, args)
		if !enabled {
			return nil
		}
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
		}
		if compilerSelection.Query == "" {
			return nil
		}
		cache, err := toolenv.Value("BORKCACHE")
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()
		binary, selected, err := toolchain.Ensure(ctx, cache, compilerSelection.Query, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "bork %s (%s)\n", selected, compilerSelection.Reason); err != nil {
			return err
		}
		child := exec.CommandContext(ctx, binary, os.Args[1:]...)
		child.Env = append(os.Environ(), toolchain.SelectedEnv+"="+selected, toolchain.ReasonEnv+"="+compilerSelection.Reason)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
				os.Exit(exit.ExitCode())
			}
			return err
		}
		os.Exit(0)
		return nil
	}
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
