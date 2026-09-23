package sngl_test

import (
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/tools/txtar"

	"git.duckfam.us/jonathan/sngl/internal/build"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestWalkMatchesRewriteOnCorpus is ir's lockstep test over real programs:
// ir.Walk and a no-op ir.Rewrite must visit the same nodes in the same order
// in every testdata program as checked, and in every golden fixture's IR as
// each of its targets leaves it after lowering and codegen -- which is where
// the synthesized shapes (closures, redraws, lifted handlers) are. The checked
// .sngl half costs as much again as the goldens and adds no lowered IR, so
// -short leaves it out.
func TestWalkMatchesRewriteOnCorpus(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var sngls []string
	if !testing.Short() {
		var err error
		if sngls, err = filepath.Glob("testdata/*.sngl"); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range sngls {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := parser.Parse(filepath.Base(file), src)
		if err != nil {
			continue
		}
		pkg, _ := build.Check(doc, build.CheckConfig{Dir: "testdata", IsMain: true})
		if pkg != nil {
			assertSameVisits(t, file, pkg)
		}
	}

	archives, err := filepath.Glob("testdata/*.txtar")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archives {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		fsys := fstest.MapFS{}
		var main string
		for _, f := range txtar.Parse(raw).Files {
			name := path.Clean(f.Name)
			if strings.HasPrefix(name, "out/") || strings.HasPrefix(name, "run/") {
				continue
			}
			fsys[name] = &fstest.MapFile{Data: f.Data}
			if main == "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".sngl") {
				main = name
			}
		}
		doc, err := build.ParsePackageFS(fsys)
		if err != nil {
			continue
		}
		pkg, err := build.Check(doc, build.CheckConfig{Dir: ".", FS: fsys, Resolver: build.NewFSResolver(fsys), IsMain: true})
		if err != nil {
			continue
		}
		assertSameVisits(t, file+" (checked)", pkg)
		results, err := build.Emit(pkg, build.Options{Name: main, Dir: ".", OutDir: ".", ProjectFS: fsys})
		if err != nil {
			continue
		}
		for _, res := range results {
			assertSameVisits(t, file+" ("+res.Target.Lang+"/"+res.Target.Platform+")", res.Pkg)
		}
	}
}

func assertSameVisits(t *testing.T, label string, pkg *ir.Package) {
	t.Helper()
	controls := map[string]func(i int) error{
		"all": func(int) error { return nil },
		"skipdir": func(i int) error {
			if i%5 == 2 {
				return ir.SkipDir
			}
			return nil
		},
	}
	for name, ctl := range controls {
		var walked, rewritten []ir.Node
		_ = ir.Walk(pkg, func(n ir.Node) error {
			walked = append(walked, n)
			return ctl(len(walked) - 1)
		})
		_ = ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
			rewritten = append(rewritten, n)
			return n, ctl(len(rewritten) - 1)
		})
		for i := range min(len(walked), len(rewritten)) {
			if walked[i] != rewritten[i] {
				t.Errorf("%s/%s: Walk and Rewrite diverge at visit %d: %T against %T", label, name, i, walked[i], rewritten[i])
				return
			}
		}
		if len(walked) != len(rewritten) {
			t.Errorf("%s/%s: Walk made %d visits, Rewrite %d", label, name, len(walked), len(rewritten))
		}
	}
}
