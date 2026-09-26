package lower

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPassOrderConstraints(t *testing.T) {
	at := make(map[string]int, len(passes))
	for i, p := range passes {
		at[p.name] = i
	}
	for _, c := range orderConstraints {
		i, ok := at[c.Earlier]
		if !ok {
			t.Errorf("constraint names no such pass %q\n\tconstraint: %s", c.Earlier, c)
			continue
		}
		j, ok := at[c.Later]
		if !ok {
			t.Errorf("constraint names no such pass %q\n\tconstraint: %s", c.Later, c)
			continue
		}
		if i >= j {
			t.Errorf("pass order violates a stated requirement:\n\t%s must run before %s\n\tbecause: %s\n\tregistry has %s at %d and %s at %d",
				c.Earlier, c.Later, c.Why, c.Earlier, i, c.Later, j)
		}
	}
}

func TestPassRegistryIsWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(passes))
	for i, p := range passes {
		if p.name == "" {
			t.Errorf("passes[%d] has no name", i)
		}
		if seen[p.name] {
			t.Errorf("duplicate pass name %q", p.name)
		}
		seen[p.name] = true
		if p.apply == nil {
			t.Errorf("passes[%d] (%s) has nil apply", i, p.name)
		}
		if p.enabled == nil {
			t.Errorf("passes[%d] (%s) has nil enabled", i, p.name)
		}
	}
}

// alwaysOn is the passes that answer for every target, so no capability gates
// them. It is a set and not an order: what it states is that a pass cannot
// quietly acquire a gate, or quietly lose one, without someone saying so.
var alwaysOn = []string{
	// A program naming sngl:async is refused on a target with no answer for
	// it, and every target is asked -- what differs is the answer.
	"AsyncCapable",
	"BoundaryFailed",
	"CSE",
	"ForElse",
	"ForeignPrimitive",
	"HoistBodyTypes",
	"HoistState",
	"IndexedIter",
	"InlinePure",
	"IterKind",
	// A helper a `sngl:` package declares and an emitted body calls has to be
	// in pkg.Funcs for any target, because that is what every backend emits
	// from.
	"LibFuncs",
	// Which bindings are written is a fact about the finished bodies, and
	// every target has a backend that may care -- Kotlin copies on binding
	// unless this says the binding is never written.
	"MutatedVars",
	// A prop read off a `#id` is answered by what the *tree* says the node's
	// primitive keeps, so every target runs the pass and the declaration
	// decides what it does.
	"NodePropReads",
	"PlatformExtensionBody",
	"PropBindings",
	"Query",
	"RecursionDepth",
	"RefLoop",
	"SpreadOnce",
	"StampUsage",
	// A context nothing provides is folded to its default on every target: the
	// ones that lower contexts to state, and the ones that keep them.
	"UnprovidedContext",
	"ViewForElse",
	"WindowNesting",
}

func TestAlwaysOnPasses(t *testing.T) {
	// NoLowering is the target that claims everything and asks for nothing, so
	// what survives the filter is exactly the ungated set -- which states both
	// halves at once: a pass missing here has grown a gate, and an extra one
	// has lost its.
	//
	// It used to be the empty Features, back when empty meant "no pass
	// requested". Under the polarity that is now every pass.
	got := slices.Clone(EnabledPasses(NoLowering()))
	slices.Sort(got)
	if !slices.Equal(got, alwaysOn) {
		t.Errorf("EnabledPasses(NoLowering()) = %v\nwant %v", got, alwaysOn)
	}
}

// soleGate is what each capability asks for on its own: the passes
// EnabledPasses adds when that Features field is the only one differing from a
// target that claims everything. Most rows are the one pass named after the
// field; the rows that are not are where a capability's cost is more than its
// name says, and each carries why.
//
// The pass names still read "No…" where the field no longer does. A pass name
// is what `dump --list` prints and what a fixture header asks for, so it
// outlived the record its gate was named after.
//
// This is the other half of what the old filtered list said, and no ordering
// constraint can say it -- a gate is what a pass asks of a target, not what it
// asks of another pass. Unlike that list it is indexed by capability rather
// than by position, so a row is wrong only when the gate it names changed.
var soleGate = map[string][]string{
	"Toggle":        {"NoToggle"},
	"Ternary":       {"NoTernary"},
	"Lambda":        {"NoLambda"},
	"Ref":           {"NoRef"},
	"Unit":          {"NoUnit"},
	"Enum":          {"NoEnum"},
	"AsyncReactive": {"NoAsyncReactive"},
	"Computed":      {"NoComputed"},
	"ListLambdas":   {"NoListLambdas"},
	"AsyncCalls":    {"NoAsyncCalls"},

	// Both context flags request the one pass: a target asking for either a
	// component receiver or a threaded stdlib param needs the context rewrite,
	// and the pass reads the flags itself to decide how far to thread.
	"StructComponents":   {"Context"},
	"StdlibContextParam": {"Context"},

	// hasInstanceRuntime reads Reactivity and nothing else, so a target that
	// keeps its reactivity gets none of the instance machinery.
	"Reactivity": {"ComponentProps", "InstanceBodies", "InstanceEvents", "InstanceSlots", "NoReactivity"},

	// NodeEscape has no flag of its own: the escape analysis only has
	// something to analyse once the tree is flat.
	"Declarative": {"NoDeclarative", "NodeEscape"},

	"InlineComponents": {"NoInlineComponents"},
	"ImplicitRecv":     {"NoImplicitRecv"},
	"StructSpread":     {"NoStructSpread"},
	"FocusOrder":       {"FocusOrder"},
	"Canvas":           {"Canvas"},

	// Gates nothing. It is the one capability the lowering never reads: the
	// build asks it, to decide whether a constant view loop is unrolled.
	"ViewStatements": nil,

	// Gates no pass either, though the lowering reads it: passNoInlineComponents
	// asks it whether a component with state of its own is built at run time or
	// spliced with a cell per copy.
	"InstanceState": nil,

	// The pass and the field are named for opposite sides of the same fact:
	// ReactiveCanvas is the redraw the platform wants, CanvasReactivity is the
	// pass that injects it. Effects is the same shape inverted -- withholding
	// it is what asks for the pass.
	"ReactiveCanvas": {"CanvasReactivity"},
	"Effects":        {"Effect"},

	// Alone it turns on nothing: retaining a slot child buys the placement
	// match and nothing else, so it is only ever asked alongside an instance
	// runtime. See TestASlotChildNeedsBothCapabilities.
	"InsertBefore": nil,

	// Also gates nothing: the offload pass is asked for by NoAsyncCalls and
	// reads this itself to choose between the rewrite and refusing the
	// program, so a target that can post back still needs the first flag.
	"AsyncPost": nil,

	// Nor this one. passAsyncCapable runs for every target and reads both
	// async flags to decide which half of the build to name in its refusal,
	// so neither turns a pass on.
	"AsyncSpawn": nil,
}

