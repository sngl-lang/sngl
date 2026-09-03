// Command tmpconv converts a cmd/sngl/testdata/*.txt script fixture into one
// testdata/*.txtar golden archive per program it builds. Temporary.
package main

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	_ "git.duckfam.us/jonathan/sngl/internal/testtargets"
)

type target struct {
	input, lang, plat, outdir string
	opts                      map[string]string
}

type assertion struct {
	neg               bool
	pat, file, reason string
}

var flagRe = regexp.MustCompile(`^--?([a-zA-Z-]+)(?:=(.*))?$`)

func main() {
	src, outDir, base := os.Args[1], os.Args[2], os.Args[3]
	raw, err := os.ReadFile(src)
	must(err)
	arc := txtar.Parse(raw)

	targets, asserts, prose, err := parseScript(string(arc.Comment))
	if err != nil {
		fatal("%s: %v", src, err)
	}
	if len(targets) == 0 {
		fatal("%s: no generate command", src)
	}

	// One archive per program: an archive is one package, and two programs in
	// one would be merged into it.
	var inputs []string
	byInput := map[string][]target{}
	for _, t := range targets {
		if _, ok := byInput[t.input]; !ok {
			inputs = append(inputs, t.input)
		}
		byInput[t.input] = append(byInput[t.input], t)
	}

	for _, in := range inputs {
		ts := byInput[in]
		seen := map[string]string{}
		for _, t := range ts {
			k := t.lang + "/" + t.plat
			if prev, ok := seen[k]; ok {
				fatal("%s: %s built for %s twice (%s and %s); needs hand conversion", src, in, k, prev, t.outdir)
			}
			seen[k] = t.outdir
		}
		name := base
		if len(inputs) > 1 && path.Base(in) != "app.sngl" {
			name = base + "_" + strings.TrimSuffix(path.Base(in), ".sngl")
		}
		writeArchive(src, path.Join(outDir, name+".txtar"), arc, in, ts, asserts, prose)
	}
}

func writeArchive(src, dst string, arc *txtar.Archive, input string, ts []target, asserts []assertion, prose string) {
	out := &txtar.Archive{}
	var rootIdx = -1
	for _, f := range arc.Files {
		n := path.Clean(f.Name)
		isRootSNGL := !strings.Contains(n, "/") && strings.HasSuffix(n, ".sngl")
		if isRootSNGL && n != path.Clean(input) {
			continue // another program's source; a package would swallow it
		}
		out.Files = append(out.Files, f)
		if isRootSNGL {
			rootIdx = len(out.Files) - 1
		}
	}
	if rootIdx < 0 {
		fatal("%s: input %q is not in the archive", src, input)
	}
	data, err := addOutputBlock(out.Files[rootIdx].Name, out.Files[rootIdx].Data, ts)
	if err != nil {
		fatal("%s: %v", src, err)
	}
	out.Files[rootIdx].Data = data
	for i, f := range out.Files {
		if !strings.HasSuffix(f.Name, ".sngl") || strings.Contains(string(f.Data), "NOFMT") {
			continue
		}
		if doc, err := parser.Parse(f.Name, f.Data); err == nil {
			out.Files[i].Data = []byte(parser.Format(doc))
		}
	}
	out.Comment = []byte(strings.TrimSpace(prose) + "\n")
	must(os.WriteFile(dst, txtar.Format(out), 0o644))

	// Sidecars for the second pass, which resolves an assertion's file to a
	// golden path once -update has said which target wrote what.
	f, err := os.Create(dst + ".asserts")
	must(err)
	defer f.Close()
	mine := map[string]bool{}
	for _, t := range ts {
		mine[t.outdir] = true
	}
	for _, a := range asserts {
		dir, _, _ := strings.Cut(a.file, "/")
		if !mine[dir] && !mine[a.file] {
			continue
		}
		neg := ""
		if a.neg {
			neg = "!"
		}
		fmt.Fprintf(f, "%s\t%s\t%s\t%s\n", neg, a.file, a.pat, a.reason)
	}
	m, err := os.Create(dst + ".targets")
	must(err)
	defer m.Close()
	for _, t := range ts {
		fmt.Fprintf(m, "%s\t%s\t%s\n", t.outdir, t.lang, t.plat)
	}
}

