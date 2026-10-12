package checker

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// pkgEntry is a directory import's or a scheme's package as a check left it,
// kept on the library cache it was checked against for every later check that
// imports the same source.
//
// Only a package whose check is a function of what the entry records is kept:
// its source, by digest, and the entries it imported. So its imports are
// library packages and kept directory packages and nothing else -- a go:
// package, a plugin's output or a fetch would have to be resolved again to be
// known unchanged -- and its check reported nothing of its own.
type pkgEntry struct {
	key string
	// path is a directory import's path under the check's FS, which is read
	// again to tell whether the entry still holds; empty for a scheme's.
	path     string
	digest   string
	deps     []*pkgEntry
	pkg      *ir.Package
	exported *ir.Package
	// libKeys is the library loads the check reached, whose diagnostics a
	// check finding the entry reports as one reaching them would.
	libKeys []string
}

// nestedReport is what a nested check tells the import that started it about
// whether its package may be kept.
type nestedReport struct {
	uncacheable bool
	deps        []*pkgEntry
	libKeys     []string
}

func (cfg *Config) nestedReport() *nestedReport {
	if cfg == nil {
		return nil
	}
	return cfg.report
}

// sourceDigest is the digest of a package's files, or "" for one with a file
// the compiler built rather than parsed.
func sourceDigest(docs []*ast.Document) string {
	var b strings.Builder
	for _, d := range docs {
		if d.Digest == "" {
			return ""
		}
		b.WriteString(d.Digest)
	}
	return b.String()
}

// entryKey names what a nested check reads besides its source and its
// imports: the import root, the replace map and the build's targets.
func (c *checker) entryKey(kind, name string) string {
	reps := slices.Sorted(maps.Keys(c.replaces))
	for i, r := range reps {
		reps[i] = r + "=" + c.replaces[r]
	}
	return strings.Join([]string{kind, c.cfg.Dir, name, strings.Join(reps, ","), fmt.Sprint(c.targets)}, "\x00")
}

// noteDep records that this package imported e, a kept directory package; nil
// is one that was not kept.
func (c *checker) noteDep(e *pkgEntry) {
	if e == nil {
		c.uncacheable = true
	} else if !slices.Contains(c.deps, e) {
		c.deps = append(c.deps, e)
	}
}

// cachedDir is cachedPkg for a directory import, which under a scheme's FS is
// never kept: an entry's directories are read again through the project's.
func (c *checker) cachedDir(key string, docs []*ast.Document) *pkgEntry {
	if c.cfg.inScheme {
		return nil
	}
	return c.cachedPkg(key, docs)
}

// cachedPkg is the entry key holds for docs, if it is still what checking them
// would give.
func (c *checker) cachedPkg(key string, docs []*ast.Document) *pkgEntry {
	digest := sourceDigest(docs)
	if digest == "" {
		return nil
	}
	c.libs.pkgMu.Lock()
	e := c.libs.pkgEntries[key]
	c.libs.pkgMu.Unlock()
	if e == nil || e.digest != digest {
		return nil
	}
	for _, d := range e.deps {
		if !c.entryCurrent(d) {
			return nil
		}
	}
	return e
}

// entryCurrent reports whether a kept directory package another entry
// imported is still the one this build would check: this build has not
// checked the directory into a package of its own, the cache still holds it,
// and its source is unchanged, as are its own imports.
//
// Only the last two are remembered for the chain, since the build may check
// the directory afresh after asking -- under another replace map, so another
// key -- and a later hit must then not name the kept one.
func (c *checker) entryCurrent(e *pkgEntry) bool {
	if pkg, ok := c.dirPkgs[e.path]; ok && pkg != e.exported {
		return false
	}
	if ok, seen := c.hold.current[e]; seen {
		return ok
	}
	c.hold.current[e] = false
	c.libs.pkgMu.Lock()
	held := c.libs.pkgEntries[e.key] == e
	c.libs.pkgMu.Unlock()
	if !held {
		return false
	}
	docs, err := c.cfg.Resolver.Resolve(c.cfg.FS, e.path)
	if err != nil || sourceDigest(docs) != e.digest {
		return false
	}
	for _, d := range e.deps {
		if !c.entryCurrent(d) {
			return false
		}
	}
	c.hold.current[e] = true
	return true
}

// useEntry makes a kept package this build's, with every directory package it
// imported, as checking them would have: memoized, their contexts adopted and
// the library diagnostics they reached reported.
func (c *checker) useEntry(e *pkgEntry, seen map[*pkgEntry]bool) {
	if seen[e] {
		return
	}
	seen[e] = true
	c.adoptContexts(e.pkg)
	for _, key := range e.libKeys {
		c.replayLoad(key)
	}
	if e.path != "" {
		if _, done := c.dirPkgs[e.path]; !done {
			c.dirPkgs[e.path] = e.exported
			c.dirEntries[e.path] = e
		}
	}
	for _, d := range e.deps {
		c.useEntry(d, seen)
	}
}

// keep stores what a nested check left, if it may be kept, and returns the
// entry; nil means the importing package may not be kept either. A package
// that may not be kept drops what was kept under its key, which is stale.
func (c *checker) keep(key, path string, docs []*ast.Document, pkg, exported *ir.Package, rep *nestedReport) *pkgEntry {
	digest := sourceDigest(docs)
	if rep.uncacheable || digest == "" {
		c.libs.storeEntry(key, nil)
		return nil
	}
	c.settle(pkg)
	ir.Freeze(pkg)
	ir.Freeze(exported)
	e := &pkgEntry{key: key, path: path, digest: digest, deps: rep.deps, pkg: pkg, exported: exported, libKeys: rep.libKeys}
	c.libs.storeEntry(key, e)
	return e
}

// storeEntry keeps e under key, or nothing for a nil e, and drops the entry it
// replaces and every entry that imported that one, directly or not: an edit
// ejects the package and what was checked against it.
func (l *libCache) storeEntry(key string, e *pkgEntry) {
	l.pkgMu.Lock()
	defer l.pkgMu.Unlock()
	if l.pkgEntries == nil {
		l.pkgEntries = map[string]*pkgEntry{}
	}
	old := l.pkgEntries[key]
	if e != nil {
		l.pkgEntries[key] = e
	} else {
		delete(l.pkgEntries, key)
	}
	if old == nil || old == e {
		return
	}
	gone := map[*pkgEntry]bool{old: true}
	for changed := true; changed; {
		changed = false
		for k, kept := range l.pkgEntries {
			if slices.ContainsFunc(kept.deps, func(d *pkgEntry) bool { return gone[d] }) {
				gone[kept] = true
				delete(l.pkgEntries, k)
				changed = true
			}
		}
	}
}
