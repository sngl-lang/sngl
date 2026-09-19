package goldentest

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"testing"
)

// A deny is one assertion of absence: this pattern must not appear in this
// generated file.
//
// A golden shows absence but does not state it, and no reader reliably
// notices that something is not there. `! grep 'held := \[\]int'` said
// "nothing materialises a list to walk a sequence" — that claim is the point
// of the change, and a reviewer reading the golden has no way to know it was
// ever being made. So the claim stays, written down, with its reason.
//
// It is not rsc.io/script's `grep` under `!`, deliberately: that spelling is
// what let six assertions silently pass. Here a directive naming no file, no
// pattern, no reason, a file the target does not generate, or a pattern that
// does not compile is a test failure. The only way for a deny to pass is for
// the pattern to compile, the file to exist, and the match to be absent.
type deny struct {
	line    int
	file    string
	pattern *regexp.Regexp
	reason  string
}

// parseDenies reads the `deny` directives out of the archive comment.
//
//	deny out/go/bubbletea/model.go `held := \[\]int` -- nothing materialises a list
//	deny * `\.toList\(\)` -- no target copies a progression into a list
//
// The pattern is backquoted so a generated-code regex needs no escaping past
// what the regex itself wants, and it matches against the whole file rather
// than line by line -- a line-anchored claim writes its own `(?m)`. `*` as the
// file means every generated file of every target.
func parseDenies(comment string) ([]deny, error) {
	var out []deny
	for i, raw := range strings.Split(comment, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		rest, ok := strings.CutPrefix(line, "deny")
		if !ok || (rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t")) {
			continue
		}
		d, err := parseDeny(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w\n\t%s", i+1, err, line)
		}
		d.line = i + 1
		out = append(out, d)
	}
	return out, nil
}

func parseDeny(rest string) (deny, error) {
	const form = "want: deny <out/lang/platform/file|*> `pattern` -- reason"

	file, rest, ok := strings.Cut(rest, " ")
	if !ok || file == "" {
		return deny{}, fmt.Errorf("deny names no pattern (%s)", form)
	}
	if file != "*" {
		if !strings.HasPrefix(file, goldenPrefix) || strings.Count(path.Clean(file), "/") < 3 {
			return deny{}, fmt.Errorf("deny file %q is not a golden path (%s)", file, form)
		}
	}

	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "`") {
		return deny{}, fmt.Errorf("deny pattern must be backquoted (%s)", form)
	}
	pat, rest, ok := strings.Cut(rest[1:], "`")
	if !ok {
		return deny{}, fmt.Errorf("deny pattern has no closing backquote (%s)", form)
	}
	if pat == "" {
		return deny{}, fmt.Errorf("deny pattern is empty (%s)", form)
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return deny{}, fmt.Errorf("deny pattern does not compile: %w", err)
	}

	reason := strings.TrimSpace(rest)
	reason = strings.TrimSpace(strings.TrimPrefix(reason, "--"))
	if reason == "" {
		return deny{}, fmt.Errorf("deny states no reason (%s)", form)
	}

	return deny{file: file, pattern: re, reason: reason}, nil
}

func checkDenies(t *testing.T, denies []deny, got map[string][]byte, boilerplate map[string]bool) {
	t.Helper()
	for _, d := range denies {
		targets := []string{d.file}
		if d.file == "*" {
			// `*` is every file generated *from the program*. The scaffold a
			// platform writes around it is not the program's output and a
			// claim about codegen is not a claim about it -- android's
			// manifest carries `android:label="App"`, which matched a fixture
			// denying `\blabel\b` about a prop name it has nothing to do
			// with. A deny that does mean the scaffold names the file.
			targets = sortedKeys(reviewable(got, boilerplate))
		}
		for _, name := range targets {
			data, ok := got[name]
			if !ok {
				// A deny naming a file nothing generates asserts nothing, and
				// looks exactly like one that asserts something — which is the
				// whole class of bug this harness replaces.
				t.Errorf("comment line %d: deny names %s, which no target generates", d.line, name)
				continue
			}
			if loc := d.pattern.FindIndex(data); loc != nil {
				t.Errorf("comment line %d: %s matches %s (%s):\n\t%s",
					d.line, name, d.pattern, d.reason, lineAt(data, loc[0]))
			}
		}
	}
}

func lineAt(data []byte, off int) string {
	start := 0
	for i := off - 1; i >= 0; i-- {
		if data[i] == '\n' {
			start = i + 1
			break
		}
	}
	end := len(data)
	for i := off; i < len(data); i++ {
		if data[i] == '\n' {
			end = i
			break
		}
	}
	return strings.TrimSpace(string(data[start:end]))
}
