package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebsiteProducesContent is a sanity check that compiling website.sngl
// emits real page content — not just the nav sidebar. The site's body is
// driven by go:// imports (docs.ComponentsByTier etc.) that are evaluated by
// compiling those packages to wasm; if that evaluation breaks, the sidebar
// (also data-driven) can still render while every page body comes up empty.
// This test guards against that whole-site-goes-blank regression.
//
// It does not skip in -short, unlike the other heavy tests: those need a
// browser, an Android SDK or a display, and this needs none of them. It is
// merely slow -- ~80s, of which the wasm evaluation its old skip message
// blamed is 6s and emitting the 789 pages is the rest. That is ~11% on a
// twelve-minute run, against a regression that otherwise reaches the default
// branch and breaks the published docs, because `pages` is the only other job
// that builds the site and it runs nowhere else.
func TestWebsiteProducesContent(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "website.sngl")); err != nil {
		t.Fatalf("website.sngl not found at repo root %s: %v", repoRoot, err)
	}

	out := t.TempDir()
	cmd := exec.Command("go", "tool", "sngl", "generate",
		"--platform", "html", "--lang", "none", "--out", out, "website.sngl")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building website.sngl failed: %v\n%s", err, combined)
	}

	page := filepath.Join(out, "components", "index.html")
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("reading generated %s: %v", page, err)
	}
	html := string(data)

	// Static body text from PageLayout's slot — present even with empty data.
	if !strings.Contains(html, "Browse all built-in components") {
		t.Error("components/index.html missing static page body text")
	}
	// Data-driven body content from the go:// `tiers` const. Its absence is
	// the "sidebar renders but content missing" symptom: the sidebar uses
	// NavItem/NavGroup, only the page body emits component-card.
	if !strings.Contains(html, "component-card") {
		t.Error("components/index.html has no data-driven content (component cards) — page body is empty")
	}
	// The site's stylesheet (declared via output { none { html(stylesheet=...) } })
	// must be linked. It was lost when CLI --platform/--lang flags discarded the
	// declared output options.
	if !strings.Contains(html, `rel="stylesheet"`) {
		t.Error("components/index.html has no <link rel=\"stylesheet\"> — site CSS dropped")
	}
}

// TestWebsiteTypeChecks is TestWebsiteProducesContent's cheap half, and the
// only one that runs in -short mode -- which is the mode CI's `go tool verify
// -dry` uses. Checking the site against the html platform costs under a
// second, because the go:// consts are evaluated by the optimizer and this
// stops at the checker; the full build compiles them to wasm and costs a
// minute, which is why it skips.
//
// Without this the website is only ever built by the `pages` job, and `pages`
// runs on the default branch alone: a type error in a `platform html` body
// reached main and broke the docs deploy, having passed every pipeline on the
// way in.
func TestWebsiteTypeChecks(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// The platform must be named: a `platform html { ... }` body is checked
	// against the html package only when html is a target of the build, so
	// `sngl check` with no platform walks straight past the thing that broke.
	cmd := exec.Command("go", "tool", "sngl", "dump", "--stage", "checked",
		"--platform", "html", "--lang", "none", "website.sngl")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("website.sngl does not type-check against the html platform: %v\n%s", err, combined)
	}
}
