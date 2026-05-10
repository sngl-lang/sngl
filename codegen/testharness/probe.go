package testharness

import (
	"fmt"
	"sync"
)

type ProbeFunc func() Available

var (
	probesMu sync.RWMutex
	probes   = map[string]ProbeFunc{}
)

// Register installs a probe under the given platform name. Intended for
// init() in each platform package. Re-registering replaces the previous
// probe.
func Register(name string, fn ProbeFunc) {
	probesMu.Lock()
	defer probesMu.Unlock()
	probes[name] = fn
}

// Probe runs the registered probe for name. Returns OK=false with a
// reason when no probe is registered.
func Probe(name string) Available {
	probesMu.RLock()
	fn, ok := probes[name]
	probesMu.RUnlock()
	if !ok {
		return Available{OK: false, Reason: fmt.Sprintf("no probe registered for platform %q", name)}
	}
	return fn()
}

// unregisterForTest is exported within-package only for test cleanup.
func unregisterForTest(name string) {
	probesMu.Lock()
	defer probesMu.Unlock()
	delete(probes, name)
}
