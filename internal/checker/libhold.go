package checker

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"duckfam.us/sngl/ir"
)

// LibraryState is what a target says about itself that changes the package it
// serves beyond its name and whether it is available: gtk4's GIR setting
// decides which widgets sngl:platform/gtk4 declares.
type LibraryState interface {
	LibraryState() string
}

// processLibs is the library each configuration of targets checks against,
// loaded lazily and kept for the life of the process.
var processLibs struct {
	sync.Mutex
	byKey map[string]*libCache
}

// sharedLibCache is the process's cache for cfg's targets, or nil when cfg
// substitutes library source or asks for a library of its own.
func sharedLibCache(cfg *Config) *libCache {
	if cfg != nil && (cfg.LibSources != nil || cfg.PrivateLibrary) {
		return nil
	}
	key := libKey(cfg)
	processLibs.Lock()
	defer processLibs.Unlock()
	if processLibs.byKey == nil {
		processLibs.byKey = map[string]*libCache{}
	}
	l := processLibs.byKey[key]
	if l == nil {
		l = newLibCache()
		processLibs.byKey[key] = l
	}
	return l
}

// EjectLibraries drops every library the process holds, so the next check
// loads afresh; a check already running keeps the one it holds.
func EjectLibraries() {
	processLibs.Lock()
	defer processLibs.Unlock()
	processLibs.byKey = nil
}

// libKey names what a library check reads besides the embedded source: each
// registered target, whether it is available, and its LibraryState.
func libKey(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	var parts []string
	add := func(tier, name string, t any) {
		part := tier + ":" + name
		if err := targetUnavailable(t); err != nil {
			part += "!" + err.Error()
		}
		if s, ok := t.(LibraryState); ok {
			part += "=" + s.LibraryState()
		}
		parts = append(parts, part)
	}
	for _, l := range cfg.Languages {
		add("language", l.LanguageIdentifier(), l)
	}
	for _, p := range cfg.Platforms {
		add("platform", p.PlatformIdentifier(), p)
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n")
}

// libHold is one check's hold on its cache, shared with the checks nested in
// it -- a directory import, a scheme's package -- so neither lock is taken
// twice by one chain of checks.
//
// A check holds the cache for reading from start to end, which is what lets
// it read the cache's tables without a lock of its own. A load trades that for
// the write lock while it writes them, and freezes what it loaded as it lets
// go: everything another check finds in the cache is finished.
type libHold struct {
	libs   *libCache
	read   bool
	writes int
	// replayed is the packages whose load diagnostics this check has
	// reported (replayLoad).
	replayed map[string]bool
}

func (cfg *Config) libHold(libs *libCache) *libHold {
	if cfg != nil && cfg.hold != nil {
		return cfg.hold
	}
	return &libHold{libs: libs, replayed: map[string]bool{}}
}

// check holds the cache for reading for a check, and returns its end. A
// nested check holds nothing more.
func (h *libHold) check() func() {
	if h.read || h.writes > 0 {
		return func() {}
	}
	h.libs.mu.RLock()
	h.read = true
	return func() {
		h.read = false
		h.libs.mu.RUnlock()
	}
}

// load holds the cache for writing, and returns the load's end. Another check
// may load between letting go of the read lock and taking the write lock, so a
// load looks again for what it is about to load once it holds the cache.
func (h *libHold) load() func() {
	if h.writes++; h.writes > 1 {
		return func() { h.writes-- }
	}
	if h.read {
		h.libs.mu.RUnlock()
	}
	h.libs.mu.Lock()
	return func() {
		if h.writes--; h.writes > 0 {
			return
		}
		h.libs.freezeLoaded()
		h.libs.mu.Unlock()
		if h.read {
			h.libs.mu.RLock()
		}
	}
}

// freezeLoaded freezes every package in the cache once a load has added one,
// but a target package whose overrides are not merged yet: a later check
// building for it merges them into it. The caller holds the write lock.
func (l *libCache) freezeLoaded() {
	if !l.loaded {
		return
	}
	l.loaded = false
	for _, p := range l.packages() {
		if name, ok := strings.CutPrefix(p.Path, "sngl:"); ok && !l.extended[name] {
			if _, _, isTarget := targetTierName(name); isTarget {
				continue
			}
		}
		ir.Freeze(p)
	}
}

// recordLoad keeps what loading key reported, from start on, for every later
// check that finds it loaded. The caller holds the write lock.
func (c *checker) recordLoad(key string, start int) {
	c.libs.loaded = true
	c.hold.replayed[key] = true
	if diags := c.diags[start:]; len(diags) > 0 {
		c.libs.diags[key] = slices.Clone(diags)
	}
}

// replayLoad reports what loading key reported, to a check that found it
// loaded: a library problem is every program's that reaches it, as it was
// when each check loaded its own.
func (c *checker) replayLoad(key string) {
	if c.hold.replayed[key] {
		return
	}
	c.hold.replayed[key] = true
	diags := c.libs.diags[key]
	if len(diags) == 0 {
		return
	}
	seen := make(map[string]bool, len(c.diags))
	for _, d := range c.diags {
		seen[diagKey(d)] = true
	}
	for _, d := range diags {
		if !seen[diagKey(d)] {
			c.diags = append(c.diags, d)
		}
	}
}

func diagKey(d ir.Diagnostic) string {
	return fmt.Sprintf("%d %s", d.Severity, d.Error())
}

// loadHold is libHold.load for this check. The outermost load resolves the
// signature of every macro the cache holds before it lets go, where a mark's
// first use used to: written then, a program's check would write a library
// declaration other checks and builds are reading.
func (c *checker) loadHold() func() {
	end := c.hold.load()
	return func() {
		if c.hold.writes == 1 && c.libs.loaded {
			for _, p := range c.libs.packages() {
				for _, fn := range p.Macros {
					c.resolveMacroSig(p, fn)
				}
			}
		}
		end()
	}
}
