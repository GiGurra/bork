package driver

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/GiGurra/bork/internal/diag"
)

// Set only by integration tests through -ldflags=-X. Environment variables alone
// cannot enable disk result reuse in an ordinary compiler. Remove this gate once
// bounded admission, eviction and clean support permit automatic use.
var cacheTestGate string

var cacheTestState = startCacheTestState()

type cacheTestStartup struct {
	start        sync.Once
	toolDone     chan struct{}
	toolContext  *goContext
	toolDigest   [sha256.Size]byte
	toolEvidence *goToolEvidence
	toolErr      error
	root         string
	done         chan struct{}
	namespace    [sha256.Size]byte
	err          error
}

func startCacheTestState() *cacheTestStartup {
	if cacheTestGate != "enabled" || cacheDisabled() {
		return nil
	}
	root := os.Getenv("BORK_TEST_DISK_CACHE_DIRECTORY")
	if root == "" || !filepath.IsAbs(root) || !validReceiptPath(root) {
		return nil
	}

	return &cacheTestStartup{root: root}
}

func (state *cacheTestStartup) startIdentity() {
	state.start.Do(func() {
		state.done = make(chan struct{})
		state.toolDone = make(chan struct{})
		state.toolContext = resolveGoContext()
		go func() {
			state.namespace, state.err = compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
			close(state.done)
		}()
		go func() {
			state.toolDigest, state.toolEvidence, state.toolErr = captureGoToolEvidence(state.toolContext.tool)
			close(state.toolDone)
		}()
	})
}

func testCachedCompile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {
	state := cacheTestState
	cwd, err := os.Getwd()
	if err != nil {
		return freshTestCachedCompile(path, emit)
	}
	request := cacheArtifactRequest{Path: path, Cwd: cwd, Emit: emit}

	// Locate a bounded candidate before identity work. Programs with no candidate
	// and known-bypass misses never hash compiler/launcher for cache purposes.
	if body := findCacheArtifact(state.root, request); body != nil {
		inputs, err := body.Source.snapshot()
		if err == nil && inputs.current() {
			state.startIdentity()
			<-state.done
			if state.err != nil {
				return freshTestCachedCompile(path, emit)
			}
			if body.Namespace != state.namespace {
				body = (cacheStore{root: state.root, namespace: state.namespace}).read(request)
				if body == nil {
					return freshTestCachedCompile(path, emit)
				}
			}
			<-state.toolDone
			restore := func(receipt *goContextReceipt) (*goContext, error) {
				if state.toolErr != nil {
					return nil, errUnsupportedGoReceipt
				}
				return receipt.restoreWithToolEvidence(resolveGoContext(), state.toolDigest, state.toolEvidence)
			}
			result, err := body.validateWithContext(request, body.Namespace, restore)
			<-state.done
			if err == nil && state.err == nil && state.namespace == body.Namespace {
				(cacheStore{root: state.root, namespace: body.Namespace}).touch(body.Key)
				testCacheProbe("hit")
				return result.goSource, result.warnings, nil
			}
		}
	}
	return freshTestCachedCompile(path, emit)
}

func freshTestCachedCompile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {

	timings := newCacheTestTimings()
	defer timings.finish()

	background := os.Getenv("BORK_TEST_DISK_CACHE_BACKGROUND") == "1"
	compile := compileCacheMiss
	if background {
		compile = compileCacheMissUncaptured
	}
	src, warnings, artifact, err := compile(path, emit, timings.observer("compile/"))
	if err != nil {
		testCacheProbe("error")
		return src, warnings, err
	}
	if artifact == nil {
		testCacheProbe("bypass")
		return src, warnings, nil
	}
	state := cacheTestState
	if background {
		timings.phase("publication/queue")
		if queueCachePublication(state.root, artifact) {
			testCacheProbe("queued")
		} else {
			testCacheProbe("publish-skip")
		}
		return src, warnings, nil
	}
	state.startIdentity()
	<-state.done
	if state.err != nil {
		testCacheProbe("bypass")
		return src, warnings, nil
	}
	ownedStore := cacheStore{root: state.root, namespace: state.namespace}
	store := &ownedStore
	body, artifactErr := cacheArtifactFromObserved(artifact, artifact.sourcePaths, store.namespace, timings.observer("publication/"))
	if artifactErr != nil {
		testCacheProbe("bypass")
		return src, warnings, nil
	}
	timings.phase("publication/encode-and-store")
	if err := store.write(body); err != nil {
		testCacheProbe("write-miss")
	} else {
		testCacheProbe("miss")
	}
	return src, warnings, nil
}

func testCacheProbe(status string) {
	path := os.Getenv("BORK_TEST_DISK_CACHE_PROBE")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(status + "\n")
	_ = file.Close()
}

// Profiling is available only in a specially built test CLI, never ordinary builds.
type cacheTestTimings struct {
	path    string
	stage   string
	start   time.Time
	elapsed map[string]float64
}

func newCacheTestTimings() *cacheTestTimings {
	path := os.Getenv("BORK_TEST_DISK_CACHE_TIMINGS")
	if path == "" {
		return nil
	}
	return &cacheTestTimings{path: path, elapsed: map[string]float64{}}
}
func (t *cacheTestTimings) phase(stage string) {
	if t == nil {
		return
	}
	if t.stage != "" {
		t.elapsed[t.stage] += float64(time.Since(t.start)) / float64(time.Millisecond)
	}
	t.stage = stage
	t.start = time.Now()
}
func (t *cacheTestTimings) observer(prefix string) func(string) {
	if t == nil {
		return nil
	}
	return func(stage string) {
		if stage != "" {
			stage = prefix + stage
		}
		t.phase(stage)
	}
}
func (t *cacheTestTimings) finish() {
	if t == nil {
		return
	}
	t.phase("")
	data, err := json.Marshal(t.elapsed)
	if err == nil {
		_ = os.WriteFile(t.path, data, 0600)
	}
}
