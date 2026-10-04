package driver

import "syscall"

func lowerCachePublisherPriority() { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19) }
