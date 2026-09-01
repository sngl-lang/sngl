package ir_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Decl on a collection is metadata, never identity: comparing it would make a
// list built before the library loaded a different type from one built after.
func TestCollectionEqualityIgnoresTheDeclaration(t *testing.T) {
	sd := &ir.StructDef{Name: "list", Builtin: ir.BuiltinList}
	bare := &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypInt}}
	decl := &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypInt}, Decl: sd}
	if !bare.Equal(decl) || !decl.Equal(bare) {
		t.Errorf("list<int> with a declaration is not equal to one without")
	}

	md := &ir.StructDef{Name: "map", Builtin: ir.BuiltinMap}
	bareMap := &ir.Type{Kind: ir.TypeMap, Elems: []*ir.Type{ir.TypString, ir.TypInt}}
	declMap := &ir.Type{Kind: ir.TypeMap, Elems: []*ir.Type{ir.TypString, ir.TypInt}, Decl: md}
	if !bareMap.Equal(declMap) || !declMap.Equal(bareMap) {
		t.Errorf("map<string, int> with a declaration is not equal to one without")
	}
}

// Substitute rebuilds a collection type, so it has to carry the declaration
// across: `list<T>` instantiated at int is no less declared than `list<T>`.
func TestSubstitutePreservesTheDeclaration(t *testing.T) {
	sd := &ir.StructDef{Name: "list", Builtin: ir.BuiltinList}
	generic := &ir.Type{
		Kind:  ir.TypeList,
		Elems: []*ir.Type{{Kind: ir.TypeTypeParam, ParamName: "T"}},
		Decl:  sd,
	}
	got := generic.Substitute(map[string]*ir.Type{"T": ir.TypInt})
	if got.String() != "list<int>" {
		t.Fatalf("Substitute = %s, want list<int>", got)
	}
	if got.Decl != sd {
		t.Errorf("Substitute dropped the declaration")
	}
}

// An id is the dispatch key every backend answers to, so two declarations of
// one id means half its call sites reach the wrong signature. Only library
// source can carry the mark, so this is a compiler error and panics.
func TestDuplicateIntrinsicPanics(t *testing.T) {
	first := ir.IntrinsicDef{Name: "test.dupe", Pkg: "sngl:one", DeclaredAs: "one.dupe"}
	ir.RegisterIntrinsic(first)

	// The same declaration again, as a second check of the same library does.
	ir.RegisterIntrinsic(first)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("no panic for two declarations of one intrinsic id")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "sngl:one.one.dupe") || !strings.Contains(msg, "sngl:two.two.dupe") {
			t.Errorf("panic %q does not name both declarations", msg)
		}
	}()
	ir.RegisterIntrinsic(ir.IntrinsicDef{Name: "test.dupe", Pkg: "sngl:two", DeclaredAs: "two.dupe"})
}

// The generic collection intrinsics are written against type variables, and a
// caller binds them at the element type it has -- which is what keeps
// list<dyn> out of the IR of the passes that synthesize a call from one.
func TestGenericIntrinsicsInstantiate(t *testing.T) {
	// As lib/builtin/lists.sngl declares it. The registry holds what a check
	// registered, and this package's tests run no checker.
	tvT := &ir.Type{Kind: ir.TypeTypeParam, ParamName: "T"}
	tvU := &ir.Type{Kind: ir.TypeTypeParam, ParamName: "U"}
	def := &ir.IntrinsicDef{
		Name:       "list.push",
		TypeParams: []string{"T"},
		Params: []*ir.Param{
			{Name: "l", Type: ir.ListOf(tvT)},
			{Name: "item", Type: tvT},
		},
		Return: ir.ListOf(tvT),
	}
	if len(def.TypeParams) != 1 || def.TypeParams[0] != "T" {
		t.Fatalf("TypeParams = %v, want [T]", def.TypeParams)
	}
	params, ret := def.Instantiate(ir.TypString)
	if got := params[0].Type.String(); got != "list<string>" {
		t.Errorf("param 0 = %s, want list<string>", got)
	}
	if got := params[1].Type.String(); got != "string" {
		t.Errorf("param 1 = %s, want string", got)
	}
	if got := ret.String(); got != "list<string>" {
		t.Errorf("return = %s, want list<string>", got)
	}

	// Unbound leaves the variable in place rather than degrading to dyn.
	params, _ = def.Instantiate()
	if got := params[1].Type.String(); got != "T" {
		t.Errorf("uninstantiated param 1 = %s, want T", got)
	}

	// map's result element is its own variable, as `list<T>.map<U>` declares.
	mapDef := &ir.IntrinsicDef{
		Name:       "list.map",
		TypeParams: []string{"T", "U"},
		Params: []*ir.Param{
			{Name: "l", Type: ir.ListOf(tvT)},
			{Name: "fn", Type: ir.FuncOf([]*ir.Param{{Name: "item", Type: tvT}}, tvU)},
		},
		Return: ir.ListOf(tvU),
	}
	_, ret = mapDef.Instantiate(ir.TypInt, ir.TypString)
	if got := ret.String(); got != "list<string>" {
		t.Errorf("list.map return = %s, want list<string>", got)
	}
}
