package goldentest

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"duckfam.us/sngl/internal/toolchain"
)

// An execution is one run of a target's program, made after the program
// builds and its tests pass, and covered by the target's record: the record's
// digest is of the generated files and every execution of them, so adding,
// changing or removing one asks for -update as a codegen change does.
//
//	exec out/go/gtk4 --window x -- shown.txt
//	exec out/go/fyne <clicks.txt -- want.txt
//	exec out/go/gtk4 toplevels[Load]=2
//
// The words after the target are the program's arguments, except `<file`,
// which is its standard input, and `toplevels=N` or `toplevels[title]=N`, how
// many times gtk4 titles a toplevel, any or the one named -- the Wayland
// protocol log is how a run tells a window reached the screen. `-- file` is
// what the program must print. A file named is one of the archive's own, and a
// run must exit zero whatever else it asserts.
type execution struct {
	line           int
	text           string
	lang, platform string
	run            toolchain.Run
	stdout         string
	toplevels      map[string]int
}

func (e execution) target() string { return e.lang + "/" + e.platform }

var toplevelsWord = regexp.MustCompile(`^toplevels(?:\[([^\]]+)\])?=([0-9]+)$`)

// parseExecs reads the `exec` directives out of the archive comment, by
// target.
func parseExecs(comment string, src map[string][]byte) (map[string][]execution, error) {
	out := map[string][]execution{}
	for i, raw := range strings.Split(comment, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		rest, ok := strings.CutPrefix(line, "exec")
		if !ok || (rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t")) {
			continue
		}
		e, err := parseExec(strings.TrimSpace(rest), src)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w\n\t%s", i+1, err, line)
		}
		e.line, e.text = i+1, line
		out[e.target()] = append(out[e.target()], e)
	}
	return out, nil
}

func parseExec(rest string, src map[string][]byte) (execution, error) {
	const form = "want: exec out/<lang>/<platform> [args] [<stdin] [toplevels[title]=N] [-- stdout]"

	words := strings.Fields(rest)
	if len(words) == 0 {
		return execution{}, fmt.Errorf("exec names no target (%s)", form)
	}
	spec, ok := strings.CutPrefix(words[0], goldenPrefix)
	lang, platform, ok2 := strings.Cut(strings.TrimSuffix(spec, "/"), "/")
	if !ok || !ok2 || lang == "" || platform == "" || strings.Contains(platform, "/") {
		return execution{}, fmt.Errorf("exec target %q is not out/<lang>/<platform> (%s)", words[0], form)
	}
	e := execution{lang: lang, platform: platform, toplevels: map[string]int{}}
	file := func(name string) ([]byte, error) {
		data, ok := src[name]
		if !ok || strings.HasSuffix(name, ".sngl") {
			return nil, fmt.Errorf("exec names %q, which is not a file of the archive", name)
		}
		return data, nil
	}
	words = words[1:]
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--":
			if i != len(words)-2 {
				return execution{}, fmt.Errorf("exec's `--` takes one file and ends the line (%s)", form)
			}
			if _, err := file(words[i+1]); err != nil {
				return execution{}, err
			}
			e.stdout = words[i+1]
			i++
		case strings.HasPrefix(w, "<"):
			data, err := file(w[1:])
			if err != nil {
				return execution{}, err
			}
			e.run.Stdin = data
		case toplevelsWord.MatchString(w):
			if platform != "gtk4" {
				return execution{}, fmt.Errorf("exec counts toplevels on %s; only gtk4 speaks Wayland to the compositor", platform)
			}
			m := toplevelsWord.FindStringSubmatch(w)
			n, _ := strconv.Atoi(m[2])
			e.toplevels[m[1]] = n
			e.run.Wayland = true
		default:
			e.run.Args = append(e.run.Args, w)
		}
	}
	return e, nil
}

// withExecs is what a target's digest is taken over: its generated files and,
// where it has any execution, each one, the archive files it names, and the
// source of the runtime packages the program imports from this checkout --
// which is most of what an execution tests.
func withExecs(files map[string][]byte, execs []execution, src map[string][]byte) (map[string][]byte, error) {
	if len(execs) == 0 {
		return files, nil
	}
	h := sha256.New()
	if err := toolchain.HashModuleImports(h, files); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(files)+len(execs))
	maps.Copy(out, files)
	for i, e := range execs {
		key := fmt.Sprintf("\x00exec/%d", i)
		out[key] = []byte(e.text)
		out[key+"/stdin"] = e.run.Stdin
		if e.stdout != "" {
			out[key+"/stdout"] = src[e.stdout]
		}
	}
	out["\x00exec/modules"] = h.Sum(nil)
	return out, nil
}

// checkRan holds one execution to what it printed.
func checkRan(e execution, ran toolchain.Ran, src map[string][]byte) error {
	if ran.Err != nil {
		return fmt.Errorf("line %d: %v\n--- stdout ---\n%s--- stderr ---\n%s", e.line, ran.Err, ran.Stdout, tail(ran.Stderr))
	}
	if e.stdout != "" && ran.Stdout != string(src[e.stdout]) {
		return fmt.Errorf("line %d: printed\n%s\nwant %s:\n%s", e.line, ran.Stdout, e.stdout, src[e.stdout])
	}
	for title, want := range e.toplevels {
		if got := countToplevels(ran.Stderr, title); got != want {
			name := "toplevels"
			if title != "" {
				name += "[" + title + "]"
			}
			return fmt.Errorf("line %d: %s=%d, want %d", e.line, name, got, want)
		}
	}
	return nil
}

// countToplevels counts the set_title requests in a WAYLAND_DEBUG log, of
// every toplevel or of the one titled title.
func countToplevels(log, title string) int {
	n := 0
	for line := range strings.SplitSeq(log, "\n") {
		if !strings.Contains(line, "xdg_toplevel") || !strings.Contains(line, ".set_title(") {
			continue
		}
		if title == "" || strings.Contains(line, ".set_title("+strconv.Quote(title)+")") {
			n++
		}
	}
	return n
}

// tail is the end of a log too long to read whole, which a Wayland one is.
func tail(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max:]
}