func parseScript(script string) (targets []target, asserts []assertion, prose string, err error) {
	var blocks [][]string
	var pending []string
	for _, raw := range strings.Split(script, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" {
			pending = nil
			continue
		}
		if c, ok := strings.CutPrefix(strings.TrimSpace(line), "#"); ok {
			c = strings.TrimSpace(c)
			if len(pending) == 0 {
				blocks = append(blocks, nil)
			}
			pending = append(pending, c)
			blocks[len(blocks)-1] = pending
			continue
		}
		toks, terr := tokenize(line)
		if terr != nil {
			return nil, nil, "", terr
		}
		if len(toks) == 0 {
			continue
		}
		neg := false
		if toks[0] == "!" {
			neg, toks = true, toks[1:]
		}
		switch toks[0] {
		case "sngl":
			if len(toks) < 2 || toks[1] != "generate" || neg {
				return nil, nil, "", fmt.Errorf("unsupported command %q", line)
			}
			t, gerr := parseGenerate(toks[2:])
			if gerr != nil {
				return nil, nil, "", gerr
			}
			targets = append(targets, t)
		case "grep":
			if len(toks) != 3 {
				return nil, nil, "", fmt.Errorf("grep wants a pattern and a file: %q", line)
			}
			asserts = append(asserts, assertion{neg: neg, pat: toks[1], file: toks[2], reason: strings.Join(pending, " ")})
		case "exists":
		default:
			return nil, nil, "", fmt.Errorf("unsupported command %q", line)
		}
		pending = nil
	}
	used := map[string]bool{}
	for _, a := range asserts {
		if a.neg && a.reason != "" {
			used[a.reason] = true
		}
	}
	var paras []string
	for _, b := range blocks {
		if !used[strings.Join(b, " ")] {
			paras = append(paras, strings.Join(b, "\n"))
		}
	}
	return targets, asserts, strings.Join(paras, "\n\n"), nil
}

func parseGenerate(args []string) (target, error) {
	t := target{outdir: "out", opts: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		m := flagRe.FindStringSubmatch(a)
		if m == nil {
			t.input = a
			continue
		}
		if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			m[2] = args[i]
		}
		switch m[1] {
		case "lang":
			t.lang = m[2]
		case "platform":
			t.plat = m[2]
		case "out", "o":
			t.outdir = m[2]
		case "opt":
			k, v, _ := strings.Cut(m[2], "=")
			t.opts[k] = v
		default:
			return t, fmt.Errorf("unsupported generate flag %q", a)
		}
	}
	if t.input == "" {
		return t, fmt.Errorf("generate names no input")
	}
	if t.plat == "" {
		return t, nil
	}
	if t.lang == "" {
		p := codegen.LookupPlatform(t.plat)
		if p == nil || len(p.SupportedLangs()) == 0 {
			return t, fmt.Errorf("unknown platform %q", t.plat)
		}
		t.lang = p.SupportedLangs()[0]
	}
	return t, nil
}

func addOutputBlock(name string, data []byte, targets []target) ([]byte, error) {
	if regexp.MustCompile(`(?m)^output\b`).Match(data) {
		return data, nil
	}
	byLang := map[string][]target{}
	var langs []string
	for _, t := range targets {
		if t.plat == "" {
			return nil, fmt.Errorf("%s: no output block and no --platform", name)
		}
		if _, ok := byLang[t.lang]; !ok {
			langs = append(langs, t.lang)
		}
		byLang[t.lang] = append(byLang[t.lang], t)
	}
	sort.Strings(langs)
	var b strings.Builder
	b.WriteString("output {\n")
	for _, l := range langs {
		fmt.Fprintf(&b, "    %s {\n", l)
		ts := byLang[l]
		sort.Slice(ts, func(i, j int) bool { return ts[i].plat < ts[j].plat })
		for _, t := range ts {
			fmt.Fprintf(&b, "        %s", t.plat)
			if len(t.opts) > 0 {
				var keys []string
				for k := range t.opts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var kv []string
				for _, k := range keys {
					kv = append(kv, k+"="+optLit(t.opts[k]))
				}
				fmt.Fprintf(&b, "(%s)", strings.Join(kv, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("    }\n")
	}
	b.WriteString("}\n")

	lines := strings.Split(string(data), "\n")
	insert := 0
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "import ") {
			insert = i + 1
			continue
		}
		if s == "" || strings.HasPrefix(s, "//") {
			continue
		}
		break
	}
	outLines := append([]string{}, lines[:insert]...)
	outLines = append(outLines, "", strings.TrimRight(b.String(), "\n"))
	outLines = append(outLines, lines[insert:]...)
	joined := strings.Join(outLines, "\n")

	doc, err := parser.Parse(name, []byte(joined))
	if err != nil {
		return nil, fmt.Errorf("reparse after adding output block: %w", err)
	}
	return []byte(parser.Format(doc)), nil
}

var numRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

func optLit(v string) string {
	if v == "true" || v == "false" || numRe.MatchString(v) {
		return v
	}
	return `"` + v + `"`
}

func tokenize(line string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	inTok := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if inTok {
				toks = append(toks, cur.String())
				cur.Reset()
				inTok = false
			}
		case c == '\'' || c == '"':
			q := c
			i++
			start := i
			for i < len(line) && line[i] != q {
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unterminated quote in %q", line)
			}
			cur.WriteString(line[start:i])
			inTok = true
		default:
			cur.WriteByte(c)
			inTok = true
		}
	}
	if inTok {
		toks = append(toks, cur.String())
	}
	return toks, nil
}

func must(err error) {
	if err != nil {
		fatal("%v", err)
	}
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}
