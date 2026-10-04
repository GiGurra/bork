package driver

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/GiGurra/bork/internal/diag"
)

// Set only by integration tests through -ldflags=-X. Environment variables alone
// cannot enable disk result reuse in an ordinary compiler. Remove this gate once
// bounded admission, eviction and clean support permit automatic use.
var cacheTestGate string

var cacheTestState = startCacheTestState()

type cacheTestStartup struct {
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
	if cacheTestGate != "enabled" {
		return nil
	}
	root := os.Getenv("BORK_TEST_DISK_CACHE_DIRECTORY")
	if root == "" || !filepath.IsAbs(root) || !validReceiptPath(root) {
		return nil
	}
	state := &cacheTestStartup{root: root, done: make(chan struct{}), toolDone: make(chan struct{}), toolContext: resolveGoContext()}
	// Hash the actual running image alongside startup and CLI argument parsing.
	go func() {
		state.namespace, state.err = compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
		close(state.done)
	}()

	go func() {
		state.toolDigest, state.toolEvidence, state.toolErr = captureGoToolEvidence(state.toolContext.tool)
		close(state.toolDone)
	}()
	return state
}

func testCachedCompile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {
	state := cacheTestState
	cwd, err := os.Getwd()
	if err != nil {
		return freshTestCachedCompile(path, emit, nil)
	}
	request := cacheArtifactRequest{Path: path, Cwd: cwd, Emit: emit}
	<-state.done
	if state.err != nil {
		return freshTestCachedCompile(path, emit, nil)
	}
	store := cacheStore{root: state.root, namespace: state.namespace}
	if body := store.read(request); body != nil {
		<-state.toolDone
		restore := func(receipt *goContextReceipt) (*goContext, error) {
			if state.toolErr != nil {
				return nil, errUnsupportedGoReceipt
			}
			return receipt.restoreWithToolEvidence(resolveGoContext(), state.toolDigest, state.toolEvidence)
		}
		if result, err := body.validateWithContext(request, state.namespace, restore); err == nil {
			testCacheProbe("hit")
			return result.goSource, result.warnings, nil
		}
	}
	return freshTestCachedCompile(path, emit, &store)
}

func freshTestCachedCompile(path string, emit bool, store *cacheStore) ([]byte, []diag.Diagnostic, error) {

	timings := newCacheTestTimings()
	defer timings.finish()
	session := NewSession()
	session.observe = timings.observer("compile/")
	src, warnings, err := session.compile(path, emit)
	if err != nil {
		testCacheProbe("error")
		return src, warnings, err
	}
	if store == nil || session.last == nil {
		testCacheProbe("bypass")
		return src, warnings, nil
	}
	body, artifactErr := cacheArtifactFromObserved(session.last, session.last.sourcePaths, store.namespace, timings.observer("publication/"))
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
