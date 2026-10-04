package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/syntax"
)

// A batch belongs to one compilation. The driver authorizes one recipe at a
// time, after its dependencies and proof obligations have been checked.
type comptimeBatch struct {
	nodes       []*check.Comptime
	packages    map[*check.PackageBinding]*check.Comptime
	bindings    map[*check.Comptime]*check.PackageBinding
	cmd         *exec.Cmd
	input       io.WriteCloser
	output      io.ReadCloser
	stderr      *boundedOutput
	dir         string
	observation *executionObservation
	cancel      context.CancelFunc
	stopSignals context.CancelFunc
	waited      bool
}

func planComptimeBatch(info *check.Info, ordinary bool) *comptimeBatch {
	batch := &comptimeBatch{nodes: slices.Clone(info.Comptimes), packages: map[*check.PackageBinding]*check.Comptime{}, bindings: map[*check.Comptime]*check.PackageBinding{}}
	var include func(*check.PackageBinding)
	include = func(binding *check.PackageBinding) {
		if batch.packages[binding] != nil {
			return
		}
		node := check.PackageComptime(binding)
		batch.packages[binding], batch.bindings[node] = node, binding
		batch.nodes = append(batch.nodes, node)
		for _, dep := range binding.Dependencies {
			include(dep)
		}
	}
	for _, node := range info.Comptimes {
		for _, binding := range check.ComptimePackageBindings(info, node) {
			include(binding)
		}
	}
	// Keep single ordinary recipes on their standalone/native fast path.
	// Package dependencies need shared getters even with one explicit block.
	if len(batch.packages) == 0 && (!ordinary || len(batch.nodes) < 2) {
		return nil
	}
	return batch
}

func (batch *comptimeBatch) start(files []*syntax.File, info *check.Info, module *goModuleInputs, goctx *goContext, usage *goUsage) error {
	source, err := gen.ComptimeBatchProgram(files, info, batch.nodes, batch.packages)
	if err != nil {
		return err
	}
	batch.dir, err = os.MkdirTemp("", "bork-comptime-batch-*")
	if err != nil {
		return err
	}
	batch.observation = beginExecutionObservation(usage, goctx, "comptime", goctx.comptimeLimit())
	exe := filepath.Join(batch.dir, "eval")
	if err := buildGoWithModeObserved(files, source, exe, module, goctx, "comptime", batch.observation, info.Embeds...); err != nil {
		return err
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	batch.stopSignals = stop
	ctx, cancel := context.WithCancel(signals)
	batch.cancel = cancel
	batch.cmd = exec.CommandContext(ctx, exe, filepath.Join(batch.dir, "result.json"))
	batch.cmd.WaitDelay = time.Second
	configureEvaluationProcess(batch.cmd)
	batch.cmd.Env = slices.Clone(goctx.processEnv)
	batch.cmd.Dir = batch.dir
	batch.stderr = &boundedOutput{limit: 64 << 10}
	batch.cmd.Stderr = batch.stderr
	batch.input, err = batch.cmd.StdinPipe()
	if err != nil {
		return err
	}
	batch.output, err = batch.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	batch.observation.command(batch.cmd, false)
	return batch.cmd.Start()
}

func (batch *comptimeBatch) close() {
	if batch == nil {
		return
	}
	if batch.input != nil {
		_ = batch.input.Close()
	}
	if batch.cancel != nil {
		batch.cancel()
	}
	if batch.cmd != nil && batch.cmd.Process != nil {
		if batch.cmd.Cancel != nil {
			_ = batch.cmd.Cancel()
		}
		if !batch.waited {
			_ = batch.cmd.Wait()
		}
	}
	if batch.stopSignals != nil {
		batch.stopSignals()
	}
	if batch.output != nil {
		_ = batch.output.Close()
	}
	if batch.dir != "" {
		_ = os.RemoveAll(batch.dir)
	}
	batch.observation.finish()
}

func (batch *comptimeBatch) evaluate(node *check.Comptime, files []*syntax.File, info *check.Info, module *goModuleInputs, goctx *goContext, usage *goUsage) ([]byte, error) {
	if batch.cmd == nil {
		if err := batch.start(files, info, module, goctx, usage); err != nil {
			return nil, err
		}
	}
	index := slices.Index(batch.nodes, node)
	if index < 0 {
		return nil, fmt.Errorf("comptime recipe is missing from batch")
	}
	if _, err := fmt.Fprintln(batch.input, index); err != nil {
		return nil, err
	}
	completed := make(chan error, 1)
	go func() {
		var ack [1]byte
		_, err := io.ReadFull(batch.output, ack[:])
		if err == nil && ack[0] != 1 {
			err = fmt.Errorf("invalid comptime response")
		}
		completed <- err
	}()
	timer := time.NewTimer(goctx.comptimeLimit())
	defer timer.Stop()
	select {
	case err := <-completed:
		if err != nil {
			batch.cancel()
			// Wait drains stderr before exposing the panic diagnostic.
			_ = batch.cmd.Wait()
			batch.waited = true
			message := strings.TrimSpace(batch.stderr.String())
			if message == "" {
				message = err.Error()
			}
			return nil, fmt.Errorf("%s", message)
		}
	case <-timer.C:
		batch.cancel()
		<-completed
		return nil, fmt.Errorf("evaluation exceeded %s", goctx.comptimeLimit())
	}
	file, err := os.Open(filepath.Join(batch.dir, "result.json"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, check.ComptimeResultLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > check.ComptimeResultLimit {
		return nil, fmt.Errorf("result exceeds 16 MiB")
	}
	return data, nil
}
