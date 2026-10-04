//go:build linux

package driver

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const cachePublishArgument = "--bork-internal-cache-publish-v1"
const cachePublisherTimeout = 15 * time.Second

func init() {
	if cacheTestGate != "enabled" || len(os.Args) != 4 || os.Args[1] != cachePublishArgument {
		return
	}
	// A publisher is its own session/process-group leader. Its timeout terminates
	// the entire group, including metadata subprocesses, rather than leaving Go.
	group, err := syscall.Getpgid(0)
	if err != nil || group != os.Getpid() {
		os.Exit(0)
	}
	timeout := cachePublisherTimeout
	if text := os.Getenv("BORK_TEST_CACHE_PUBLISH_TIMEOUT_MS"); text != "" {
		if n, err := strconv.Atoi(text); err == nil && n > 0 && n <= 15000 {
			timeout = time.Duration(n) * time.Millisecond
		}
	}
	_ = time.AfterFunc(timeout, func() { _ = syscall.Kill(-os.Getpid(), syscall.SIGKILL); os.Exit(0) })
	lowerCachePublisherPriority()
	ok := runCachePublisher()
	if descriptor, err := strconv.Atoi(os.Args[2]); err == nil && descriptor >= 6 && descriptor <= 7 {
		file := os.NewFile(uintptr(descriptor), "cache-publish-completion")
		status := byte(0)
		if ok {
			status = 1
		}
		_, _ = file.Write([]byte{status})
		_ = file.Close()
	}
	os.Exit(0)
}

func queueCachePublication(directory string, artifact *sessionArtifact) bool {
	if !cacheTrimSupported() || cacheDisabled() || cacheTestGate != "" && os.Getenv("BORK_TEST_CACHE_PUBLISH") != "on" {
		return false
	}
	job, err := newCachePublishJob(directory, artifact)
	if err != nil {
		return false
	}
	encoded, err := encodeCachePublishJob(job)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return false
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Join("locks", "publish-v1"), 0700); err != nil {
		return false
	}
	key, err := job.Request.key()
	if err != nil {
		return false
	}
	slotName := filepath.Join("locks", "publish-v1", fmt.Sprintf("%02x.lock", key[0]%cachePublisherSlots))
	slot, err := openCacheFile(root, slotName, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	defer func() { _ = slot.Close() }()
	// Fixed admission slots are nonblocking: busy jobs are skipped, never queued.
	if err := syscall.Flock(int(slot.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	if err := root.MkdirAll(filepath.Join("jobs", "v1"), 0700); err != nil {
		return false
	}
	temporary := filepath.Join("jobs", "v1", ".job-"+rand.Text())
	file, err := openCacheFile(root, temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	// Unlink before writing: the inherited descriptor is the only job identity.
	// A killed parent or worker leaves no retained spool payload.
	if err := root.Remove(temporary); err != nil {
		return false
	}
	if _, err := file.Write(encoded); err != nil {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	image, err := os.Open("/proc/self/exe")
	if err != nil {
		return false
	}
	defer func() { _ = image.Close() }()
	// Descriptor execution keeps the child's image identical to the actual parent,
	// even if an installer has replaced the os.Executable pathname meanwhile.
	extras := []*os.File{image, file, slot}
	notification, barrier := 0, 0
	for _, hook := range []struct {
		env    string
		target *int
	}{{"BORK_TEST_CACHE_PUBLISH_NOTIFY_FD", &notification}, {"BORK_TEST_CACHE_PUBLISH_BARRIER_FD", &barrier}} {
		descriptor, err := strconv.Atoi(os.Getenv(hook.env))
		if err != nil || descriptor < 3 || descriptor > 64 {
			continue
		}
		// Hooks use explicit pipes, never terminal streams or arbitrary inherited FDs.
		duplicate, err := syscall.Dup(descriptor)
		if err != nil {
			continue
		}
		f := os.NewFile(uintptr(duplicate), hook.env)
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			_ = f.Close()
			continue
		}
		defer func() { _ = f.Close() }()
		*hook.target = 3 + len(extras)
		extras = append(extras, f)
	}
	command := exec.Command("/proc/self/fd/3", cachePublishArgument, strconv.Itoa(notification), strconv.Itoa(barrier))
	command.ExtraFiles = extras
	command.Env = artifact.context.processEnv
	command.Dir = job.Request.Cwd
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Nil stdin/stdout/stderr are /dev/null. No terminal or user stream survives.
	if err := command.Start(); err != nil {
		return false
	}
	// In a long-lived caller reap the process. On CLI exit init adopts the child.
	go func() { _ = command.Wait() }()
	return true
}

func runCachePublisher() bool {
	for descriptor := 3; descriptor <= 7; descriptor++ {
		syscall.CloseOnExec(descriptor)
	}
	image := os.NewFile(3, "cache-publish-image")
	info, err := image.Stat()
	if err != nil || !sameRunningImage(info) {
		return false
	}
	_ = image.Close()
	file := os.NewFile(4, "cache-publish-job")
	job, err := decodeCachePublishJob(file)
	_ = file.Close()
	if err != nil {
		return false
	}
	// Admission FD5 remains open until process exit, including completion hooks.
	EnableCLICache()
	if cacheCLIState == nil || job.Root != cacheCLIState.root || cacheDisabled() {
		return false
	}
	if descriptor, err := strconv.Atoi(os.Args[3]); err == nil && descriptor >= 6 && descriptor <= 7 {
		barrier := os.NewFile(uintptr(descriptor), "cache-publish-barrier")
		var byte [1]byte
		_, err := io.ReadFull(barrier, byte[:])
		_ = barrier.Close()
		if err != nil {
			return false
		}
	}
	candidate, err := job.candidate()
	if err != nil {
		return false
	}
	artifact := captureCacheMiss(candidate)
	if artifact == nil {
		return false
	}
	namespace, err := compilerArtifactNamespace(strconv.Itoa(cacheArtifactSchema), cacheArtifactLayout)
	if err != nil {
		return false
	}
	body, err := cacheArtifactFrom(artifact, artifact.sourcePaths, namespace)
	if err != nil {
		return false
	}
	ok := (cacheStore{root: job.Root, namespace: namespace}).write(body) == nil
	if ok {
		_ = queueCacheTrim(job.Root)
	}
	return ok
}

func lowerCachePublisherPriority() {
	// Linux nice/io priority is per thread. Set every existing thread so future
	// Go threads and subprocesses inherit low priority too; unsupported IO policy
	// or permission failures simply retain the available priority mechanism.
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19)
		return
	}
	for _, entry := range entries {
		thread, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, thread, 19)
		_, _, _ = syscall.Syscall(syscall.SYS_IOPRIO_SET, 1, uintptr(thread), uintptr(3<<13))
	}
}
