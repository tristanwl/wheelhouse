package main

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// The cgroup files that hold a container's memory limit: version 2, then 1.
var cgroupMemoryFiles = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

// memLimitPercent is how much of the container limit the Go runtime may use
// before its garbage collector works harder: the rest is headroom for ffmpeg,
// the SQLite driver's C-free allocations, stacks and the kernel's accounting.
const memLimitPercent = 80

// cgroupMemoryLimit returns the memory limit in bytes of the container the
// process runs in, or 0 when there is none (no cgroup files, "max", or a value
// so large it means "unlimited").
func cgroupMemoryLimit(readFile func(string) ([]byte, error)) int64 {
	for _, name := range cgroupMemoryFiles {
		raw, err := readFile(name)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(raw))
		if s == "" || s == "max" {
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 || n >= 1<<60 {
			return 0 // cgroup v1 reports a huge number for "no limit"
		}
		return n
	}
	return 0
}

// applyMemoryLimit sets the Go runtime's soft memory limit (what GOMEMLIMIT
// does) to 80% of the container's cgroup limit, so a burst of large upstream
// answers makes the collector run harder instead of the kernel killing the
// process. An operator's own GOMEMLIMIT always wins and nothing is set when
// there is no container limit. It returns the limit it set, or 0.
func applyMemoryLimit(getenv func(string) string, readFile func(string) ([]byte, error), set func(int64) int64) int64 {
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	limit := cgroupMemoryLimit(readFile)
	if limit == 0 {
		return 0
	}
	soft := limit / 100 * memLimitPercent
	set(soft)
	return soft
}

func applyMemoryLimitFromEnvironment() int64 {
	return applyMemoryLimit(os.Getenv, os.ReadFile, debug.SetMemoryLimit)
}
