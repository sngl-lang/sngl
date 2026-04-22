package docs

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestTutorial(t *testing.T) {
	sections := Tutorial()
	if len(sections) == 0 {
		t.Fatal("Tutorial() returned no sections")
	}

	seenBasics := false
	totalLessons := 0
	for _, s := range sections {
		if s.Title == "" || s.Slug == "" {
			t.Errorf("section missing title/slug: %+v", s)
		}
		if s.Title == "Basics" {
			seenBasics = true
		}
		for _, l := range s.Lessons {
			totalLessons++
			if l.Title == "" {
				t.Errorf("lesson in %q missing title", s.Title)
			}
			if !strings.HasPrefix(l.Slug, s.Slug+"/") {
				t.Errorf("lesson slug %q does not belong to section %q", l.Slug, s.Slug)
			}
			if l.Code == "" {
				t.Errorf("lesson %q has empty seed code", l.Slug)
			}
			if _, err := parser.Parse("lesson.sngl", []byte(l.Code)); err != nil {
				t.Errorf("lesson %q seed does not parse: %v", l.Slug, err)
			}
			if l.BodyHTML == "" {
				t.Errorf("lesson %q has empty body HTML", l.Slug)
			}
		}
	}
	if !seenBasics {
		t.Error("expected a Basics section")
	}
	if totalLessons < 5 {
		t.Errorf("expected ≥5 lessons, got %d", totalLessons)
	}
}
