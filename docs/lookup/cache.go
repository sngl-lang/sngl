package lookup

import (
	"strings"
	"sync"
)

// Memoize Lookup/Index results so TUI renders that hit Resolve dozens of
// times per frame don't re-parse the same package on every call. Entries are
// keyed by inputs (cwd, path, idents) and never expire on their own.

type lookupKey struct {
	cwd    string
	path   string
	idents string
}

type lookupEntry struct {
	res Result
	err error
}

var (
	lookupCacheMu sync.RWMutex
	lookupCache   = map[lookupKey]lookupEntry{}

	indexCacheMu sync.RWMutex
	indexCache   = map[string][]PackageRef{}
)

func cachedLookup(cwd, path string, idents []string) (Result, error, bool) {
	k := lookupKey{cwd: cwd, path: path, idents: strings.Join(idents, "\x00")}
	lookupCacheMu.RLock()
	e, ok := lookupCache[k]
	lookupCacheMu.RUnlock()
	if !ok {
		return Result{}, nil, false
	}
	return e.res, e.err, true
}

func storeLookup(cwd, path string, idents []string, res Result, err error) {
	k := lookupKey{cwd: cwd, path: path, idents: strings.Join(idents, "\x00")}
	lookupCacheMu.Lock()
	lookupCache[k] = lookupEntry{res: res, err: err}
	lookupCacheMu.Unlock()
}

func cachedIndex(cwd string) ([]PackageRef, bool) {
	indexCacheMu.RLock()
	v, ok := indexCache[cwd]
	indexCacheMu.RUnlock()
	return v, ok
}

func storeIndex(cwd string, refs []PackageRef) {
	indexCacheMu.Lock()
	indexCache[cwd] = refs
	indexCacheMu.Unlock()
}
