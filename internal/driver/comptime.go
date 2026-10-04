package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/syntax"
)

const comptimeTimeout = 10 * time.Second

func (ctx *goContext) comptimeLimit() time.Duration {
	if ctx.evalLimit > 0 {
		return ctx.evalLimit
	}
	return comptimeTimeout
}

func evaluateComptimes(files []*syntax.File, info *check.Info, diags *diag.List, module *goModuleInputs, goctx *goContext, usage *goUsage) check.Evaluator {
	if goctx.err != nil {
		diags.AddCode(info.Comptimes[0].Pos(), "comptime.toolchain", "cannot resolve Go: %v", goctx.err)
		return nil
	}
	if goctx.values["GOOS"] != runtime.GOOS || goctx.values["GOARCH"] != runtime.GOARCH {
		diags.AddCode(info.Comptimes[0].Pos(), "comptime.target", "comptime requires native target %s/%s, got %s/%s", runtime.GOOS, runtime.GOARCH, goctx.values["GOOS"], goctx.values["GOARCH"])
		return nil
	}
	states := map[*check.Comptime]int{}
	proofStates := map[*check.Func]int{}
	activeQueries := map[string]bool{}
	memo := newPredicateMemo()
	var proofEvaluator check.Evaluator
	var evaluate func(*check.Comptime)
	evaluate = func(node *check.Comptime) {
		if diags.Len() > 0 || states[node] == 2 {
			return
		}
		if states[node] == 1 {
			diags.AddCode(node.Pos(), "comptime.cycle", "cyclic comptime value or proof dependency")
			return
		}
		states[node] = 1
		if usage != nil {
			usage.evaluator = true
		}
		helpers := gen.ComptimeFunctions(files, info, node)
		dependencies := func(x check.Expr) bool {
			if dep, ok := x.(*check.Comptime); ok {
				if dep.Value == nil {
					evaluate(dep)
				}
				return false
			}
			return true
		}
		check.WalkComptime(node.Body, dependencies)
		for _, capture := range node.Captures {
			check.WalkComptime(capture.Let.Value, dependencies)
		}
		for _, fn := range helpers {
			check.WalkComptime(fn.Requires, dependencies)
			check.WalkComptime(fn.Body, dependencies)
		}
		if diags.Len() > 0 {
			return
		}
		eval := evaluatorWithTimeoutMemo(files, info, module, goctx, goctx.comptimeLimit(), memo)
		// Proof predicates can have their own computed dependencies too.
		var prove check.Evaluator
		prove = func(queries []check.Query) ([]bool, error) {
			var prepare func(check.Query)
			prepare = func(q check.Query) {
				for _, q := range q.And {
					prepare(q)
				}
				for _, q := range q.Or {
					prepare(q)
				}
				if q.Pred != nil {
					synthetic := &check.Comptime{Body: q.Pred.Body}
					functions := append([]*check.Func{q.Pred}, gen.ComptimeFunctions(files, info, synthetic, q.Dicts...)...)
					for _, fn := range functions {
						check.WalkComptime(fn.Requires, dependencies)
						check.WalkComptime(fn.Body, dependencies)
					}
					if diags.Len() > 0 {
						return
					}
					var pending []*check.Func
					seen := map[*check.Func]bool{}
					for _, fn := range functions {
						if seen[fn] {
							continue
						}
						seen[fn] = true
						if proofStates[fn] == 1 {
							diags.AddCode(node.Pos(), "comptime.cycle", "cyclic comptime predicate proof dependency")
							return
						}
						if proofStates[fn] != 2 {
							pending = append(pending, fn)
						}
					}
					for _, fn := range pending {
						if proofStates[fn] == 2 {
							continue
						}
						proofStates[fn] = 1
						check.ComptimeProof([]*check.Func{fn}, info, diags, prove)
						if diags.Len() > 0 {
							return
						}
						proofStates[fn] = 2
					}
					key := check.ComptimeQueryKey(q)
					if activeQueries[key] {
						diags.AddCode(node.Pos(), "comptime.cycle", "cyclic comptime predicate argument proof")
						return
					}
					activeQueries[key] = true
					check.ComptimeQuery(q, info, diags, prove)
					delete(activeQueries, key)
				}
			}
			for _, q := range queries {
				prepare(q)
			}
			if diags.Len() > 0 {
				return nil, fmt.Errorf("comptime proof dependency could not be evaluated")
			}
			if usage != nil {
				usage.evaluator = true
			}
			return eval(queries)
		}
		proofEvaluator = prove
		check.ComptimeRecipe(node, info, diags, prove, helpers)
		if diags.Len() > 0 {
			return
		}
		source, err := gen.ComptimeProgram(files, info, node)
		if err != nil {
			diags.AddCode(node.Pos(), "comptime.result", "cannot bake computation: %v", err)
			return
		}
		if usage != nil {
			usage.evaluator = true
		}
		var value []byte
		if usage == nil {
			value, err = runComptime(files, source, module, goctx, info.Embeds...)
		} else {
			audit := check.AuditComptimeExecution(info, node)
			value, err = runComptimeObserved(files, source, module, goctx, usage, audit, info.Embeds...)
		}
		if err != nil {
			diags.AddCode(node.Pos(), "comptime.evaluate", "comptime failed: %v", err)
			return
		}
		decoded, err := check.DecodeComptime(node, value)
		if err != nil {
			diags.AddCode(node.Pos(), "comptime.result", "invalid computation result: %v", err)
			return
		}
		node.Value = decoded
		check.ComptimeResult(node, info, diags, prove)
		if diags.Len() > 0 {
			return
		}
		states[node] = 2
	}
	for _, node := range info.Comptimes {
		evaluate(node)
	}
	// Reuse checked predicate implementations for contextual output constraints.
	return proofEvaluator
}

