// Package goldentest runs the language and codegen fixtures in testdata/*.txtar.
//
// A fixture is one SNGL package and the code every target it declares
// generates from it. The archive's root-level files are the package; `out/` is
// the golden. Which targets run is the source's own `output` block, so the
// fixture reads as a program rather than as a program plus a harness header,
// and the block's own nesting is why the golden is laid out
// `out/<lang>/<platform>/`.
//
// The compile is internal/build's — the same code `sngl generate` runs. This
// package only arranges files around it.
//
// # The record beside the golden
//
// A golden says what the compiler emitted. Whether that emission is a
// *program* is a question only a host toolchain answers, and the two fail
// differently: a change that reorders two statements breaks the golden, and
// one that emits a name nothing declares leaves the golden looking fine. So
// `run/<lang>/<platform>` holds what the host said — `pass`, and the digest of
// the bytes it said it about.
//
// The digest is what makes the compiler *conditional*. Generating is
// milliseconds and compiling is seconds to minutes, so the default run
// generates, fingerprints, and compares against the record: unchanged output
// has already been compiled, by whoever last ran -update, and the archive
// carries the proof. Only -update compiles, and only for the targets whose
// output actually moved.
//
// That the proof is *committed* is the point. Go's build cache already avoids
// recompiling byte-identical code on a warm machine, which is why this looked
// unnecessary locally; a CI runner clones fresh every pipeline and has no such
// memory, and gradle, a browser and `go mod tidy` have no such cache anywhere.
//
// Two things follow. A record must never be written for a verification that
// did not happen — a host missing the toolchain leaves the committed record
// alone and says so, rather than recording a skip as a pass. And a record goes
// stale when the *toolchain* moves under it, which no diff announces, so
// -verify compiles everything regardless of what the records say.
//
// # Why this exists next to cmd/sngl/testdata
//
// cmd/sngl/testdata is an rsc.io/script harness driving the real CLI, and it
// stays that: flags, exit codes, `dump` stages, error text, anything whose
// subject is the command line. What it was also being used for is asserting on
// generated code, with `grep` — and a substring assertion cannot see the shape
// of what it matched, cannot see the order two statements came out in, and is
// an escaped regex over generated Go that is hard to read and harder to
// review. A whole-file golden shows all three.
//
// Two failures are worth naming because they are what this harness is for. A
// pass that set its flag as a loop body's first statement was moved to set it
// last, breaking `break` and `continue` on every compiled target; the entire
// suite stayed green, because the only assertion was `grep -count=1` for the
// assignment. And `! grep` with a single argument is a usage error, which
// under `!` is a pass — six such lines had accumulated.
//
// What a golden does not fix: a fixture whose input never reaches the branch
// it means to exercise generates identical output either way. That is a
// property of the input program, and no assertion format catches it.
//
// # What stays a script fixture
//
// The 67 fixtures that were only `sngl generate` plus greps are archives here
// now. What was left behind, and why, so the line is not redrawn by accident:
//
//   - Anything whose subject is the command line: flags, exit codes, the text
//     of an error, `dump` stages, what is printed on stdout.
//   - Anything whose subject is the `sngl` command itself compiling or running
//     a program: `sngl test`'s own reporting, `sngl build`'s artifacts, the
//     `--skip-platform` flag. The *generated code* being compiled is no longer
//     a reason to stay — that is what the record above is — but the CLI's
//     behaviour around it still is.
//   - A fixture whose imports reach outside the archive. A `go:` or `c:`
//     import is resolved by shelling out to the host toolchain against the
//     working directory, and this harness has an fstest.MapFS and no
//     directory. The script harness gives it a real one.
//   - A fixture that builds one target twice to compare two option sets
//     (minify against not, cache-busting against not). Two builds of one
//     lang/platform have one golden path between them, so the comparison has
//     nowhere to live; generate() rejects it rather than letting one silently
//     overwrite the other.
package goldentest

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"

	// Register every language and platform, so a fixture's output block can
	// name any of them, and every import scheme, so a fixture may import a
	// `go:` or `c:` package the way a program built by the CLI can -- main.go
	// blank-imports the same package.
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme"
	_ "git.duckfam.us/jonathan/sngl/internal/testtargets"
)

// goldenPrefix is the archive directory holding generated output. Everything
// outside it is the package's own source.
const goldenPrefix = "out/"

// Run runs every archive matching glob as a subtest. update rewrites each
// archive's golden instead of comparing against it.
func Run(t *testing.T, glob string, update, force bool) {
	t.Helper()
	// The build logs a phase timing per target at Info, which is three lines
	// per fixture of noise between a failure and its diff. Restored after,
	// because this is one test in a binary it does not own.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixtures match %s", glob)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txtar")
		t.Run(name, func(t *testing.T) {
			runFixture(t, file, update, force)
		})
	}
}

