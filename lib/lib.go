// Package lib embeds the SNGL standard library source so it can be read at
// runtime without depending on the filesystem layout.
//
// Each subdirectory is one importable package: lib/<path> is `sngl:<path>`.
// Nothing enumerates them in Go — add a directory and it exists.
//
//	builtin/    ambient built-ins, dot-imported into every file implicitly
//	ui/         the components an application is built from, and the Style
//	            and event vocabulary every one of them refers to
//	ui/draw/    the 2D canvas and its shapes, one specialised surface under ui
//	app/        window, timer, errorBoundary and the error they carry
//	dialog/     host-native notifications and file choosers
//	test/       the receiver a test function's first parameter carries
//	i18n/       the translation surface `$"..."` lowers to
//	macro/      the marks a package writes to describe its own declarations
//	internal/   the compiler's own primitives, declared so that what the
//	            compiler knows about them is written where they are
//
// A target's package is not here: `sngl:platform/<name>` and
// `sngl:language/<name>` are served by the registered plugin, whose source
// sits beside it.

package lib

import (
	"embed"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

//go:embed */*.sngl */*/*.sngl
var FS embed.FS

// Packages returns the embedded package paths, sorted. A directory holding
// .sngl files is a package, at whatever depth — lib/internal/stdlib is
// `sngl:internal/stdlib`. Order is not load order: packages import each
// other and are loaded on demand.
func Packages() []string {
	seen := map[string]bool{}
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sngl") {
			return nil
		}
		seen[path.Dir(p)] = true
		return nil
	})
	if err != nil {
		panic("sngl: reading embedded stdlib: " + err.Error())
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// PublicPackages returns the packages a program is meant to import, dropping
// the compiler's own tier under internal/. Documentation indexes them; name
// one explicitly and it still resolves, as an internal Go package does.
//
// A per-target package under platforms/ or languages/ is dropped for a
// different reason: they are mutually exclusive, so indexing them alongside
// the library would make a bare name like `Options` ambiguous between six
// packages that can never be imported together. The docs reach them through
// the plugin registry instead. The `platforms` and `languages` packages
// themselves are ordinary public vocabulary and stay.
func PublicPackages() []string {
	perTarget := []string{"platform", "language"}
	out := make([]string, 0, len(Packages()))
	for _, p := range Packages() {
		if p == "internal" || strings.HasPrefix(p, "internal/") {
			continue
		}
		if slices.ContainsFunc(perTarget, func(t string) bool { return strings.HasPrefix(p, t+"/") }) {
			continue
		}
		out = append(out, p)
	}
	return out
}
