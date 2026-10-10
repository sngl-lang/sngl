package main

import (
	"fmt"
	"html"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// buildMarker is the placeholder website.sngl's PageLayout footer emits for
// the build stamp. See downloadsMarker for the same convention.
const buildMarker = "<!-- sngl:build -->"

// yearMarker is the placeholder in the footer's copyright line.
const yearMarker = "<!-- sngl:year -->"

// repoURL is where a commit hash in the footer links to.
const repoURL = "https://github.com/sngl-lang/sngl"

// stamp is the commit the site was generated from.
type stamp struct {
	commit   string // full hash; "" when the revision is unknown
	when     time.Time
	modified bool // uncommitted changes were present at build time
}

func (s stamp) short() string {
	if len(s.commit) > 7 {
		return s.commit[:7]
	}
	return s.commit
}

// resolveStamp reads the VCS metadata the toolchain embeds in the binary,
// falling back to git. The fallback is not redundant: only `go build`,
// `go install`, and `go tool` stamp VCS info — `go run ./internal/cmd/docsgen`
// does not, and a site built that way should still say which commit it is.
func resolveStamp() stamp {
	var s stamp
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range bi.Settings {
			switch kv.Key {
			case "vcs.revision":
				s.commit = kv.Value
			case "vcs.time":
				if t, err := time.Parse(time.RFC3339, kv.Value); err == nil {
					s.when = t
				}
			case "vcs.modified":
				s.modified = kv.Value == "true"
			}
		}
	}
	if s.commit != "" {
		return s
	}
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		s.commit = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "show", "-s", "--format=%cI", "HEAD").Output(); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out))); err == nil {
			s.when = t
		}
	}
	if out, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		s.modified = strings.TrimSpace(string(out)) != ""
	}
	return s
}

// footerHTML renders the stamp as the footer's build line. An unknown revision
// renders as nothing so the footer degrades to just its links.
func (s stamp) footerHTML() string {
	if s.commit == "" {
		return ""
	}
	label := s.short()
	if s.modified {
		label += "-dirty"
	}
	link := fmt.Sprintf(`<a href="%s/commit/%s">%s</a>`,
		repoURL, html.EscapeString(s.commit), html.EscapeString(label))
	if s.when.IsZero() {
		return "built from " + link
	}
	return fmt.Sprintf(`built from %s on <time datetime="%s">%s</time>`,
		link, s.when.UTC().Format(time.RFC3339), s.when.UTC().Format("2006-01-02"))
}

// copyrightYear is the year the footer's notice carries: the commit's, so a
// rebuilt-but-unchanged site keeps saying the same thing, and today's when the
// revision is unknown.
func (s stamp) copyrightYear() string {
	when := s.when
	if when.IsZero() {
		when = time.Now()
	}
	return strconv.Itoa(when.UTC().Year())
}

// injectFooter fills the build and year markers on every generated page.
func injectFooter(outDir string, s stamp) error {
	frag := s.footerHTML()
	year := s.copyrightYear()
	count := 0
	err := filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), buildMarker) && !strings.Contains(string(data), yearMarker) {
			return nil
		}
		out := strings.ReplaceAll(string(data), buildMarker, frag)
		out = strings.ReplaceAll(out, yearMarker, year)
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count == 0 {
		log.Printf("footer: markers %q / %q not found in any page", buildMarker, yearMarker)
		return nil
	}
	log.Printf("footer: build stamp injected into %d pages", count)
	return nil
}
