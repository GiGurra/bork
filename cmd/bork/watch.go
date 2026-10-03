package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/GiGurra/bork/internal/driver"
)

func runCheckWatch(parent context.Context, path string, asJSON bool, output io.Writer) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	retrigger := make(chan struct{}, 1)
	if runtime.GOOS != "windows" {
		hangup := make(chan os.Signal, 1)
		signal.Notify(hangup, syscall.SIGHUP)
		defer signal.Stop(hangup)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hangup:
					select {
					case retrigger <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	encoder := json.NewEncoder(output)
	err := driver.Watch(ctx, path, driver.WatchOptions{Retrigger: retrigger}, func(result driver.WatchResult) error {
		if asJSON {
			return encoder.Encode(result)
		}
		if _, err := fmt.Fprintf(output, "check %d: %s\n", result.RequestID, result.Status); err != nil {
			return err
		}
		for _, diagnostic := range result.Diagnostics {
			if _, err := fmt.Fprintln(output, diagnostic); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
