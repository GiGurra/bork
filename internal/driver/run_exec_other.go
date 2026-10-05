//go:build !unix

package driver

import "errors"

const canExecProgram = false

func execProgram(string, []string) error {
	return errors.New("process replacement is not supported")
}
