package driver

import (
	"crypto/sha256"
	"encoding/binary"
	"os/exec"
	"time"
)

// Request-owned observations are not execution receipts. Installed SDK identity
// follows the shared immutable-installation policy, not a consumed-input proof.
type executionObservations struct {
	Invocations, Captured, Validated, Declined, MemoHits uint64
	LastBuild, LastProcess                               [sha256.Size]byte
	LastMode                                             string
	LastLimit                                            time.Duration
	LastSDK                                              installedSDKIdentity
	LastDecline                                          executionDecline
}

func (o *executionObservations) decline(reason executionDecline) {
	o.Declined++
	if len(reason) > 512 {
		reason = "execution observation declined"
	}
	o.LastDecline = reason
}

type executionObservation struct {
	usage   *goUsage
	context *goContext
	token   executionInvocation
	sdk     *installedSDKIdentity
}

func beginExecutionObservation(usage *goUsage, context *goContext, mode string, limit time.Duration) *executionObservation {
	if usage == nil {
		return nil
	}
	usage.evaluator = true
	if usage.deferInputs {
		return nil
	}
	if usage.execution == nil {
		usage.execution = &executionTracker{}
	}
	observation := &executionObservation{usage: usage, context: context, token: usage.execution.begin(nil)}
	counts := &usage.executions
	counts.Invocations++
	counts.LastMode, counts.LastLimit = mode, limit
	// Clear witnesses so a failed call or memo hit cannot inherit an older build.
	counts.LastBuild, counts.LastProcess = [sha256.Size]byte{}, [sha256.Size]byte{}
	counts.LastSDK = installedSDKIdentity{}
	if context != nil && context.err == nil {
		observation.sdk = captureInstalledSDK(context.tool, context.values["GOROOT"], context.values["GOVERSION"])
	}
	if observation.sdk == nil {
		counts.decline("installed SDK identity unavailable")
	} else {
		counts.Captured++
		counts.LastSDK = *observation.sdk
	}
	return observation
}

func (o *executionObservation) finish() {
	if o == nil {
		return
	}
	defer o.usage.execution.decline(o.token, executionClosureUnavailable)
	if o.sdk == nil {
		return
	}
	if o.sdk.current(o.context.tool, o.context.values["GOROOT"], o.context.values["GOVERSION"]) {
		o.usage.executions.Validated++
	} else {
		o.usage.executions.decline("installed SDK identity changed during evaluation")
	}
}

// Bind actual command descriptors separately for build and evaluator processes.
// Hashing is bounded and preserves ordered argv/env; these are observations only.
func (o *executionObservation) command(cmd *exec.Cmd, build bool) {
	if o == nil {
		return
	}
	total := len(cmd.Path) + len(cmd.Dir) + 32
	for _, values := range [][]string{cmd.Args, cmd.Env} {
		for _, value := range values {
			if len(value) > executionIdentityMaxBytes-total-8 {
				o.usage.executions.decline("command observation exceeds budget")
				return
			}
			total += len(value) + 8
		}
	}
	if total > executionIdentityMaxBytes {
		o.usage.executions.decline("command observation exceeds budget")
		return
	}
	digest := sha256.New()
	var length [8]byte
	write := func(value string) {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
	write(cmd.Path)
	write(cmd.Dir)
	for _, values := range [][]string{cmd.Args, cmd.Env} {
		binary.BigEndian.PutUint64(length[:], uint64(len(values)))
		_, _ = digest.Write(length[:])
		for _, value := range values {
			write(value)
		}
	}
	var identity [sha256.Size]byte
	copy(identity[:], digest.Sum(nil))
	if build {
		o.usage.executions.LastBuild = identity
	} else {
		o.usage.executions.LastProcess = identity
	}
}

func (o *executionObservation) memoHit() {
	if o != nil {
		o.usage.executions.MemoHits++
	}
}
