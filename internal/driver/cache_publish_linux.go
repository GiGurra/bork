//go:build linux

package driver

import (
	"os"
	"strconv"
	"syscall"
)

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