// No t.Helper: with one, every failure from any of the four checks below
// reports this function's caller, and which check fired is the useful part.
func runFixture(t *testing.T, path string, update, force bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	arc := txtar.Parse(raw)

	src, golden, records, err := split(arc)
	if err != nil {
		t.Fatal(err)
	}
	denies, err := parseDenies(string(arc.Comment))
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	exempt, err := parseExemptions(string(arc.Comment))
	if err != nil {
		t.Fatalf("comment: %v", err)
	}

	assertFormatted(t, src)

	got, err := generate(src, nil, false)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// What the host toolchain is handed is not what the golden shows, and
	// deliberately so. The golden is the plain build, because that is what a
	// reader wants; the toolchain gets a *runnable* one.
	//
	// `main` is what makes it runnable, and it is unconditional. A library
	// build gives android one Kotlin file and no Gradle project at all, so
	// there was nothing to build it with and the target recorded nothing --
	// the "skip because there is nothing to run" that this harness must not
	// have. With main it is an eleven-file scaffold with a wrapper, and every
	// target answers for every fixture.
	//
	// A fixture carrying `func test…` adds the test options on top, so the
	// assertions are compiled and run rather than merely the program.
	opts, _ := testutil.TestHarnessBuild(fixtureSource(src))
	verifyFiles, err := generate(src, opts, true)
	if err != nil {
		t.Fatalf("generate (verification build): %v", err)
	}

	// Verification comes before the golden is written, so a -update run that
	// cannot compile what it generated leaves the archive as it found it.
	// Written first, the bad golden would be committed and the record beside
	// it would be the only thing saying so.
	kept := verify(t, targetsOf(verifyFiles), records, exempt, update, force)

	if update {
		if t.Failed() {
			// All or nothing. A verification that could not run leaves the
			// archive exactly as it was found -- written anyway, the golden
			// would advance to bytes nobody built while the record beside it
			// still described the old ones, which is the half-updated state
			// the record exists to make impossible.
			t.Logf("not writing the archive: verification did not complete")
			return
		}
		writeGolden(t, path, arc, got, kept)
		// Deny directives are checked against the rewritten golden too: a
		// -update run that quietly reintroduces what a fixture denies is the
		// regression the directive is there to catch.
		checkDenies(t, denies, got)
		return
	}

	compare(t, got, golden)
	checkDenies(t, denies, got)
}

// split separates the package's source from the golden and from the
// verification records.
func split(arc *txtar.Archive) (src, golden map[string][]byte, records map[string]record, err error) {
	src, golden, records = map[string][]byte{}, map[string][]byte{}, map[string]record{}
	fail := func(format string, a ...any) (_, _ map[string][]byte, _ map[string]record, err error) {
		return nil, nil, nil, fmt.Errorf(format, a...)
	}
	for _, f := range arc.Files {
		name := path.Clean(f.Name)
		if !fs.ValidPath(name) {
			return fail("file %q is not a valid FS path", f.Name)
		}
		if rest, ok := strings.CutPrefix(name, goldenPrefix); ok {
			if strings.Count(rest, "/") < 2 {
				return fail("golden file %q: expected out/<lang>/<platform>/<path>", f.Name)
			}
			golden[name] = f.Data
			continue
		}
		if rest, ok := strings.CutPrefix(name, recordPrefix); ok {
			if strings.Count(rest, "/") != 1 {
				return fail("record file %q: expected run/<lang>/<platform>", f.Name)
			}
			r, rerr := parseRecord(name, f.Data)
			if rerr != nil {
				return fail("%w", rerr)
			}
			records[name] = r
			continue
		}
		src[name] = f.Data
	}
	if len(src) == 0 {
		return fail("archive has no source files")
	}
	if !hasRootSNGL(src) {
		return fail("archive has no root-level .sngl file")
	}
	return src, golden, records, nil
}

func hasRootSNGL(src map[string][]byte) bool {
	for name := range src {
		if !strings.Contains(name, "/") && strings.HasSuffix(name, ".sngl") {
			return true
		}
	}
	return false
}

// assertFormatted holds a fixture's SNGL to the same rule testdata/*.sngl is
// held to: every one is written the way `sngl fmt` writes it. `sngl fmt` takes
// no archive, so this is where the rule is enforced for source inside one.
// A fixture whose exact layout is the thing under test opts out with
// `// NOFMT "reason"`, as the .sngl fixtures do.
func assertFormatted(t *testing.T, src map[string][]byte) {
	t.Helper()
	for _, name := range sortedKeys(src) {
		if !strings.HasSuffix(name, ".sngl") {
			continue
		}
		source := string(src[name])
		doc, err := parser.Parse(name, src[name])
		if err != nil {
			// Reported here rather than left to the compile: only root-level
			// files reach ParsePackageFS, and a subdirectory package is
			// parsed lazily by the resolver -- so one nothing imports is
			// never read, and its parse error would go nowhere.
			t.Errorf("%s: %v", name, err)
			continue
		}
		nofmt, _, nerr := testutil.ParseNoFmtSource(source)
		if nerr != nil {
			t.Errorf("%s: %v", name, nerr)
			continue
		}
		formatted := parser.Format(doc)
		switch {
		case nofmt && formatted == source:
			t.Errorf("%s: NOFMT is stale — the file is formatted, so drop the directive", name)
		case !nofmt && formatted != source:
			t.Errorf("%s: not formatted. Write it with `sngl fmt` and paste it back:\n%s", name, firstDiff(source, formatted))
		}
	}
}

