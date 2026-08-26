package golang

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The list intrinsics whose edges the three backends disagreed on (GitLab
// #100) are documented in lib/builtin/methods.sngl and implemented by
// internal/interp. Nothing compiled the Go they emit, so `list<int>.join(",")`
// produced code that did not build and slice/remove panicked where the
// declaration promises a clamp or a no-op.
//
// These tests emit each form, drop it into a real program and run it. Emitted
// text is not the subject — behaviour is.

// runGo compiles and runs body as the whole of main() and returns its stdout.
func runGo(t *testing.T, imports []string, body string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain is not installed")
	}
	dir := t.TempDir()

	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("import (\n\t\"fmt\"\n")
	for _, imp := range imports {
		if imp != "fmt" {
			b.WriteString("\t\"" + imp + "\"\n")
		}
	}
	b.WriteString(")\n\nvar _ = fmt.Sprint\n\nfunc main() {\n" + body + "\n}\n")

	src := b.String()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module listbounds\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n--- program ---\n%s\n--- output ---\n%s", err, src, out)
	}
	return strings.TrimSpace(string(out))
}

// emit renders intrinsic id over args typed by types, as codegen would.
func emit(t *testing.T, id string, names []string, types []*ir.Type) (string, []string) {
	t.Helper()
	fn := codegen.LookupIntrinsic(langGo, id)
	if fn == nil {
		t.Fatalf("%s: no Go emitter", id)
	}
	args := make([]ir.Expr, len(names))
	for i, n := range names {
		args[i] = &ir.Ident{Name: n, Type: types[i]}
	}
	return fn(args, func(e ir.Expr) string { return e.(*ir.Ident).Name })
}

func TestListJoinCompilesForNonStringElements(t *testing.T) {
	code, imports := emit(t, "list.join",
		[]string{"xs", "sep"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypString})

	got := runGo(t, imports, "\txs := []int{1, 2, 3}\n\tsep := \",\"\n\tfmt.Println("+code+")")
	if got != "1,2,3" {
		t.Errorf("list<int>.join(\",\") = %q, want %q", got, "1,2,3")
	}
}

func TestListJoinStillUsesStringsJoinForStrings(t *testing.T) {
	code, imports := emit(t, "list.join",
		[]string{"xs", "sep"},
		[]*ir.Type{ir.ListOf(ir.TypString), ir.TypString})

	got := runGo(t, imports, "\txs := []string{\"a\", \"b\"}\n\tsep := \"-\"\n\tfmt.Println("+code+")")
	if got != "a-b" {
		t.Errorf("list<string>.join(\"-\") = %q, want %q", got, "a-b")
	}
	// The fast path is worth keeping; a list<string> should not be reformatted
	// value by value.
	if !strings.Contains(code, "strings.Join(xs, sep)") {
		t.Errorf("list<string>.join emitted %q, want the direct strings.Join form", code)
	}
}

func TestListSliceClampsAndCopies(t *testing.T) {
	code, imports := emit(t, "list.slice",
		[]string{"xs", "lo", "hi"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt, ir.TypInt})

	// Every pair is documented to clamp to [0, length]; an inverted range is
	// empty. Each of these panicked on the Go slice expression this replaces.
	for _, tc := range []struct{ lo, hi, want string }{
		{"0", "99", "[1 2 3]"},
		{"-5", "2", "[1 2]"},
		{"-5", "99", "[1 2 3]"},
		{"2", "1", "[]"},
		{"99", "0", "[]"},
	} {
		body := "\txs := []int{1, 2, 3}\n\tlo, hi := " + tc.lo + ", " + tc.hi + "\n\tfmt.Println(" + code + ")"
		if got := runGo(t, imports, body); got != tc.want {
			t.Errorf("slice(%s, %s) = %s, want %s", tc.lo, tc.hi, got, tc.want)
		}
	}

	// The result is a copy: growing it must not write through to the operand.
	body := "\txs := []int{1, 2, 3}\n\tlo, hi := 0, 2\n\thead := " + code +
		"\n\thead = append(head, 9)\n\tfmt.Println(xs)"
	if got := runGo(t, imports, body); got != "[1 2 3]" {
		t.Errorf("slice aliased its operand: xs = %s, want [1 2 3]", got)
	}
}

func TestListRemoveIgnoresAnOutOfRangeIndex(t *testing.T) {
	code, imports := emit(t, "list.remove",
		[]string{"xs", "i"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	for _, tc := range []struct{ idx, want string }{
		{"99", "[1 2 3]"},
		{"-1", "[1 2 3]"},
		{"1", "[1 3]"},
	} {
		body := "\txs := []int{1, 2, 3}\n\ti := " + tc.idx + "\n\t" + code + "\n\tfmt.Println(xs)"
		if got := runGo(t, imports, body); got != tc.want {
			t.Errorf("remove(%s) left %s, want %s", tc.idx, got, tc.want)
		}
	}
}

// The index is an arbitrary expression, and the guard reads it more than once.
func TestListRemoveEvaluatesItsIndexOnce(t *testing.T) {
	code, imports := emit(t, "list.remove",
		[]string{"xs", "next()"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	body := "\txs := []int{1, 2, 3}\n\tcalls := 0\n\tnext := func() int { calls++; return 1 }\n\t" +
		code + "\n\tfmt.Println(calls)"
	if got := runGo(t, imports, body); got != "1" {
		t.Errorf("index evaluated %s times, want 1", got)
	}
}
