package codegen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// buildMemoryBudget is what one Go toolchain invocation is assumed to need.
// A cold `go test` over a generated fyne program peaks around 0.66GB of
// resident compiler and linker; a gtk4 one adds cgo on top. A gigabyte apiece
// leaves room for that and for the test binary driving it.
const buildMemoryBudget = 1 << 30

// AcquireBuildToken blocks until this machine has room for another Go
// toolchain invocation, and returns the func that gives the room back.
//
// The bound is across processes, not within one. `go test ./...` runs the
// platform test binaries concurrently, each drives its fixtures in parallel,
// and each fixture shells out to a `go` that forks its own compilers and
// linkers — so an in-process limit bounds a fifth of the real number. Memory
// is what actually runs out: CI OOM-killed the compiler
// ("compile: signal: killed") building a fyne fixture, and the host it runs on
// has locked up under build pressure before.
//
// Tokens are lock files, so a process that dies releases its own.
func AcquireBuildToken(ctx context.Context) (release func(), err error) {
	slots := buildTokenSlots()
	dir := filepath.Join(SnglCacheDir(), "tokens")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Without a place to coordinate, run unbounded rather than not at all.
		return func() {}, nil
	}

	for {
		for i := range slots {
			path := filepath.Join(dir, fmt.Sprintf("build-%d.lock", i))
			if unlock, ok := lockBuildDirFile(path); ok {
				return unlock, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// BuildTokenSlots reports how many Go toolchain invocations may run at once.
func BuildTokenSlots() int { return buildTokenSlots() }

var buildTokenSlots = sync.OnceValue(computeBuildTokenSlots)

func computeBuildTokenSlots() int {
	if s := os.Getenv("SNGL_BUILD_SLOTS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	slots := int(availableMemory() / buildMemoryBudget)
	slots = min(slots, runtime.GOMAXPROCS(0))
	return max(slots, 1)
}

// availableMemory reports the memory this process may use, preferring the
// cgroup limit the container was given over the host's total — a CI runner
// sees the host's /proc/meminfo whatever its own limit is.
//
// The cgroup branch is the one that matters in CI and is not a fallback:
// GitLab's docker executor sets a hard `memory` on every job container, so
// this reads the runner's real budget rather than the shared host's free
// memory. SNGL_BUILD_SLOTS exists for the case where neither number is the
// truth; it should not be needed to paper over this one.
func availableMemory() uint64 {
	if v, ok := readCgroupLimit(); ok {
		return v
	}
	if v, ok := readMemAvailable(); ok {
		return v
	}
	return 4 << 30
}

func readCgroupLimit() (uint64, bool) {
	for _, p := range []string{
		"/sys/fs/cgroup/memory.max",                   // cgroup v2
		"/sys/fs/cgroup/memory/memory.limit_in_bytes", // cgroup v1
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		if s == "max" {
			continue
		}
		n, err := strconv.ParseUint(s, 10, 64)
		// v1 reports a sentinel near 2^63 when unlimited.
		if err != nil || n == 0 || n > 1<<62 {
			continue
		}
		return n, true
	}
	return 0, false
}

func readMemAvailable() (uint64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "MemAvailable:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
