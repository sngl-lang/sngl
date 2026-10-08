package codegen

import (
	"fmt"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
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
// Its #[gen.name] is the identifier the plugin registered under, which is how
// a build directive reaches it; the component itself is named for its tier.
func buildNode(pkg, name string) (*ir.Component, error) {
	p := checker.LibPackage(pkg)
	if p == nil {
		return nil, fmt.Errorf("no package sngl:%s", pkg)
	}
	for _, c := range p.Components {
		if _, got, ok := ir.TargetNode(c); ok && got == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("sngl:%s declares no build-target node named %q, so the target says nothing about what it generates", pkg, name)
}

// CapsOrNone is CapsFor for a caller with nowhere to put an error, which after
// the migration means the tests: each holds a generator and a translator
// rather than two names, and an unresolvable pair there is a broken checkout
// rather than a case to handle. Every caller that can report uses CapsFor.
//
// The zero Features it answers with is not a fallback invented here. It is the
// polarity the marks declare, applied to a target that said nothing at all --
// every lowering pass runs, which is the answer that leaves a program correct.
// A registered target cannot reach it anyway: its build node is part of the
// plugin, and a build that could not resolve one would have failed selecting
// the target.
func CapsOrNone(language, platform string) lower.Features {
	f, err := CapsFor(language, platform)
	if err != nil {
		return lower.Features{}
	}
	return f
}
