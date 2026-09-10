package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The remote.Value<T> surface is asserted here rather than in a testdata fixture
// because every platform's TestFixtures feeds testdata/*.sngl to its own
// backend, and no backend can emit a Value until the query lowering and the
// pkg/<lang>/remote runtimes land.

const remotePrelude = `import remote "sngl:remote"
import ui "sngl:ui"

struct User {
    name string
}

func users() remote.Value<list<User>> {
    return remote.of([User{name = "ada"}])
}

func count() remote.Value<int> {
    return remote.of(1)
}
`

// A Value's four accessors and three derived predicates all dispatch. The
// derived ones resolve through their SNGL bodies, so a backend implements the
// intrinsics and not the predicates.
func TestRemoteMethodsDispatch(t *testing.T) {
	for _, expr := range []string{
		"users.inFlight()",
		"users.loading()",
		"users.ready()",
		"users.failed()",
	} {
		src := remotePrelude + "\ncomponent c() ui.node {\n    var b bool = " + expr + "\n}\n"
		if errs := checkSrc(t, src); len(errs) > 0 {
			t.Errorf("%s: %v", expr, errs[0].Error())
		}
	}
}

// or() is the way out of the box, and the type argument travels through it.
func TestRemoteOrUnwrapsToTheTypeArgument(t *testing.T) {
	src := remotePrelude + `
component c() ui.node {
    var names list<User> = users.or([])
    var n int = count.or(0)
}
`
	if errs := checkSrc(t, src); len(errs) > 0 {
		t.Fatalf("check: %v", errs[0].Error())
	}
}

// error() carries the package's own Failure rather than app.error.
func TestRemoteErrorIsAnOptionalFailure(t *testing.T) {
	src := remotePrelude + `
component c() ui.node {
    var why option<remote.Failure> = users.error()
}
`
	if errs := checkSrc(t, src); len(errs) > 0 {
		t.Fatalf("check: %v", errs[0].Error())
	}
}

// A Value<T> is reached only through a constructor. Neither direction of the
// implicit conversion holds: a bare T does not become a box, because a Value
// carries fetch state no plain value implies, and a box does not decay to a T,
// because that would render a value that has not arrived. This is where the type
// deliberately parts company with option<T>, which promotes in one direction.
func TestRemoteDoesNotConvertImplicitly(t *testing.T) {
	for name, decl := range map[string]string{
		"T does not become a Value": `
func boxed() remote.Value<int> {
    return 1
}`,
		"a Value does not decay to T": `
func unboxed() int {
    return remote.of(1)
}`,
	} {
		t.Run(name, func(t *testing.T) {
			errs := checkSrc(t, "import remote \"sngl:remote\"\n"+decl+"\n")
			if len(errs) == 0 {
				t.Fatal("the conversion was accepted")
			}
			if got := errs[0].Error(); !strings.Contains(got, "remote.Value<int>") {
				t.Errorf("diagnostic does not name the type: %s", got)
			}
		})
	}
}

// The three constructors build the three states directly, which is what lets a
// test render each branch of a view without a fetch.
func TestRemoteConstructorsBuildEachState(t *testing.T) {
	src := `import remote "sngl:remote"
import ui "sngl:ui"

component c() ui.node {
    var settled remote.Value<int> = remote.of(1)
    var broken remote.Value<int> = remote.failedWith(remote.Failure{kind = transport, message = "no route", code = ""})
    var waiting remote.Value<int> = remote.pending()
}
`
	if errs := checkSrc(t, src); len(errs) > 0 {
		t.Fatalf("check: %v", errs[0].Error())
	}
}

// A missing type argument is reported where it is written, the way list and
// option report theirs.
func TestRemoteRequiresATypeArgument(t *testing.T) {
	src := `import remote "sngl:remote"
import ui "sngl:ui"

component c() ui.node {
    var r remote.Value = 0
}
`
	errs := checkSrc(t, src)
	if len(errs) == 0 {
		t.Fatal("a bare remote.Value checked")
	}
	if got := errs[0].Error(); !strings.Contains(got, "requires a type argument") {
		t.Errorf("unexpected diagnostic: %s", got)
	}
}

// A #[builtin] generic is the built-in however it was reached. Unqualified
// resolution dispatched on the kind; qualified resolution did not, so
// remote.Value<T> came back an ordinary generic struct that shared a declaration
// with the built-in but not a type, and failed to unify with itself.
//
// remote.Value is likely the first generic built-in a program can name
// qualified: list, map and option are ambient, and importing sngl:builtin
// explicitly is an error. So this asserts the rule rather than one type.
func TestQualifiedBuiltinGenericIsTheBuiltin(t *testing.T) {
	src := `import remote "sngl:remote"
import ui "sngl:ui"

func a() remote.Value<int> {
    return remote.of(1)
}

component c() ui.node {
    var v remote.Value<int> = a()
}
`
	if errs := checkSrc(t, src); len(errs) > 0 {
		t.Fatalf("a qualified built-in generic did not unify with itself: %v", errs[0].Error())
	}
}

// A Value is total: every accessor answers on one nothing has fetched, so no
// reader can fault. That rests on remote.Value<T> having a zero — the idle box.
func TestRemoteHasAnIdleZero(t *testing.T) {
	z := ir.ZeroExpr(ir.RemoteOf(ir.TypInt))
	if z == nil {
		t.Fatal("remote.Value<int> has no zero; a box nothing fetched would have no value to read")
	}
	lit, ok := z.(*ir.StructLit)
	if !ok {
		t.Fatalf("zero of remote.Value<int> is %T, want an empty composite", z)
	}
	if len(lit.Fields) != 0 {
		t.Errorf("zero sets %d field(s); idle means none set", len(lit.Fields))
	}
	if lit.Type == nil || lit.Type.Kind != ir.TypeRemote {
		t.Errorf("zero has type %v, want a Value", lit.Type)
	}
}

// The zero is a zero of remote.Value<T>, never a zero of T. Seeding with the
// latter is what made a fetch in flight and a fetch that returned an empty list
// the same value.
func TestRemoteZeroIsNotTheWrappedZero(t *testing.T) {
	z := ir.ZeroExpr(ir.RemoteOf(ir.ListOf(ir.TypInt)))
	if _, isList := z.(*ir.ListLit); isList {
		t.Fatal("zero of remote.Value<list<int>> is an empty list; it must be an idle box")
	}
}
