package driver

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Set by integration tests through -ldflags=-X. Test images require explicit
// result-cache and detached-worker opt-ins; ordinary CLI images enable caching.
var cacheTestGate string

var cacheCLIState = startCacheTestState()

type cacheCLIStartup struct {
	production bool
	start      sync.Once
	root       string
	done       chan struct{}
	namespace  [sha256.Size]byte
	err        error
}

func startCacheTestState() *cacheCLIStartup {
	if cacheTestGate != "enabled" || !cacheTrimSupported() || cacheDisabled() {
		return nil
	}
	root := os.Getenv("BORK_TEST_DISK_CACHE_DIRECTORY")
	if root == "" || !filepath.IsAbs(root) || !validReceiptPath(root) {
		return nil
	}

	return &cacheCLIStartup{root: root}
}

// EnableCLICache activates owned complete-result reuse for the command-line
// process. Library callers keep the ordinary AST pipeline unless explicitly
// enabled. Unsupported platforms use their ordinary compiler path.
func EnableCLICache() {
	if cacheTestGate != "" && (cacheTestGate != "enabled" || os.Getenv("BORK_TEST_CACHE_PRODUCTION") != "1") || cacheDisabled() || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		return
	}
	root, err := cacheRootDir()
	if err == nil {
		cacheCLIState = &cacheCLIStartup{root: root, production: true}
	}
}

func (state *cacheCLIStartup) startIdentity() {
	state.start.Do(func() {
		state.done = make(chan struct{})
		go func() {
			state.namespace, state.err = compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
			close(state.done)
		}()
	})
}

func cachedCompile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {
	if result := lookupCachedCompilation(path, emit); result != nil {
		return result.goSource, result.warnings, nil
	}
	return freshCachedCompile(path, emit)
}
func lookupCachedCompilation(path string, emit bool) *cachedCompilation {
	state := cacheCLIState
	cwd, err := os.Getwd()
	if err != nil {
		return nil
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
				return nil
			}
			if body.Namespace != state.namespace {
				return nil
			}
			restore := func(receipt *goContextReceipt) (*goContext, error) {
				return receipt.restoreInstalledSDK(resolveGoContext())
			}
			result, err := body.validateWithContext(request, body.Namespace, restore)
			<-state.done
			if err == nil && state.err == nil && state.namespace == body.Namespace {
				(cacheStore{root: state.root, namespace: body.Namespace}).touch(body.Key)
				_ = queueCacheTrim(state.root)
				testCacheProbe("hit")
				return result
			}
		}
	}
	return nil
}

func compileBuild(path string, build func(*compiledProgram, []byte) error) error {
	if result := lookupCachedCompilation(path, true); result != nil && result.rootSource != "" {
		// These are only source-location records for staging/error mapping. No
		// parser/checker state is reconstructed or reused.
		files := cachedBuildFiles(result)
		return build(&compiledProgram{files: files, module: result.module, context: result.context}, result.goSource)
	}
	_, _, err := freshCachedCompileWithBuild(path, true, build)
	return err
}

func cachedBuildFiles(result *cachedCompilation) []*syntax.File {
	files := make([]*syntax.File, 0, len(result.sourcePaths))
	for _, path := range result.sourcePaths {
		files = append(files, &syntax.File{Path: path, Prelude: path != result.rootSource})
	}
	return files
}

func freshCachedCompile(path string, emit bool) ([]byte, []diag.Diagnostic, error) {
	return freshCachedCompileWithBuild(path, emit, nil)
}
func freshCachedCompileWithBuild(path string, emit bool, build func(*compiledProgram, []byte) error) ([]byte, []diag.Diagnostic, error) {

	timings := newCacheTestTimings()
	defer timings.finish()

	background := cacheCLIState.production || os.Getenv("BORK_TEST_DISK_CACHE_BACKGROUND") == "1"
	compile := compileCacheMiss
	if background {
		compile = compileCacheMissUncaptured
	}
	if build != nil {
		compile = func(path string, emit bool, observe func(string)) ([]byte, []diag.Diagnostic, *sessionArtifact, error) {
			source, warnings, artifact, err := compileCacheMissWithBuild(path, emit, observe, build)
			if err == nil && artifact != nil && !background {
				artifact = captureCacheMiss(artifact)
			}
			return source, warnings, artifact, err
		}
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
	state := cacheCLIState
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
		_ = queueCacheTrim(state.root)
	}
	return src, warnings, nil
}

func testCacheProbe(status string) {
	testCacheProbeAt("BORK_TEST_DISK_CACHE_PROBE", status)
}

func testCacheProbeAt(setting, status string) {
	if cacheTestGate != "enabled" {
		return
	}
	path := os.Getenv(setting)
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
	if cacheTestGate != "enabled" {
		return nil
	}
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
