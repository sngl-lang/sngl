package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Decl on a collection is metadata, never identity: two list types with the
// same element type are the same type whether or not the stdlib had registered
// its declaration when each was built. Comparing it would have made a list
// built before the library loaded a different type from one built after.
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

// Substitute rebuilds a collection type, and dropping the declaration there
// would leave `list<T>` instantiated at int carrying less than the `list<T>`
// it came from.
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

// The generic collection intrinsics are written against type variables, and a
// caller binds them at the element type it has. Spelling them dyn made this a
// less-typed second record of the lib declaration, and put list<dyn> into the
// IR of every pass that synthesizes a call from one.
func TestGenericIntrinsicsInstantiate(t *testing.T) {
	// Registered the way lib/builtin/lists.sngl declares it. The registry holds
	// what a check registered, so this package's own tests state their inputs
	// rather than loading the library through the checker.
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

	// Unbound leaves the variable in place rather than degrading to dyn: a
	// caller that only wanted the arity gets the signature as declared.
	params, _ = def.Instantiate()
	if got := params[1].Type.String(); got != "T" {
		t.Errorf("uninstantiated param 1 = %s, want T", got)
	}

	// map's result element is its own variable, as `list<T>.map<U>` declares:
	// the receiver's parameter binds first, then the method's own.
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
