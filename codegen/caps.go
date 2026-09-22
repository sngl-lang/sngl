package codegen

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CapsFor reads a target pair's lowering capabilities off the build-tree nodes
// their packages declare -- the `#[gen.can]`, `#[gen.cannot]` and `#[gen.wants]`
// marks on `component go(…) build.language` and `component html(…) build.platform`.
//
// By name rather than from an `*ir.Output`, though that record holds the two
// components already. A target does not always come from an output block:
// `--lang`/`--platform` on the command line selects one with no directive
// behind it, and `internal/build.Target` is therefore two strings. Loading the
// packages is memoized, and the components found here are the same pointers an
// Output carries.
func CapsFor(language, platform string) (lower.Features, error) {
	langComp, err := buildNode("language/"+language, language)
	if err != nil {
		return lower.Features{}, err
	}
	platComp, err := buildNode("platform/"+platform, platform)
	if err != nil {
		return lower.Features{}, err
	}
	return lower.FeaturesFrom(langComp, platComp)
}

// buildNode finds the component a target package declares for the build tree.
// It is named for the target, which is how a build directive reaches it, so
// the identifier the plugin registered under is the name to look up.
func buildNode(pkg, name string) (*ir.Component, error) {
	p := checker.LibPackage(pkg)
	if p == nil {
		return nil, fmt.Errorf("no package sngl:%s", pkg)
	}
	for _, c := range p.Components {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("sngl:%s declares no component %q, so the target says nothing about what it generates", pkg, name)
}
