package driver

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// These bounded counters describe actual executions, not eligibility for hits.
// In particular a validated Go inventory does not certify generated support,
// contextual Facts, ordered prior values, or the enclosing compilation.
type executionObservations struct {
	Invocations, Captured, Validated, Declined uint64
	LastGoIdentity                             [sha256.Size]byte
	LastDecline                                executionDecline
}

func (o *executionObservations) decline(reason executionDecline) {
	o.Declined++
	// Do not retain arbitrary subprocess/path diagnostic payloads in accounting.
	if len(reason) > 512 {
		reason = "execution inventory declined"
	}
	o.LastDecline = reason
}

func (o *executionObservations) finish(inventory *goExecutionInventory, cleanup func()) {
	defer cleanup()
	if inventory.current() {
		o.Validated++
	} else {
		o.decline("Go execution inputs changed during execution")
	}
}

// buildObservedComptime keeps the published stage locked through execution and
// endpoint validation. One-shot callers and unsupported selections retain their
// ordinary build path; there are no result lookups or certification here.
func buildObservedComptime(files []*syntax.File, source []byte, out string, module *goModuleInputs, ctx *goContext, usage *goUsage, audit check.ExecutionAudit, embeds []*check.Embedded) (func(), error) {
	finish := func() {}
	if usage == nil {
		return finish, buildGoWithMode(files, source, out, module, ctx, "comptime", embeds...)
	}
	usage.evaluator = true
	observations := &usage.executions
	observations.Invocations++
	decline := executionDecline(audit.Decline)
	if decline == "" && (ctx == nil || ctx.err != nil || ctx.values["CGO_ENABLED"] != "0") {
		decline = "execution observation requires captured CGO-disabled context"
	}
	if decline == "" && (ctx.driver != "off" || ctx.driverErr != nil || goModuleHook != nil) {
		decline = "unsupported execution driver or module hook"
	}
	if decline != "" {
		observations.decline(decline)
		return finish, buildGoWithMode(files, source, out, module, ctx, "comptime", embeds...)
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return finish, err
	}
	dir, pinned, cleanup, err := stageGo(files, source, module, ctx, "comptime", embeds)
	if err != nil {
		return finish, err
	}
	inventory, decline := captureGoExecution(ctx, goExecutionStage{Root: dir, Mode: "comptime", Output: absOut, Program: source, Module: module})
	if decline != "" {
		observations.decline(decline)
		// Use this already-published stage. Restaging could change the effective
		// module inputs or invoke a test hook a second time.
		err := buildStagedGo(files, absOut, dir, pinned, ctx)
		cleanup()
		return finish, err
	}
	observations.Captured++
	observations.LastGoIdentity = inventory.identity()
	inv := inventory.Invocation
	cmd := exec.Command(inv.Tool, inv.BuildArgs...)
	cmd.Dir = inv.Root
	cmd.Env = slices.Clone(inv.Env)
	output, err := cmd.CombinedOutput()
	if err != nil {
		observations.decline("observed Go build failed")
		cleanup()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if diags := unsafeGoErrors(sourcePaths(files), dir, string(output)); diags != nil {
				return finish, &DiagError{Diags: diags}
			}
			if pinned {
				return finish, fmt.Errorf("building generated program with pinned Go dependencies failed (offline builds need the modules in Go's cache):\n%s", strings.TrimSpace(string(output)))
			}
			return finish, fmt.Errorf("go build failed on the generated code (this is a bork compiler bug):\n%s", strings.TrimSpace(string(output)))
		}
		return finish, fmt.Errorf("running go build (is Go installed?): %w", err)
	}
	return func() {
		observations.finish(inventory, cleanup)
	}, nil
}