func gatedPasses(c Features) []string {
	always := make(map[string]bool, len(alwaysOn))
	for _, n := range alwaysOn {
		always[n] = true
	}
	var out []string
	for _, n := range EnabledPasses(c) {
		if !always[n] {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// TestEachCapabilityGatesItsPasses walks the Features fields themselves, so a
// new capability with no row here fails rather than passing unexamined.
//
// Each field is *flipped* from what NoLowering holds rather than set true,
// which is what makes one driver serve three kinds of field: a capability is
// true there and withdrawing it asks for a pass, while a want or a grant is
// false there and setting it does. Under the old record every field read the
// same way round and setting one on the zero value was enough.
func TestEachCapabilityGatesItsPasses(t *testing.T) {
	rt := reflect.TypeFor[Features]()
	for i := range rt.NumField() {
		f := rt.Field(i)
		want, ok := soleGate[f.Name]
		if !ok {
			t.Errorf("Features.%s gates no passes here; add a row to soleGate saying what it asks for", f.Name)
			continue
		}
		base := NoLowering()
		v := reflect.ValueOf(&base).Elem()
		v.Field(i).SetBool(!v.Field(i).Bool())
		t.Run(f.Name, func(t *testing.T) {
			if got := gatedPasses(base); !slices.Equal(got, want) {
				t.Errorf("flipping Features.%s enables %v; want %v", f.Name, got, want)
			}
		})
	}
}

// TestASlotChildNeedsBothCapabilities is the one gate two flags answer for:
// a container that can only append tears its children down every render, so
// there is no identity for a retained slot child to be asked about.
func TestASlotChildNeedsBothCapabilities(t *testing.T) {
	want := []string{"ComponentProps", "InstanceBodies", "InstanceEvents", "InstanceSlots", "NoReactivity", "SlotChildInstances"}
	if got := gatedPasses(withInsertBefore("reactivity")); !slices.Equal(got, want) {
		t.Errorf("gated passes = %v; want %v", got, want)
	}
	alone := NoLowering()
	alone.InsertBefore = true
	if got := gatedPasses(alone); len(got) != 0 {
		t.Errorf("InsertBefore alone enables %v; want nothing", got)
	}
}

// TestEveryPassIsNamedBySomeGate is the other half of what the old full-order
// list caught. That list noticed a pass disappearing because it wrote every
// name down in order; this notices it because every pass is either always on
// or asked for by a capability, and both halves are stated. A pass named
// nowhere is one whose removal no test would report.
func TestEveryPassIsNamedBySomeGate(t *testing.T) {
	named := make(map[string]bool, len(passes))
	for _, n := range alwaysOn {
		named[n] = true
	}
	for _, ns := range soleGate {
		for _, n := range ns {
			named[n] = true
		}
	}
	named["SlotChildInstances"] = true // TestASlotChildNeedsBothCapabilities
	for _, p := range passes {
		if !named[p.name] {
			t.Errorf("no test names pass %q: it is not in alwaysOn and no capability row asks for it, so deleting it would be silent", p.name)
		}
	}
}

// TestEveryDeclaredPassIsRegistered reads the package's own source for
// `var passX = pass{name: "..."}` and requires each to be in the registry.
//
// This is the half of the old full-order list worth keeping: it caught a pass
// dropping out of `passes` while its file stayed put, which is a silent change
// of what every target compiles. Derived from the source rather than written
// down, so adding a pass needs no edit here.
func TestEveryDeclaredPassIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package source: %v", err)
	}
	registered := make(map[string]bool, len(passes))
	for _, p := range passes {
		registered[p.name] = true
	}
	declared := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, d := range file.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.CompositeLit)
					if !ok {
						continue
					}
					if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "pass" {
						continue
					}
					declared++
					name := passNameField(t, lit)
					if !registered[name] {
						t.Errorf("%s declares a pass named %q that %s does not register",
							path, name, "passes")
					}
				}
			}
		}
	}
	if declared != len(passes) {
		t.Errorf("source declares %d passes; registry holds %d", declared, len(passes))
	}
}

func passNameField(t *testing.T, lit *ast.CompositeLit) string {
	t.Helper()
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "name" {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			t.Fatalf("pass name is not a string literal at %v", kv.Value)
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			t.Fatalf("pass name %s: %v", bl.Value, err)
		}
		return s
	}
	t.Fatal("pass literal has no name field")
	return ""
}
