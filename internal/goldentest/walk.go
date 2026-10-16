package goldentest

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// walkDivergence reports where ir.Walk and a no-op ir.Rewrite first visit pkg
// differently, or nil. ir's own tests pin the two against synthetic IR; this
// holds them to every fixture's real IR, checked and lowered.
func walkDivergence(pkg *ir.Package) error {
	controls := []struct {
		name string
		ctl  func(i int) error
	}{
		{"all", func(int) error { return nil }},
		{"skipdir", func(i int) error {
			if i%5 == 2 {
				return ir.SkipDir
			}
			return nil
		}},
	}
	for _, c := range controls {
		var walked, rewritten []ir.Node
		_ = ir.Walk(pkg, func(n ir.Node) error {
			walked = append(walked, n)
			return c.ctl(len(walked) - 1)
		})
		_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
			rewritten = append(rewritten, n)
			return n, c.ctl(len(rewritten) - 1)
		})
		for i := range min(len(walked), len(rewritten)) {
			if walked[i] != rewritten[i] {
				return fmt.Errorf("%s: ir.Walk and ir.Rewrite diverge at visit %d: %T against %T", c.name, i, walked[i], rewritten[i])
			}
		}
		if len(walked) != len(rewritten) {
			return fmt.Errorf("%s: ir.Walk made %d visits, ir.Rewrite %d", c.name, len(walked), len(rewritten))
		}
	}
	return nil
}

// ownContexts is pkg without the frozen contexts it adopted, a library's or a
// kept directory package's, which ir.Rewrite leaves alone: every check in the
// process reads them.
func ownContexts(pkg *ir.Package) *ir.Package {
	view := *pkg
	view.Contexts = nil
	for _, ctx := range pkg.Contexts {
		if !ctx.Frozen {
			view.Contexts = append(view.Contexts, ctx)
		}
	}
	return &view
}
