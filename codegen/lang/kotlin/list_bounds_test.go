package kotlin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// The Kotlin forms of the list intrinsics whose edges the three backends
// disagreed on (GitLab #100): subList returned a live view of the receiver and
// threw out of range, removeAt threw, where lib/builtin/methods.sngl documents
// a clamped copy and an out-of-range no-op.
//
// Like the Go and JS cases these are executed rather than pattern-matched.
// kotlinc costs seconds per invocation, so every case for both intrinsics is
// compiled and run as one script and the output lines are compared in order.

// emitKt renders intrinsic id over args typed by types, as codegen would.
func emitKt(t *testing.T, id string, names []string, types []*ir.Type) string {
	t.Helper()
	fn := codegen.LookupIntrinsic(langKt, id)
	if fn == nil {
		t.Fatalf("%s: no Kotlin emitter", id)
	}
	args := make([]ir.Expr, len(names))
	for i, n := range names {
		args[i] = &ir.Ident{Name: n, Type: types[i]}
	}
	code, _ := fn(args, func(e ir.Expr) string { return e.(*ir.Ident).Name })
	return code
}

// runKts runs script under kotlinc and returns the lines it marked as results.
func runKts(t *testing.T, script string) []string {
	t.Helper()
	if _, err := exec.LookPath("kotlinc"); err != nil {
		t.Skip("kotlinc is not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.kts")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("kotlinc", "-script", path)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kotlinc: %v\n--- script ---\n%s\n--- output ---\n%s", err, script, out)
	}
	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		// kotlinc writes its own warnings to the same stream; keep our markers.
		if after, ok := strings.CutPrefix(l, "case "); ok {
			lines = append(lines, after)
		}
	}
	return lines
}

func TestKotlinListBoundsBehaviour(t *testing.T) {
	slice := emitKt(t, "list.slice",
		[]string{"xs", "lo", "hi"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt, ir.TypInt})
	remove := emitKt(t, "list.remove",
		[]string{"xs", "i"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	type kase struct {
		name   string
		body   string
		expect string
	}
	var cases []kase

	// Bounds are clamped to [0, length]; an inverted range is empty. Every one
	// of these threw IndexOutOfBoundsException on the bare subList it replaces.
	for _, b := range []struct{ lo, hi, want string }{
		{"0", "99", "1,2,3"},
		{"-1", "3", "1,2,3"},
		{"-5", "2", "1,2"},
		{"-5", "99", "1,2,3"},
		{"2", "1", ""},
		{"99", "0", ""},
	} {
		cases = append(cases, kase{
			name: "slice(" + b.lo + ", " + b.hi + ")",
			body: "run { val xs = mutableListOf(1, 2, 3); val lo = " + b.lo + "; val hi = " + b.hi +
				"; println(\"case \" + (" + slice + ").joinToString(\",\")) }",
			expect: b.want,
		})
	}

	// The result is a copy, not the live subList view: growing it must not be
	// visible through the operand.
	cases = append(cases, kase{
		name: "slice returns a copy",
		body: "run { val xs = mutableListOf(1, 2, 3); val lo = 0; val hi = 2" +
			"; val head = (" + slice + ").toMutableList(); head.add(9)" +
			"; println(\"case \" + xs.joinToString(\",\")) }",
		expect: "1,2,3",
	})

	// An out-of-range or negative index leaves the list unchanged.
	for _, b := range []struct{ idx, want string }{
		{"99", "1,2,3"},
		{"-1", "1,2,3"},
		{"1", "1,3"},
	} {
		cases = append(cases, kase{
			name: "remove(" + b.idx + ")",
			body: "run { val xs = mutableListOf(1, 2, 3); val i = " + b.idx + "; " + remove +
				"; println(\"case \" + xs.joinToString(\",\")) }",
			expect: b.want,
		})
	}

	// The index is an arbitrary expression and the guard reads it more than
	// once, so it has to be bound before the guard runs.
	removeCall := emitKt(t, "list.remove",
		[]string{"xs", "next()"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})
	cases = append(cases, kase{
		name: "remove evaluates its index once",
		body: "run { val xs = mutableListOf(1, 2, 3); var calls = 0; fun next(): Int { calls++; return 1 }; " +
			removeCall + "; println(\"case \" + calls) }",
		expect: "1",
	})

	var script strings.Builder
	for _, c := range cases {
		script.WriteString(c.body + "\n")
	}
	got := runKts(t, script.String())

	if len(got) != len(cases) {
		t.Fatalf("got %d result lines, want %d:\n%s", len(got), len(cases), strings.Join(got, "\n"))
	}
	for i, c := range cases {
		if got[i] != c.expect {
			t.Errorf("%s = %q, want %q", c.name, got[i], c.expect)
		}
	}
}
