package c_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// TestRun holds the translator to what each testdata/run archive says a
// program prints. An archive holds app.sngl, whose functions are written as
// C into program.c; main.c, which includes program.c and prints what its
// functions answer; and want, which is what it prints. Each runs with every
// compiler on the host, under each standard the output holds to, so a
// construct one compiler accepts and another refuses fails here.
func TestRun(t *testing.T) {
	compilers := clang.Compilers()
	if len(compilers) == 0 {
		t.Skip("no C compiler on the host")
	}
	archives, err := filepath.Glob("testdata/run/*.txtar")
	if err != nil || len(archives) == 0 {
		t.Fatalf("no testdata/run archives: %v", err)
	}
	for _, path := range archives {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".txtar"), func(t *testing.T) {
			a, err := txtar.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, f := range a.Files {
				files[f.Name] = f.Data
			}
			program := translate(t, files["app.sngl"])
			dir := t.TempDir()
			if err := clang.WriteRuntime(dir); err != nil {
				t.Fatal(err)
			}
			t.Logf("program.c:\n%s", program)
			write(t, dir, "program.c", program)
			write(t, dir, "main.c", files["main.c"])
			for _, cc := range compilers {
				for _, std := range clang.Standards {
					t.Run(cc.Name+"/"+std, func(t *testing.T) {
						exe := filepath.Join(dir, cc.Name+"-"+std)
						if err := cc.Build(dir, []string{"main.c", "sngl.c"}, exe, std); err != nil {
							t.Fatalf("%v\nprogram.c:\n%s", err, program)
						}
						got := run(t, cc, exe)
						if want := string(files["want"]); got != want {
							t.Errorf("printed\n%s\nwant\n%s\nprogram.c:\n%s", got, want, program)
						}
					})
				}
			}
		})
	}
}

// translate is src's functions and structs, checked and lowered as a build
// for C lowers them, written as C.
func translate(t *testing.T, src []byte) []byte {
	t.Helper()
	doc, err := parser.Parse("app.sngl", src)
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	pkg = ir.ClonePackageFor(pkg, "none", "c")
	feats, err := codegen.CapsFor("c", "none")
	if err != nil {
		t.Fatal(err)
	}
	if err := lower.Lower(pkg, feats, lower.Options{Platform: "none", Language: "c"}); err != nil {
		t.Fatal(err)
	}
	sink := codegen.NewMemSink()
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "program.c"}, pkg, "none")
	for _, sd := range pkg.Structs {
		fe.Struct(sd)
	}
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			fe.Func(fn)
		}
	}
	if err := fe.Close(); err != nil {
		t.Fatal(err)
	}
	return sink.Files()["program.c"]
}

func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// run is what exe prints, under Wine where cc builds for Windows.
func run(t *testing.T, cc clang.Compiler, exe string) string {
	t.Helper()
	name, args := exe, []string(nil)
	if cc.Windows {
		wine, err := exec.LookPath("wine")
		if err != nil {
			t.Skip("no wine to run a Windows executable")
		}
		name, args = wine, []string{exe}
	}
	var out, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, stderr.String())
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}
