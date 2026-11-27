package sngl_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"
	"golang.org/x/tools/txtar"

	snglast "duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/testinputs"
	"duckfam.us/sngl/internal/trust"
	"duckfam.us/sngl/ir"
)

// FuzzPipeline drives a package as far through the compiler as it will go,
// for one target, and holds every stage it reaches to what the stage promises.
// See design/testing.md, "The pipeline fuzzer".
//
// The input is a txtar archive -- the package's files -- so a seed can carry
// a go: module or an OpenAPI document beside its SNGL, and target picks the
// compile target: 0 is the archive's own output blocks, anything else one
// supported language/platform pair.
func FuzzPipeline(f *testing.F) {
	// The Go check reads export data `go list -export` builds from the
	// runtimes generated code imports.
	testinputs.Packages(f, ".", "./pkg/go/...", "./lib/...")
	targets := pipelineTargets()
	// SNGL_FUZZ_SEEDS is a regexp the seeds' file names are held to, for
	// rerunning the ones a fix is about.
	only := regexp.MustCompile("")
	if v := os.Getenv("SNGL_FUZZ_SEEDS"); v != "" {
		only = regexp.MustCompile(v)
	}
	// SNGL_FUZZ_TARGETS=all adds every seed on every target, the sweep a
	// generator change wants before the fuzzer mutates anything.
	all := os.Getenv("SNGL_FUZZ_TARGETS") == "all"
	for i, s := range pipelineSeeds(f) {
		if !only.MatchString(s.name) {
			continue
		}
		f.Add(s.archive, uint8(0))
		if all {
			for t := range targets {
				f.Add(s.archive, uint8(1+t))
			}
			continue
		}
		// Every seed on one other target too, rotating, so the seed run
		// reaches each generator without multiplying the corpus by all of
		// them.
		f.Add(s.archive, uint8(1+i%len(targets)))
	}
	f.Fuzz(func(t *testing.T, archive []byte, target uint8) {
		var lang, plat string
		if target != 0 {
			tg := targets[int(target-1)%len(targets)]
			lang, plat = tg.lang, tg.plat
		}
		if skip := runPipeline(t, archive, lang, plat); skip != "" {
			t.Skip(skip)
		}
	})
}

// TestPipelinePinned runs each archive testdata/pipeline holds -- a program
// the fuzzer found, written with the output block naming the targets it was
// found on -- and holds it to building, or to a target declining it: one that
// stops at the parse or the check pins nothing.
func TestPipelinePinned(t *testing.T) {
	testinputs.Packages(t, ".", "./pkg/go/...", "./lib/...")
	names, err := filepath.Glob("testdata/pipeline/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		t.Run(strings.TrimSuffix(filepath.Base(n), ".txtar"), func(t *testing.T) {
			archive, err := os.ReadFile(n)
			if err != nil {
				t.Fatal(err)
			}
			skip := runPipeline(t, archive, "", "")
			if skip != "" && !strings.HasPrefix(skip, "declined: ") && !strings.HasPrefix(skip, "refused: ") {
				t.Fatalf("stopped short of the build, so pins nothing: %s", skip)
			}
		})
	}
}

// runPipeline compiles archive for lang/plat, its own output blocks where
// lang is empty, and holds what it generates to the in-process checks. It
// fails t on a fault and answers why the input stopped short of codegen, ""
// where it built.
func runPipeline(t *testing.T, archive []byte, lang, plat string) string {
	t.Helper()
	files, err := compilePipeline(archive, lang, plat)
	if err != nil {
		if skip, ok := errors.AsType[pipelineSkip](err); ok {
			return skip.reason
		}
		t.Fatalf("%s/%s: %v\n--- archive ---\n%s", lang, plat, err, archive)
	}
	goos := "linux"
	if plat == "win32" {
		goos = "windows"
	}
	if err := checkGoFiles(files, goos); err != nil {
		t.Fatalf("%s/%s: generated Go: %v\n--- archive ---\n%s", lang, plat, err, archive)
	}
	for name, data := range files {
		if err := checkGenerated(name, data); err != nil {
			t.Fatalf("%s/%s: generated %s: %v\n--- archive ---\n%s\n--- %s ---\n%s",
				lang, plat, name, err, archive, name, data)
		}
	}
	return ""
}

// A pipelineSkip is an input the pipeline stopped short of codegen on for a
// reason that is not a fault: it did not parse or check, the target declined
// it, or it asked for what a hermetic build refuses.
type pipelineSkip struct{ reason string }

func (s pipelineSkip) Error() string { return s.reason }

type pipelineTarget struct{ lang, plat string }

// pipelineTargets is every supported language/platform pair, in a stable
// order so a corpus entry's target byte means the same thing next run.
func pipelineTargets() []pipelineTarget {
	var out []pipelineTarget
	for _, p := range codegen.Platforms() {
		for _, l := range codegen.LookupPlatform(p).SupportedLangs() {
			out = append(out, pipelineTarget{l, p})
		}
	}
	return out
}

