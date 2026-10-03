package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/GiGurra/bork/internal/diag"
)

// WatchOptions configures serialized content polling. Retrigger forces a fresh
// compilation, including inputs outside the compiler's tracked inventory.
type WatchOptions struct {
	Interval  time.Duration
	Retrigger <-chan struct{}
}

// WatchResult is a complete diagnostic snapshot. Failed checks are recoverable;
// every publication owns its diagnostics. Request IDs also count obsolete
// attempts whose inputs changed during checking and whose results were withheld.
type WatchResult struct {
	SchemaVersion int               `json:"schema_version"`
	RequestID     uint64            `json:"request_id"`
	Status        string            `json:"status"`
	Diagnostics   []diag.Diagnostic `json:"diagnostics"`
}

// Watch checks once, then checks again only after a tracked input changes or a
// manual retrigger. Unsupported result-cache paths still compile afresh, but do
// not execute periodically. Untracked unsafe/foreign inputs need a retrigger.
func Watch(ctx context.Context, path string, options WatchOptions, report func(WatchResult) error) error {
	return watchSession(ctx, path, options, report, NewSession())
}

func watchSession(ctx context.Context, path string, options WatchOptions, report func(WatchResult) error, session *Session) error {
	if report == nil {
		return fmt.Errorf("watch requires a result callback")
	}
	interval := options.Interval
	if interval == 0 {
		interval = time.Second
	}
	if interval < 0 {
		return fmt.Errorf("watch interval must be positive")
	}
	session.watch = true
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var request uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		request++
		warnings, err := session.Check(path)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		published, forced := false, false
		if session.attempt != nil {
			result := WatchResult{SchemaVersion: 1, RequestID: request, Status: "ok", Diagnostics: warnings}
			if err != nil {
				result.Status = "error"
				var compilation *DiagError
				if errors.As(err, &compilation) {
					result.Diagnostics = cloneSessionDiagnostics(compilation.Diags.Sorted())
				} else {
					result.Diagnostics = []diag.Diagnostic{{Code: "tool.error", Msg: err.Error(), Severity: "error"}}
				}
			}
			if result.Diagnostics == nil {
				result.Diagnostics = []diag.Diagnostic{}
			}
			var publicationErr error
			published, publicationErr = publishWatchResult(ctx, result, func() bool {
				current := session.attempt.current()
				select {
				case _, open := <-options.Retrigger:
					if open {
						forced = true
					} else {
						options.Retrigger = nil
					}
				default:
				}
				return current && !forced
			}, report)
			if publicationErr != nil {
				return publicationErr
			}
		}
		if forced {
			session.last = nil
			continue
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case _, open := <-options.Retrigger:
				if !open {
					options.Retrigger = nil
					continue
				}
				session.last = nil
				break wait
			case <-ticker.C:
				if !published || session.attempt == nil || !session.attempt.current() {
					break wait
				}
			}
		}
	}
}

// Attempts are trigger inventories, including failures and cache bypasses, never
// reusable semantic results. They retain no AST/checker state.
type watchAttempt struct {
	inputs    *sourceSnapshot
	assets    *embedSnapshot
	context   *goContext
	names     []goNameInput
	config    *sourceSnapshot
	launchers []watchLauncher
}

func newWatchAttempt(context *goContext) *watchAttempt {
	attempt := &watchAttempt{context: context}
	if context.validation != nil {
		return attempt
	}
	// Unsupported configuration does not gain result reuse. Still track the
	// concrete config files and selected launcher that this invocation observed.
	attempt.config = newSourceSnapshot()
	userFile := context.processValue("GOENV")
	if userFile == "" {
		if directory, err := os.UserConfigDir(); err == nil {
			userFile = filepath.Join(directory, "go", "env")
		}
	}
	root := context.values["GOROOT"]
	if root == "" {
		root = context.processValue("GOROOT")
	}
	paths := []string{userFile}
	if root != "" {
		paths = append(paths, filepath.Join(root, "go.env"))
	}
	for _, path := range paths {
		if path != "" && path != "off" {
			_, _ = attempt.config.readFile(path)
		}
	}
	for _, path := range []string{context.tool, context.driver, context.self} {
		if path == "" || path == "off" {
			continue
		}
		launcher := watchLauncher{path: path}
		launcher.resolved, _ = filepath.EvalSymlinks(path)
		digest, evidence, err := captureGoToolEvidence(path)
		if err != nil {
			launcher.captureErr = err.Error()
		} else {
			launcher.proof = &goContextValidation{launcher: path, toolDigest: digest, toolEvidence: evidence}
		}
		attempt.launchers = append(attempt.launchers, launcher)
	}
	return attempt
}

func (attempt *watchAttempt) current() bool {
	if attempt.inputs == nil || !attempt.inputs.current() {
		return false
	}
	resolved := resolveGoContext()
	if !slices.Equal(resolved.processEnv, attempt.context.processEnv) || resolved.tool != attempt.context.tool ||
		resolved.driver != attempt.context.driver || resolved.self != attempt.context.self || fmt.Sprint(resolved.driverErr) != fmt.Sprint(attempt.context.driverErr) {
		return false
	}
	if attempt.context.validation != nil {
		if !attempt.context.validation.current() {
			return false
		}
	} else {
		if attempt.config != nil && !attempt.config.current() {
			return false
		}
		for _, launcher := range attempt.launchers {
			target, _ := filepath.EvalSymlinks(launcher.path)
			if target != launcher.resolved {
				return false
			}
			if launcher.proof != nil {
				if !launcher.proof.toolCurrent() {
					return false
				}
			} else {
				_, _, err := captureGoToolEvidence(launcher.path)
				if err == nil || err.Error() != launcher.captureErr {
					return false
				}
			}
		}
	}
	if attempt.assets != nil && !attempt.assets.current() {
		return false
	}
	for _, name := range attempt.names {
		if name.inputs != nil && !name.inputs.current() {
			return false
		}
	}
	return attempt.inputs.current()
}

// Validate before calling the publisher, with cancellation checked again after
// potentially expensive content validation. A withheld result remains pending.
func publishWatchResult(ctx context.Context, result WatchResult, current func() bool, report func(WatchResult) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	valid := current()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}
	if err := report(result); err != nil {
		return false, err
	}
	return true, nil
}

type watchLauncher struct {
	path, resolved, captureErr string
	proof                      *goContextValidation
}
