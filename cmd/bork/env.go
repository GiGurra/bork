package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/GiGurra/bork/internal/gotoolchain"
	"os"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
)

func envCommand() *cobra.Command {
	var asJSON, write, unset bool
	command := &cobra.Command{
		Use:   "env [VAR...]",
		Short: "show or persist compiler settings and their sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (write && unset) || (asJSON && (write || unset)) {
				return errors.New("env: -json, -w and -u are mutually exclusive")
			}
			if write || unset {
				if len(args) == 0 {
					return errors.New("env: -w and -u require at least one setting")
				}
				if write {
					if err := toolenv.Update(args, nil); err != nil {
						return err
					}
					for _, arg := range args {
						name, _, _ := strings.Cut(arg, "=")
						if setting, err := toolenv.Lookup(name); err == nil && setting.Source == "environment" {
							if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is overridden by the environment\n", name); err != nil {
								return err
							}
						}
					}
					return nil
				}
				return toolenv.Update(nil, args)
			}
			if len(args) == 0 {
				args = append(toolenv.Names(), "BORKVERSION", "GOTOOLCHAIN", "GOVERSION", "GOROOT")
			}
			values := map[string]toolenv.Setting{}
			var goValues map[string]string
			for _, name := range args {
				if name == "GOTOOLCHAIN" || name == "GOVERSION" || name == "GOROOT" {
					if goValues == nil {
						var err error
						goValues, _, err = gotoolchain.Query("go", "", os.Environ())
						if err != nil {
							return err
						}
					}
					values[name] = toolenv.Setting{Value: goValues[name], Source: "Go toolchain (minimum " + gotoolchain.Minimum + ")"}
					continue
				}
				if name == "BORKVERSION" {
					values[name] = toolenv.Setting{Value: version(), Source: compilerSelection.Reason}
					continue
				}
				setting, err := toolenv.Lookup(name)
				if err != nil {
					return err
				}
				values[name] = setting
			}
			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(values)
			}
			for _, name := range args {
				setting := values[name]
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s=%s # %s\n", name, strconv.Quote(setting.Value), setting.Source); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print values and sources as JSON")
	command.Flags().BoolVarP(&write, "write", "w", false, "save VAR=value assignments")
	command.Flags().BoolVarP(&unset, "unset", "u", false, "remove saved settings")
	return command
}
