package gtk4

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

// sngdir is the written half of sngl://platforms/gtk4: the overrides and
// primitives this platform declares by hand. See
// codegen/platform/html/packagefs.go for why a plugin carries its own source.
//
//go:embed sngl/*.sngl
var sngdir embed.FS

// PackageFS is the whole of sngl://platforms/gtk4: what this package embeds,
// plus one component declaration per GTK widget class the host's introspection
// data describes.
//
// That gtk4 generates half of its own package is gtk4's business. The checker
// asks every target for an fs.FS and reads whatever it gets, so nothing outside
// this file knows the difference between a declaration written here and one
// derived from the host -- which is what will let a target serve its package
// from somewhere else entirely.
func (g *Generator) PackageFS() fs.FS {
	written, err := fs.Sub(sngdir, "sngl")
	if err != nil {
		return nil
	}
	reg, err := g.gir()
	if err != nil {
		// No introspection data, so the package is withheld whole rather than
		// served half. Its overrides are written against gtk4.Gtk* widgets the
		// generated half declares, and mergePlatformExtensions walks every
		// registered platform's source -- so serving the written half alone
		// would report those widgets as undefined in a build targeting some
		// other platform entirely.
		return nil
	}
	_ = written
	g.fsOnce.Do(func() {
		g.pkgFS = fstest.MapFS{widgetSourceFile: &fstest.MapFile{Data: widgetSource(reg)}}
	})
	return mergedFS{written: written, generated: g.pkgFS}
}

// mergedFS reads one package out of two filesystems. The generated half wins a
// name collision, which cannot happen today -- the written half is one file and
// the generated one is another -- but says which is authoritative if it ever
// does.
type mergedFS struct {
	written   fs.FS
	generated fs.FS
}

func (m mergedFS) Open(name string) (fs.File, error) {
	if f, err := m.generated.Open(name); err == nil {
		return f, nil
	}
	return m.written.Open(name)
}

func (m mergedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	seen := map[string]bool{}
	var out []fs.DirEntry
	for _, src := range []fs.FS{m.generated, m.written} {
		entries, err := fs.ReadDir(src, name)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, fs.ErrNotExist
	}
	return out, nil
}
