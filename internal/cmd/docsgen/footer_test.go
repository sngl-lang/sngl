package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInjectFooter(t *testing.T) {
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "learn"), 0o755); err != nil {
		t.Fatal(err)
	}
	pages := []string{
		filepath.Join(out, "index.html"),
		filepath.Join(out, "learn", "installation.html"),
	}
	for _, p := range pages {
		body := `<footer class="site-footer"><span class="copyright">&copy; ` + yearMarker +
			` Jonathan Duck. All rights reserved.</span><span class="build-stamp">` +
			buildMarker + `</span></footer>`
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := stamp{
		commit: "7a599b700a2eb78fa4d2f73bba9a94d164949ddc",
		when:   time.Date(2026, 8, 23, 17, 59, 40, 0, time.UTC),
	}
	if err := injectFooter(out, s); err != nil {
		t.Fatalf("injectFooter: %v", err)
	}

	for _, p := range pages {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got := string(data)
		if strings.Contains(got, buildMarker) || strings.Contains(got, yearMarker) {
			t.Errorf("%s: marker not replaced", p)
		}
		for _, want := range []string{
			"7a599b7",
			`href="https://git.duckfam.us/jonathan/sngl/commit/7a599b700a2eb78fa4d2f73bba9a94d164949ddc"`,
			"2026-08-23",
			"&copy; 2026 Jonathan Duck. All rights reserved.",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: footer missing %q\ngot: %s", p, want, got)
			}
		}
	}
}

func TestFooterHTMLDirty(t *testing.T) {
	s := stamp{commit: "abcdef1234567890", modified: true}
	got := s.footerHTML()
	if !strings.Contains(got, "abcdef1-dirty") {
		t.Errorf("dirty tree not marked: %s", got)
	}
	if strings.Contains(got, " on ") {
		t.Errorf("no commit time known, but a date was rendered: %s", got)
	}
}

// An unknown revision must render nothing, so the footer keeps its links
// rather than gaining an empty "built from" line.
func TestFooterHTMLUnknownRevision(t *testing.T) {
	if got := (stamp{}).footerHTML(); got != "" {
		t.Errorf("footerHTML() = %q, want empty", got)
	}
}

// With no commit time, the notice still needs a year — the build's own.
func TestCopyrightYearFallsBackToToday(t *testing.T) {
	want := strconv.Itoa(time.Now().UTC().Year())
	if got := (stamp{}).copyrightYear(); got != want {
		t.Errorf("copyrightYear() = %q, want %q", got, want)
	}
}

func TestResolveStamp(t *testing.T) {
	s := resolveStamp()
	if len(s.commit) != 40 {
		t.Errorf("commit = %q, want a 40-char hash (run inside the git checkout)", s.commit)
	}
	if s.when.IsZero() {
		t.Error("commit time unresolved")
	}
}
