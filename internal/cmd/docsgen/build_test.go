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
func TestWebsiteProducesContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping website build (compiles wasm) in -short mode")
	}

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
