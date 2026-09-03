package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptGrepsNameAFile guards a silent no-op. rsc.io/script's `grep` takes
// a pattern and a file; given one argument it returns a usage error, and a
// usage error under `!` is a *pass*. So `! grep 'nope'` asserts nothing at all
// and reads exactly like an assertion that does.
//
// Six of them had accumulated, three checking the absence of something a
// change was specifically about. The cost of the rule is one argument per
// line; the cost of not having it is a test that goes green when its subject
// regresses.
func TestScriptGrepsNameAFile(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("globbing testdata: %v (%d files)", err, len(files))
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			cmd := strings.TrimSpace(line)
			// The archive's files follow the script, and a `-- name --`
			// marker opens them; nothing after that is a command.
			if strings.HasPrefix(cmd, "-- ") {
				break
			}
			cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "!"))
			if !strings.HasPrefix(cmd, "grep ") {
				continue
			}
			if n := len(operands(strings.TrimPrefix(cmd, "grep "))); n != 2 {
				t.Errorf("%s:%d: grep takes a pattern and a file, got %d of them: %s",
					file, i+1, n, strings.TrimSpace(line))
			}
		}
	}
}

// operands splits a command's arguments and drops the flags, honouring the
// single and double quotes a pattern is written in.
func operands(args string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(args); i++ {
		c := args[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
		case c == ' ':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	var kept []string
	for _, a := range out {
		if !strings.HasPrefix(a, "-") {
			kept = append(kept, a)
		}
	}
	return kept
}
