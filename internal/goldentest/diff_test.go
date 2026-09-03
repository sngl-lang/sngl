package goldentest

import "strings"

import "testing"

// firstDiff walks to max(len(want), len(got)), so its backwards context window
// can start past the end of want. A golden seeded shorter than the file now
// generated, sharing a prefix, used to panic the whole test binary here --
// taking every other fixture's result with it, in place of one diff.
func TestFirstDiffSurvivesAShorterWant(t *testing.T) {
	cases := [][2]string{
		{"a\nb", "a\nb\n\n\n\nX"},
		{"", "a\nb\nc\nd\ne"},
		{"a\nb\nc\nd\ne", ""},
		{"same", "same"},
	}
	for _, c := range cases {
		got := firstDiff(c[0], c[1])
		if c[0] == c[1] && !strings.Contains(got, "identical") {
			t.Errorf("firstDiff(%q, %q) = %q, want the identical-lines note", c[0], c[1], got)
		}
	}
}
