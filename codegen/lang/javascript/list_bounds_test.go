package javascript

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The JS forms of the list intrinsics whose edges the backends disagreed on
// (GitLab #100). JS was the closest to the documented behaviour, but both
// Array.prototype.slice and .splice read a negative index as an offset from
// the end, where lib/builtin/methods.sngl clamps to [0, length] and treats an
// out-of-range remove as a no-op.

// runJS runs body under node and returns what it printed.
func runJS(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mjs"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "main.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n--- program ---\n%s\n--- output ---\n%s", err, body, out)
	}
	return strings.TrimSpace(string(out))
}

func emitJS(t *testing.T, id string, names []string, types []*ir.Type) string {
	t.Helper()
	fn := codegen.LookupIntrinsic(langJS, id)
	if fn == nil {
		t.Fatalf("%s: no JS emitter", id)
	}
	args := make([]ir.Expr, len(names))
	for i, n := range names {
		args[i] = &ir.Ident{Name: n, Type: types[i]}
	}
	code, _ := fn(args, func(e ir.Expr) string { return e.(*ir.Ident).Name })
	return code
}

func TestJSListSliceClampsAndCopies(t *testing.T) {
	code := emitJS(t, "list.slice",
		[]string{"xs", "lo", "hi"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt, ir.TypInt})

	for _, tc := range []struct{ lo, hi, want string }{
		{"0", "99", "1,2,3"},
		// A negative start that does not saturate to 0 is where raw .slice
		// diverges: .slice(-1, 3) counts from the end and yields just [3].
		{"-1", "3", "1,2,3"},
		{"-5", "2", "1,2"},
		{"-5", "99", "1,2,3"},
		{"2", "1", ""},
		{"99", "0", ""},
	} {
		body := "const xs = [1, 2, 3]; const lo = " + tc.lo + ", hi = " + tc.hi +
			"; console.log((" + code + ").join(\",\"))"
		if got := runJS(t, body); got != tc.want {
			t.Errorf("slice(%s, %s) = %q, want %q", tc.lo, tc.hi, got, tc.want)
		}
	}

	// .slice already copies; keep it that way.
	body := "const xs = [1, 2, 3]; const lo = 0, hi = 2; const head = " + code +
		"; head.push(9); console.log(xs.join(\",\"))"
	if got := runJS(t, body); got != "1,2,3" {
		t.Errorf("slice aliased its operand: xs = %q, want %q", got, "1,2,3")
	}
}

func TestJSListRemoveIgnoresAnOutOfRangeIndex(t *testing.T) {
	code := emitJS(t, "list.remove",
		[]string{"xs", "i"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	for _, tc := range []struct{ idx, want string }{
		{"99", "1,2,3"},
		{"-1", "1,2,3"}, // .splice(-1, 1) would have dropped the last item
		{"1", "1,3"},
	} {
		body := "const xs = [1, 2, 3]; const i = " + tc.idx + "; " + code +
			"; console.log(xs.join(\",\"))"
		if got := runJS(t, body); got != tc.want {
			t.Errorf("remove(%s) left %q, want %q", tc.idx, got, tc.want)
		}
	}
}

func TestJSListRemoveEvaluatesItsIndexOnce(t *testing.T) {
	code := emitJS(t, "list.remove",
		[]string{"xs", "next()"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	body := "const xs = [1, 2, 3]; let calls = 0; const next = () => { calls++; return 1; }; " +
		code + "; console.log(calls)"
	if got := runJS(t, body); got != "1" {
		t.Errorf("index evaluated %s times, want 1", got)
	}
}
