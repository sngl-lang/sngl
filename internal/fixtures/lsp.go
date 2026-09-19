package fixtures

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// assertMarkers checks the //@ directives an lsp_ fixture carries.
func assertMarkers(t *testing.T, s testutil.Sample) {
	content := s.Source
	markers := parseMarkers(content)

	doc, diags := lspcore.Analyze(content, s.Filename, s.FS, s.Dir, nil)

	// Collect diag markers by line
	diagExpected := map[int][]string{} // line (1-based) → expected substrings
	for _, m := range markers {
		if m.kind == "diag" {
			if len(m.args) < 1 {
				t.Errorf("line %d: diag marker needs at least 1 arg", m.line)
				continue
			}
			diagExpected[m.line] = append(diagExpected[m.line], m.args[0])
		}
	}

	// Check expected diagnostics are present
	diagsByLine := map[int][]lspcore.Diagnostic{}
	for _, d := range diags {
		line := d.Range.Start.Line + 1 // 0-based → 1-based
		diagsByLine[line] = append(diagsByLine[line], d)
	}

	for line, expected := range diagExpected {
		got := diagsByLine[line]
		for _, substr := range expected {
			found := false
			for _, d := range got {
				if strings.Contains(d.Message, substr) {
					found = true
					break
				}
			}
			if !found {
				msgs := make([]string, len(got))
				for i, d := range got {
					msgs[i] = d.Message
				}
				t.Errorf("line %d: expected diagnostic containing %q, got %v", line, substr, msgs)
			}
		}
	}

	// Check no unexpected diagnostics on unmarked lines
	for line, got := range diagsByLine {
		if _, ok := diagExpected[line]; ok {
			continue
		}
		for _, d := range got {
			t.Errorf("line %d: unexpected diagnostic: %s", line, d.Message)
		}
	}
	// Diagnostics at line 0 (no position) are unexpected unless there's a line-0 marker
	for _, d := range diags {
		if d.Range.Start.Line == 0 && d.Range.Start.Character == 0 {
			if _, ok := diagExpected[0]; !ok {
				// Allow line-0 diags only if they were explicitly expected at some line
			}
		}
	}

	// Run hover markers
	for _, m := range markers {
		if m.kind != "hover" {
			continue
		}
		if len(m.args) < 2 {
			t.Errorf("line %d: hover marker needs 2 args (word, expected)", m.line)
			continue
		}
		word := m.args[0]
		expected := m.args[1]

		lines := strings.Split(content, "\n")
		if m.line < 1 || m.line > len(lines) {
			t.Errorf("line %d: out of range", m.line)
			continue
		}
		lineText := lines[m.line-1]
		col := strings.Index(lineText, word)
		if col < 0 {
			t.Errorf("line %d: word %q not found on line", m.line, word)
			continue
		}
		col++ // 0-based → 1-based

		result := lspcore.Hover(content, doc, m.line, col)
		if result == "" {
			t.Errorf("line %d: hover(%q) returned empty", m.line, word)
			continue
		}
		if !strings.Contains(result, expected) {
			t.Errorf("line %d: hover(%q) = %q, want substring %q", m.line, word, result, expected)
		}
	}

	// Run completion markers
	for _, m := range markers {
		if m.kind != "complete" {
			continue
		}
		if len(m.args) < 2 {
			t.Errorf("line %d: complete marker needs at least 2 args (context, items...)", m.line)
			continue
		}
		ctx := m.args[0]

		lines := strings.Split(content, "\n")
		if m.line < 1 || m.line > len(lines) {
			t.Errorf("line %d: out of range", m.line)
			continue
		}
		lineText := lines[m.line-1]
		col := 1
		if ctx != "" {
			idx := strings.Index(lineText, ctx)
			if idx < 0 {
				t.Errorf("line %d: context %q not found on line", m.line, ctx)
				continue
			}
			col = idx + len(ctx) + 1 // end of context, 1-based
		}

		items := lspcore.Complete(content, doc, m.line, col)
		labels := map[string]bool{}
		for _, item := range items {
			labels[item.Label] = true
		}

		for _, arg := range m.args[1:] {
			if strings.HasPrefix(arg, "!") {
				absent := arg[1:]
				if labels[absent] {
					t.Errorf("line %d: completion should not contain %q", m.line, absent)
				}
			} else {
				if !labels[arg] {
					t.Errorf("line %d: completion missing %q (got %s)", m.line, arg, fmtLabels(labels))
				}
			}
		}
	}
}
func fmtLabels(labels map[string]bool) string {
	var names []string
	for k := range labels {
		names = append(names, k)
	}
	if len(names) > 10 {
		return fmt.Sprintf("[%d items]", len(names))
	}
	return strings.Join(names, ", ")
}
