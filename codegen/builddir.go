package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// buildDirTTL is how long an unused build directory is kept before the
// lazy sweep in pruneBuildDirs removes it.
const buildDirTTL = 7 * 24 * time.Hour

// buildDirSlots bounds how many concurrent holders one key can have.
// Each slot is a distinct stable path, so concurrent builds of the same
// key still get cache-friendly directories instead of serialising.
// Slots past the first are only created under contention.
const buildDirSlots = 8

var buildDirLocks sync.Map // dir -> *sync.Mutex

// BuildDir returns a stable working directory for generated code plus a
// release func that must be called when the caller is done with it.
//
// Why stable rather than os.MkdirTemp: the Go build cache keys compile and
// link actions on the absolute source directory, so byte-identical generated
// code emitted into a fresh temp dir misses the cache every single time and
// pays a full compile + link. Reusing one path per logical build turns a
// 1.4s fyne link into 0.07s and a 1.8s cgo gtk4 compile into 0.03s.
//
// kind groups directories by purpose ("test", "run", "snapshot", …); key
// identifies the logical build within that kind (fixture path, platform,
// lang, test group, …). The directory is emptied before it is returned, so
// callers always start from a clean tree; only the path is reused.
//
// Set SNGL_BUILD_CACHE=0 to opt out and get a throwaway temp dir instead.
func BuildDir(kind string, key ...string) (dir string, release func(), err error) {
	if os.Getenv("SNGL_BUILD_CACHE") == "0" {
		d, err := os.MkdirTemp("", "sngl-"+kind+"-*")
		if err != nil {
			return "", nil, err
		}
		return d, func() { os.RemoveAll(d) }, nil
	}

	root := filepath.Join(SnglCacheDir(), "build", kind)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", nil, err
	}
	pruneBuildDirs(root)

	sum := sha256.Sum256([]byte(strings.Join(key, "\x00")))
	base := hex.EncodeToString(sum[:10])

	for slot := range buildDirSlots {
		name := base
		if slot > 0 {
			name = fmt.Sprintf("%s-%d", base, slot)
		}
		d := filepath.Join(root, name)
		rel, ok := tryLockBuildDir(d)
		if !ok {
			continue
		}
		if err := resetDirContents(d); err != nil {
			rel()
			return "", nil, err
		}
		// Refresh mtime so the sweep in pruneBuildDirs treats an
		// in-use-but-empty directory as live.
		now := time.Now()
		os.Chtimes(d, now, now)
		return d, rel, nil
	}

	// Every slot is busy — fall back to a throwaway dir rather than block.
	d, err := os.MkdirTemp("", "sngl-"+kind+"-*")
	if err != nil {
		return "", nil, err
	}
	return d, func() { os.RemoveAll(d) }, nil
}

// tryLockBuildDir takes the in-process and cross-process locks for dir,
// creating it if needed. Returns false when another holder has it.
func tryLockBuildDir(dir string) (release func(), ok bool) {
	mu, _ := buildDirLocks.LoadOrStore(dir, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.Unlock()
		return nil, false
	}
	unlockFile, ok := lockBuildDirFile(dir + ".lock")
	if !ok {
		m.Unlock()
		return nil, false
	}
	return func() {
		unlockFile()
		m.Unlock()
	}, true
}

// resetDirContents empties dir without removing dir itself, so the path
// stays stable for the build cache.
func resetDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

var pruneOnce sync.Map // root -> *sync.Once

// pruneBuildDirs removes build directories under root that have not been
// touched within buildDirTTL, along with the sibling files that belong to
// them (lock files and toolchain outputs parked next to the directory).
// Runs at most once per root per process.
func pruneBuildDirs(root string) {
	o, _ := pruneOnce.LoadOrStore(root, &sync.Once{})
	o.(*sync.Once).Do(func() {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		cutoff := time.Now().Add(-buildDirTTL)

		live := map[string]bool{}
		var stale []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().After(cutoff) {
				live[e.Name()] = true
				continue
			}
			stale = append(stale, e.Name())
		}

		for _, name := range stale {
			dir := filepath.Join(root, name)
			rel, ok := tryLockBuildDir(dir)
			if !ok {
				live[name] = true
				continue
			}
			os.RemoveAll(dir)
			rel()
		}

		// Sibling files are named "<dir>.lock", "<dir>.testagent", …;
		// drop the ones whose directory is gone.
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			owner, _, found := strings.Cut(e.Name(), ".")
			if !found || live[owner] {
				continue
			}
			os.Remove(filepath.Join(root, e.Name()))
		}
	})
}