// generate compiles the package for every target its output block names, and
// returns the generated files keyed by their golden path.
//
// opts and main are the overlay a verification build adds; the golden itself
// is generated with neither, so what the archive shows is what `sngl generate`
// writes.
func generate(src map[string][]byte, opts map[string]string, main bool) (map[string][]byte, error) {
	fsys := fstest.MapFS{}
	for name, data := range src {
		fsys[name] = &fstest.MapFile{Data: data}
	}

	doc, err := build.ParsePackageFS(fsys)
	if err != nil {
		return nil, err
	}
	pkg, err := build.Check(doc, build.CheckConfig{
		Dir:      ".",
		FS:       fsys,
		Resolver: build.NewFSResolver(fsys),
		IsMain:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}

	// Name and Dir match what `sngl generate <file>` passes for a package in
	// the working directory, and OutDir is its default. A golden is meant to
	// be what the CLI would have written, so nothing here may be a setting the
	// CLI does not use.
	results, err := build.Emit(pkg, build.Options{
		Name:      firstRootSNGL(src),
		Dir:       ".",
		OutDir:    ".",
		ProjectFS: fsys,
		Opts:      opts,
		Main:      main,
	})
	if err != nil {
		return nil, err
	}

	out := map[string][]byte{}
	seen := map[string]bool{}
	// txtar has no spelling for a file that does not end in a newline --
	// Format appends one -- so a generated file missing it is compared, and
	// stored, with one. Only the android i18n manifest is written that way
	// today; without this the fixture cannot round-trip through its own
	// golden.
	for _, res := range results {
		dir := goldenPrefix + res.Target.Lang + "/" + res.Target.Platform
		if seen[dir] {
			return nil, fmt.Errorf("output declares %s/%s twice; its two builds would overwrite each other's golden", res.Target.Lang, res.Target.Platform)
		}
		seen[dir] = true
		if len(res.Files) == 0 {
			return nil, fmt.Errorf("target %s/%s generated no files", res.Target.Lang, res.Target.Platform)
		}
		for name, data := range res.Files {
			if len(data) > 0 && data[len(data)-1] != '\n' {
				data = append(append([]byte{}, data...), '\n')
			}
			out[dir+"/"+path.Clean(name)] = data
		}
	}
	return out, nil
}

func firstRootSNGL(src map[string][]byte) string {
	for _, name := range sortedKeys(src) {
		if !strings.Contains(name, "/") && strings.HasSuffix(name, ".sngl") {
			return name
		}
	}
	return "app.sngl"
}

func compare(t *testing.T, got, golden map[string][]byte) {
	t.Helper()
	if len(golden) == 0 {
		t.Fatalf("no golden files in archive; run with -update to seed them")
	}
	for _, name := range sortedKeys(got) {
		want, ok := golden[name]
		if !ok {
			t.Errorf("%s: generated but not in the golden (run -update):\n%s", name, got[name])
			continue
		}
		if !bytes.Equal(got[name], want) {
			t.Errorf("%s: differs from the golden:\n%s", name, firstDiff(string(want), string(got[name])))
		}
	}
	for _, name := range sortedKeys(golden) {
		if _, ok := got[name]; !ok {
			t.Errorf("%s: in the golden but no longer generated (run -update)", name)
		}
	}
}

func writeGolden(t *testing.T, path string, arc *txtar.Archive, got map[string][]byte, records map[string]record) {
	t.Helper()
	var files []txtar.File
	for _, f := range arc.Files {
		trimmed := strings.TrimPrefix(f.Name, "./")
		if !strings.HasPrefix(trimmed, goldenPrefix) && !strings.HasPrefix(trimmed, recordPrefix) {
			files = append(files, f)
		}
	}
	for _, name := range sortedKeys(got) {
		files = append(files, txtar.File{Name: name, Data: got[name]})
	}
	for _, name := range sortedStrings(records) {
		files = append(files, txtar.File{Name: name, Data: []byte(records[name].String())})
	}
	arc.Files = files
	if err := os.WriteFile(path, txtar.Format(arc), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstDiff reports the first differing line with a little context, because a
// generated file is long and a whole-file dump buries the one line that moved.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		w, g := "", ""
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		var b strings.Builder
		for j := max(0, i-3); j < i && j < len(wl); j++ {
			fmt.Fprintf(&b, "  %d: %s\n", j+1, wl[j])
		}
		fmt.Fprintf(&b, "- %d: %s\n", i+1, w)
		fmt.Fprintf(&b, "+ %d: %s\n", i+1, g)
		return b.String()
	}
	return "(identical line by line; trailing newline differs)"
}

// fixtureSource concatenates the package's own .sngl files, which is what the
// test-build derivation reads.
func fixtureSource(src map[string][]byte) string {
	var b strings.Builder
	for _, name := range sortedKeys(src) {
		if strings.HasSuffix(name, ".sngl") {
			b.Write(src[name])
			b.WriteString("\n")
		}
	}
	return b.String()
}
