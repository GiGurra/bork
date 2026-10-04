//go:build linux || darwin

package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const (
	cacheTrimArgument    = "--bork-internal-cache-trim-v1"
	cacheTrimHardTimeout = 15 * time.Second
)

func cacheTrimSupported() bool { return true }

// queueCacheTrim does bounded direct-path work only. The inherited admission
// descriptor permits one detached worker per root, without a growing job queue.
func queueCacheTrim(base string) bool {
	if cacheDisabled() {
		return false
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	state := readCacheTrimState(root)
	now := time.Now()
	if validCacheTrimState(state) && !state.Completed.IsZero() && !state.Completed.After(now.Add(cacheTrimInterval)) && now.Sub(state.Completed) < cacheTrimInterval {
		return false
	}
	admission, err := (cacheStore{root: base}).tryLock(root, "trim-admission.lock")
	if err != nil {
		return false
	}
	defer func() { _ = admission.Close() }()
	// Recheck after admission: the preceding worker may have just completed.
	state = readCacheTrimState(root)
	if validCacheTrimState(state) && !state.Completed.IsZero() && !state.Completed.After(now.Add(cacheTrimInterval)) && now.Sub(state.Completed) < cacheTrimInterval {
		return false
	}
	directory, err := root.Open(".")
	if err != nil {
		return false
	}
	defer func() { _ = directory.Close() }()
	image, err := openCacheTrimImage()
	if err != nil {
		return false
	}
	defer func() { _ = image.Close() }()
	extras := []*os.File{image, directory, admission}
	notify, closeNotify := cacheTrimTestPipe("BORK_TEST_CACHE_TRIM_NOTIFY_FD", &extras)
	defer closeNotify()
	barrier, closeBarrier := cacheTrimTestPipe("BORK_TEST_CACHE_TRIM_BARRIER_FD", &extras)
	defer closeBarrier()
	command := exec.Command(cacheTrimExecutable(image), cacheTrimArgument, strconv.Itoa(notify), strconv.Itoa(barrier))
	command.ExtraFiles = extras
	command.Env = os.Environ()
	command.Dir = "/"
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// No inherited terminal or user streams; the child validates its image and
	// rooted directory descriptors. No spool files or persisted stat trust.
	if err := command.Start(); err != nil {
		return false
	}
	go func() { _ = command.Wait() }()
	return true
}
func cacheTrimTestPipe(name string, extras *[]*os.File) (int, func()) {
	if cacheTestGate != "enabled" {
		return 0, func() {}
	}
	descriptor, err := strconv.Atoi(os.Getenv(name))
	if err != nil || descriptor < 3 || descriptor > 64 {
		return 0, func() {}
	}
	duplicate, err := syscall.Dup(descriptor)
	if err != nil {
		return 0, func() {}
	}
	file := os.NewFile(uintptr(duplicate), name)
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		_ = file.Close()
		return 0, func() {}
	}
	number := 3 + len(*extras)
	*extras = append(*extras, file)
	return number, func() { _ = file.Close() }
}
func init() {
	if len(os.Args) != 4 || os.Args[1] != cacheTrimArgument {
		return
	}
	group, err := syscall.Getpgid(0)
	if err != nil || group != os.Getpid() {
		os.Exit(0)
	}
	timeout := cacheTrimHardTimeout
	if cacheTestGate == "enabled" {
		if value, err := strconv.Atoi(os.Getenv("BORK_TEST_CACHE_TRIM_TIMEOUT_MS")); err == nil && value > 0 && value <= 15000 {
			timeout = time.Duration(value) * time.Millisecond
		}
	}
	_ = time.AfterFunc(timeout, func() { _ = syscall.Kill(-os.Getpid(), syscall.SIGKILL); os.Exit(0) })
	lowerCacheTrimPriority()
	ok := runQueuedCacheTrim()
	if descriptor, err := strconv.Atoi(os.Args[2]); err == nil && descriptor >= 6 && descriptor <= 7 {
		pipe := os.NewFile(uintptr(descriptor), "cache-trim-notify")
		status := byte(0)
		if ok {
			status = 1
		}
		_, _ = pipe.Write([]byte{status})
		_ = pipe.Close()
	}
	os.Exit(0)
}
func runQueuedCacheTrim() bool {
	for descriptor := 3; descriptor <= 7; descriptor++ {
		syscall.CloseOnExec(descriptor)
	}
	image := os.NewFile(3, "cache-trim-image")
	info, err := image.Stat()
	_ = image.Close()
	if err != nil || !cacheTrimImageMatches(info) || cacheDisabled() {
		return false
	}
	directory := os.NewFile(4, "cache-trim-root")
	info, err = directory.Stat()
	if err != nil || !info.IsDir() {
		return false
	}
	// FD4 remains open through rooted maintenance and makes directory replacement
	// after queueing harmless. The argument cannot choose a different cache root.
	defer func() { _ = directory.Close() }()
	if descriptor, err := strconv.Atoi(os.Args[3]); err == nil && descriptor >= 6 && descriptor <= 7 {
		barrier := os.NewFile(uintptr(descriptor), "cache-trim-barrier")
		var value [1]byte
		_, err := barrier.Read(value[:])
		_ = barrier.Close()
		if err != nil {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheTrimWorkerTime)
	defer cancel()
	for range 8 {
		report, err := runCacheTrim(ctx, cacheTrimRootDescriptor(), time.Now(), 128)
		if err != nil {
			return errors.Is(err, context.DeadlineExceeded)
		}
		if report.Complete || report.Steps == 0 {
			return true
		}
	}
	return true
}
