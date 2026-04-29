package js

import (
	"errors"
	"io/fs"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/sngl-lang/typescript-go/snglts"
)

// vfsAdapter satisfies snglts.FS over an io/fs.FS rooted at a synthetic
// absolute path. typescript-go's resolver works in absolute paths
// (`/sngl/foo.ts`); io/fs.FS uses unrooted slash paths (`foo.ts`).
// strip() bridges the two.
type vfsAdapter struct {
	fsys fs.FS
	root string // absolute virtual root, must start with "/", no trailing "/"
}

func newVFSAdapter(fsys fs.FS, root string) *vfsAdapter {
	root = strings.TrimRight(path.Clean(root), "/")
	if root == "" || root == "." {
		root = "/"
	}
	return &vfsAdapter{fsys: fsys, root: root}
}

// strip turns an absolute virtual path under root into the corresponding
// io/fs.FS-relative slash path. Returns "", false for paths outside the
// root. The empty relative path becomes ".".
func (a *vfsAdapter) strip(p string) (string, bool) {
	clean := path.Clean(p)
	if clean == a.root {
		return ".", true
	}
	prefix := a.root
	if prefix != "/" {
		prefix += "/"
	}
	if !strings.HasPrefix(clean, prefix) {
		return "", false
	}
	return strings.TrimPrefix(clean, prefix), true
}

// host bundles a vfsAdapter and the cwd to satisfy module.ResolutionHost.
type host struct {
	fsys *vfsAdapter
	cwd  string
}

func (h *host) FS() snglts.FS               { return h.fsys }
func (h *host) GetCurrentDirectory() string { return h.cwd }

// vfs.FS implementation.

func (a *vfsAdapter) UseCaseSensitiveFileNames() bool {
	return runtime.GOOS != "windows" && runtime.GOOS != "darwin"
}

func (a *vfsAdapter) FileExists(p string) bool {
	rel, ok := a.strip(p)
	if !ok {
		return false
	}
	info, err := fs.Stat(a.fsys, rel)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (a *vfsAdapter) ReadFile(p string) (string, bool) {
	rel, ok := a.strip(p)
	if !ok {
		return "", false
	}
	data, err := fs.ReadFile(a.fsys, rel)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (a *vfsAdapter) DirectoryExists(p string) bool {
	rel, ok := a.strip(p)
	if !ok {
		return false
	}
	info, err := fs.Stat(a.fsys, rel)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (a *vfsAdapter) Stat(p string) snglts.FileInfo {
	rel, ok := a.strip(p)
	if !ok {
		return nil
	}
	info, err := fs.Stat(a.fsys, rel)
	if err != nil {
		return nil
	}
	return info
}

func (a *vfsAdapter) GetAccessibleEntries(p string) snglts.Entries {
	rel, ok := a.strip(p)
	if !ok {
		return snglts.Entries{}
	}
	entries, err := fs.ReadDir(a.fsys, rel)
	if err != nil {
		return snglts.Entries{}
	}
	out := snglts.Entries{}
	for _, e := range entries {
		if e.IsDir() {
			out.Directories = append(out.Directories, e.Name())
		} else {
			out.Files = append(out.Files, e.Name())
		}
	}
	return out
}

func (a *vfsAdapter) WalkDir(root string, walkFn snglts.WalkDirFunc) error {
	rel, ok := a.strip(root)
	if !ok {
		return fs.ErrNotExist
	}
	return fs.WalkDir(a.fsys, rel, func(p string, d fs.DirEntry, err error) error {
		// Re-rebase the visited path back into the virtual root so the
		// caller sees absolute paths consistent with what it passed in.
		var virt string
		if p == "." {
			virt = a.root
		} else if a.root == "/" {
			virt = "/" + p
		} else {
			virt = a.root + "/" + p
		}
		return walkFn(virt, d, err)
	})
}

// Realpath is identity: io/fs.FS has no symlink concept, and the
// in-memory / project trees we serve never have symlinks worth resolving.
func (a *vfsAdapter) Realpath(p string) string { return p }

// Write methods are unused by module.Resolver (verified by inspection).
// Return ENOTSUP to make accidental writes loud.
func (a *vfsAdapter) WriteFile(_, _ string) error                      { return errReadOnly }
func (a *vfsAdapter) AppendFile(_, _ string) error                     { return errReadOnly }
func (a *vfsAdapter) Remove(_ string) error                            { return errReadOnly }
func (a *vfsAdapter) Chtimes(_ string, _ time.Time, _ time.Time) error { return errReadOnly }

var errReadOnly = errors.New("vfsAdapter: read-only")
