//go:build !linux && !darwin

package driver

import (
	"errors"
	"os"
)

func lockGoStage(string) (*os.File, error) {
	return nil, errors.New("stable staging locks unavailable on this platform")
}