// compilePipeline parses, checks and emits archive for one target and returns
// the generated files. An error is a fault unless it is a pipelineSkip.
func compilePipeline(archive []byte, lang, plat string) (files map[string][]byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	fsys := fstest.MapFS{}
	for _, f := range txtar.Parse(archive).Files {
		name := path.Clean(f.Name)
		if !fs.ValidPath(name) {
			return nil, pipelineSkip{"archive names " + f.Name}
		}
		fsys[name] = &fstest.MapFile{Data: f.Data}
	}
	doc, err := safeParsePackage(fsys)
	if err != nil {
		return nil, pipelineSkip{"parse: " + err.Error()}
	}
	// A check with no target first: a program wrong everywhere is not one
	// any target declined.
	pkg, err := build.Check(doc, build.CheckConfig{Dir: ".", FS: fsys, Resolver: build.NewFSResolver(fsys), IsMain: true})
	if err != nil {
		return nil, pipelineSkip{"check: " + err.Error()}
	}
	if !pkg.IsProgram() {
		return nil, pipelineSkip{"not a program: its package body renders nothing"}
	}
	if err := build.ValidateOutputs(pkg); err != nil {
		return nil, pipelineSkip{"output block: " + err.Error()}
	}
	if lang == "" && len(pkg.Outputs) == 0 {
		return nil, pipelineSkip{"no output block, and no target chosen"}
	}
	if lang != "" {
		// What the target's own library rejects is that target declining the
		// program at check time: a diagnostic, with the position it names.
		doc, _ = safeParsePackage(fsys)
		pkg, err = build.Check(doc, build.CheckConfig{
			Dir: ".", FS: fsys, Resolver: build.NewFSResolver(fsys), IsMain: true,
			Targets: build.SelectedTargets(lang, plat),
		})
		if err != nil {
			return nil, pipelineSkip{"check for " + lang + "/" + plat + ": " + err.Error()}
		}
	}
	results, err := build.Emit(pkg, build.Options{
		Name: "main.sngl", Dir: ".", OutDir: ".", ProjectFS: fsys,
		Lang: lang, Platform: plat, Main: true,
		// Nil refuses everything: a fuzzed program runs no process, reads
		// nothing off the host and fetches nothing.
		Trust: nil,
	})
	if err != nil {
		if ir.IsDeclined(err) {
			return nil, pipelineSkip{"declined: " + err.Error()}
		}
		if _, ok := errors.AsType[*trust.Refusal](err); ok {
			return nil, pipelineSkip{"refused: " + err.Error()}
		}
		return nil, fmt.Errorf("emit: %w", err)
	}
	files = map[string][]byte{}
	for _, r := range results {
		// Boilerplate included: a scaffold's runtime file declares what the
		// program's files call.
		for name, data := range r.Files {
			files[r.Target.Lang+"/"+r.Target.Platform+"/"+name] = data
		}
	}
	return files, nil
}

func safeParsePackage(fsys fstest.MapFS) (doc *snglast.Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parser panic: %v", r)
		}
	}()
	return build.ParsePackageFS(fsys)
}

// checkGenerated holds one generated file to what its language accepts, as
// far as a check that runs in process can say and file by file: JavaScript
// is parsed by esbuild. Go is checked a package at a time, by checkGoFiles.
func checkGenerated(name string, data []byte) error {
	switch filepath.Ext(name) {
	case ".js", ".mjs":
		return checkJS(name, data, api.LoaderJS)
	case ".ts":
		return checkJS(name, data, api.LoaderTS)
	}
	return nil
}

func checkJS(name string, data []byte, loader api.Loader) error {
	r := api.Transform(string(data), api.TransformOptions{Loader: loader, Sourcefile: name, LogLevel: api.LogLevelSilent})
	if len(r.Errors) == 0 {
		return nil
	}
	var b strings.Builder
	for _, m := range r.Errors {
		if m.Location != nil {
			fmt.Fprintf(&b, "%d:%d: ", m.Location.Line, m.Location.Column)
		}
		b.WriteString(m.Text)
		b.WriteString("\n")
	}
	return errors.New(b.String())
}

type pipelineSeed struct {
	name    string
	archive []byte
}

// pipelineSeeds is every fixture and every golden, as archives: a fixture is
// its one file, and a golden is its package with the generated code and the
// records taken out. Beside them, every archive testdata/pipeline holds.
func pipelineSeeds(tb testing.TB) []pipelineSeed {
	var out []pipelineSeed
	names, err := filepath.Glob("testdata/*.sngl")
	if err != nil {
		tb.Fatal(err)
	}
	for _, n := range names {
		data, err := os.ReadFile(n)
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, pipelineSeed{name: n, archive: txtar.Format(&txtar.Archive{Files: []txtar.File{{Name: "main.sngl", Data: data}}})})
	}
	goldens, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		tb.Fatal(err)
	}
	for _, n := range goldens {
		a, err := txtar.ParseFile(n)
		if err != nil {
			tb.Fatal(err)
		}
		a.Comment = nil
		a.Files = slices.DeleteFunc(a.Files, func(f txtar.File) bool {
			top, _, _ := strings.Cut(f.Name, "/")
			return top == "out" || top == "run" || top == "testrun" || top == "written"
		})
		out = append(out, pipelineSeed{name: n, archive: txtar.Format(a)})
	}
	pinned, err := filepath.Glob("testdata/pipeline/*.txtar")
	if err != nil {
		tb.Fatal(err)
	}
	for _, n := range pinned {
		data, err := os.ReadFile(n)
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, pipelineSeed{name: n, archive: data})
	}
	return out
}