func runComptime(files []*syntax.File, source []byte, module *goModuleInputs, goctx *goContext, embeds ...*check.Embedded) ([]byte, error) {
	return runComptimeObserved(files, source, module, goctx, nil, check.ExecutionAudit{}, embeds...)
}

func runComptimeObserved(files []*syntax.File, source []byte, module *goModuleInputs, goctx *goContext, usage *goUsage, audit check.ExecutionAudit, embeds ...*check.Embedded) ([]byte, error) {
	if usage != nil {
		usage.evaluator = true
		if usage.execution == nil {
			usage.execution = &executionTracker{}
		}
		// Bind a logical attempt before execution. Go-only observations cannot
		// prepare a complete candidate; all outcomes therefore remain declines.
		token := usage.execution.begin(nil)
		defer usage.execution.decline(token, executionClosureUnavailable)
	}
	dir, err := os.MkdirTemp("", "bork-comptime-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	exe := filepath.Join(dir, "eval")
	finish, err := buildObservedComptime(files, source, exe, module, goctx, usage, audit, embeds)
	if err != nil {
		return nil, err
	}
	defer finish()
	result := filepath.Join(dir, "result.json")
	deadline, cancel := context.WithTimeout(context.Background(), goctx.comptimeLimit())
	defer cancel()
	cmd := exec.CommandContext(deadline, exe, result)
	cmd.WaitDelay = time.Second
	configureEvaluationProcess(cmd)
	cmd.Env = slices.Clone(goctx.processEnv)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	stderr := &boundedOutput{limit: 64 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if deadline.Err() != nil {
			return nil, fmt.Errorf("evaluation exceeded %s", goctx.comptimeLimit())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s", message)
	}
	file, err := os.Open(result)
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

// Continue draining evaluator stderr after the diagnostic budget is exhausted.
// Returning short writes would replace the evaluator's actual failure.
type boundedOutput struct {
	data  []byte
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(n, remaining)]...)
	}
	return n, nil
}
func (b *boundedOutput) String() string { return string(b.data) }
