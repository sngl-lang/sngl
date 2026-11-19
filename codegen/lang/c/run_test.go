package c_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/platform/win32"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

var (
	update = flag.Bool("update", false, "rebuild and rerun each testdata/run program whose digest moved, and rewrite its record")
	verify = flag.Bool("verify", false, "rebuild and rerun every testdata/run program, whatever its record says")
)

// TestRun holds the translator to what each testdata/run archive says a
// program prints. An archive holds app.sngl, whose functions are written as
// C into program.c; main.c, which includes program.c and prints what its
// functions answer; and want, which is what it prints. Each runs with every
// compiler of the Matrix, under each standard the output holds to, so a
// construct one compiler accepts and another refuses fails here.
//
// A build and run is recorded, in testdata/run_records, as a digest of what
// it compiled, how and what it must print: a plain run compares the digest
// and launches nothing, so it needs none of the compilers. -update rebuilds
// what moved and -verify everything, on a host with all of them.
func TestRun(t *testing.T) {
	archives, err := filepath.Glob("testdata/run/*.txtar")
	if err != nil || len(archives) == 0 {
		t.Fatalf("no testdata/run archives: %v", err)
	}
	for _, path := range archives {
		archive := strings.TrimSuffix(filepath.Base(path), ".txtar")
		t.Run(archive, func(t *testing.T) {
			a, err := txtar.ParseFile(path)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, f := range a.Files {
				files[f.Name] = f.Data
			}
			program := translate(t, files["app.sngl"])
			files["program.c"] = program
			maps.Copy(files, clang.Runtime())
			dir := t.TempDir()
			for _, name := range []string{"program.c", "main.c"} {
				write(t, dir, name, files[name])
			}
			if err := clang.WriteRuntime(dir); err != nil {
				t.Fatal(err)
			}
			for _, cc := range clang.Matrix() {
				// Built again under AddressSanitizer and UBSan, which stop it at
				// the first undefined behavior or bad access, the arena's
				// included, and at exit at a block nothing reaches, where the
				// compiler has them.
				builds := map[string][]string{}
				if !cc.Windows {
					builds["sanitized"] = sanitize
				}
				for _, std := range clang.Standards {
					builds[std] = nil
				}
				for _, variant := range slices.Sorted(maps.Keys(builds)) {
					t.Run(cc.Name+"/"+variant, func(t *testing.T) {
						std, extra := variant, builds[variant]
						if extra != nil {
							std = "c17"
						}
						record := filepath.Join("testdata", "run_records", archive, cc.Name+"-"+variant)
						digest := runDigest(cc.Name, append(clang.Flags(std), extra...), files)
						if recorded(t, record, digest) {
							return
						}
						if cc.Path == "" {
							t.Fatalf("%s: cannot record: no %s on the host", record, cc.Name)
						}
						exe := filepath.Join(dir, cc.Name+"-"+variant)
						if err := cc.Build(dir, []string{"main.c", "sngl.c"}, exe, std, extra...); err != nil {
							t.Fatalf("%v\nprogram.c:\n%s", err, program)
						}
						if got, want := run(t, cc, exe), string(files["want"]); got != want {
							t.Fatalf("printed\n%s\nwant\n%s\nprogram.c:\n%s", got, want, program)
						}
						if *update {
							if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(record, []byte(digest+"\n"), 0o644); err != nil {
								t.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

// recorded says the record at path already answers digest, so nothing need
// be built: false under -verify, or under -update when it moved. A plain run
// whose digest moved, or has no record, fails naming -update.
func recorded(t *testing.T, path, digest string) bool {
	t.Helper()
	have, err := os.ReadFile(path)
	matched := err == nil && string(have) == digest+"\n"
	switch {
	case *verify:
		return false
	case matched:
		return true
	case *update:
		return false
	case err != nil:
		t.Fatalf("%s: no record of this run; rerun with -update -- `go tool verify -update` rerecords every package's", path)
	default:
		t.Fatalf("%s: the program changed since it last ran; rerun with -update -- `go tool verify -update` rerecords every package's", path)
	}
	return false
}

// runDigest covers the compiler, its arguments, and every file the build
// reads or the run is held to, the runtime and want included.
func runDigest(cc string, args []string, files map[string][]byte) string {
	h := sha256.New()
	field := func(s string) { fmt.Fprintf(h, "%d\x00%s", len(s), s) }
	field(cc)
	for _, a := range args {
		field(a)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if name == "app.sngl" {
			// program.c is what it says to C.
			continue
		}
		field(name)
		field(string(files[name]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// sanitize are the flags a sanitized build adds: GCC's undefined leaves out
// a float cast past an int's range, which is named on its own.
var sanitize = []string{"-fsanitize=address,undefined,float-cast-overflow", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer", "-g"}

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
	// main.c calls the fixture's functions, which the IR cannot see, so a
	// test calling each stands for it: a test is a root the shake keeps
	// what it reaches from, and is itself never written.
	keep := &ir.Func{Name: "testKeep", IsTest: true, Synthesized: true}
	for _, fn := range pkg.Funcs {
		// A generic function is written for the types its callers name,
		// and a type parameter has no zero to call it with here.
		if len(fn.TypeParams) > 0 {
			continue
		}
		// Nor has a `ref` a zero: a C function taking one is reached
		// through the function calling it.
		args := make([]ir.CallArg, len(fn.Params))
		for i, p := range fn.Params {
			args[i] = ir.CallArg{Value: ir.ZeroExpr(p.Type)}
		}
		if slices.ContainsFunc(args, func(a ir.CallArg) bool { return a.Value == nil }) {
			continue
		}
		keep.Block = append(keep.Block, &ir.CallStmt{Call: &ir.Call{Func: fn, Type: fn.Return, Args: args}})
	}
	pkg.Funcs = append(pkg.Funcs, keep)
	feats, err := codegen.CapsFor("c", "none")
	if err != nil {
		t.Fatal(err)
	}
	// Optimized either side of lowering, as a build's target is
	// (internal/build): the inliner is what spells a builtin's method out.
	opt := &optimize.Config{Platform: "none", Language: "c"}
	if err := optimize.Optimize(pkg, opt); err != nil {
		t.Fatal(err)
	}
	if err := lower.Lower(pkg, feats, lower.Options{Platform: "none", Language: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := optimize.Optimize(pkg, opt); err != nil {
		t.Fatal(err)
	}
	sink := codegen.NewMemSink()
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "program.c"}, pkg, "none")
	for _, sd := range pkg.Structs {
		fe.Struct(sd)
	}
	// A C function is C's to define, as a build leaves it (program.go).
	for _, fn := range pkg.Funcs {
		if !fn.IsTest && fn.Foreign.Name == "" {
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
	exe = cc.Exe(exe)
	name, args := exe, []string(nil)
	if cc.Windows {
		wine, err := exec.LookPath("wine")
		if err != nil {
			t.Skip("no wine to run a Windows executable")
		}
		name, args = wine, []string{exe}
	}
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	if cc.Windows {
		env, err := win32.WineEnv(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = env
	}
	cmd.Stdout = &out
	stderr, err := win32.StderrToFile(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, stderr())
	}
	stderr()
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}
