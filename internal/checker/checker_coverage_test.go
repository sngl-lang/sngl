package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestScope_All(t *testing.T) {
	parent := NewScope(nil)
	parent.Declare("x", Int)
	parent.Declare("y", String)

	child := NewScope(parent)
	child.Declare("y", Float) // override
	child.Declare("z", Bool)

	all := child.All()
	if all["x"] != Int {
		t.Errorf("expected x=Int, got %v", all["x"])
	}
	if all["y"] != Float {
		t.Errorf("expected y=Float (child override), got %v", all["y"])
	}
	if all["z"] != Bool {
		t.Errorf("expected z=Bool, got %v", all["z"])
	}
}

func TestScope_LookupParent(t *testing.T) {
	parent := NewScope(nil)
	parent.Declare("x", Int)
	child := NewScope(parent)

	typ, ok := child.Lookup("x")
	if !ok || typ != Int {
		t.Errorf("expected Int from parent, got %v, %v", typ, ok)
	}
}

func TestScope_LookupNotFound(t *testing.T) {
	s := NewScope(nil)
	_, ok := s.Lookup("missing")
	if ok {
		t.Error("expected Lookup to return false for missing var")
	}
}

func TestType_GoString(t *testing.T) {
	if got := Int.GoString(); got != "int" {
		t.Errorf("GoString() = %q, want 'int'", got)
	}
}

func TestType_String_Unknown(t *testing.T) {
	unknown := Type(999)
	if got := unknown.String(); got != "unknown" {
		t.Errorf("unknown type String() = %q, want 'unknown'", got)
	}
}

func TestNarrowNumeric(t *testing.T) {
	tests := []struct {
		left, right, want Type
	}{
		{Int, Dyn, Int},
		{Dyn, Float, Float},
		{Float, Int, Float},
		{Dyn, Dyn, Dyn},
		{Int, Float, Int},
	}
	for _, tc := range tests {
		got := narrowNumeric(tc.left, tc.right)
		if got != tc.want {
			t.Errorf("narrowNumeric(%v, %v) = %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestIsAllAlpha(t *testing.T) {
	if !isAllAlpha("US") {
		t.Error("expected 'US' to be all alpha")
	}
	if isAllAlpha("U2") {
		t.Error("expected 'U2' to not be all alpha")
	}
	if !isAllAlpha("abc") {
		t.Error("expected 'abc' to be all alpha")
	}
}

func TestInferLiteralType_List(t *testing.T) {
	if got := InferLiteralType([]any{1, 2}); got != List {
		t.Errorf("expected List, got %v", got)
	}
}

func TestInferLiteralType_Int(t *testing.T) {
	if got := InferLiteralType(42); got != Int {
		t.Errorf("expected Int, got %v", got)
	}
}

func TestInferLiteralType_String(t *testing.T) {
	if got := InferLiteralType("hello"); got != String {
		t.Errorf("expected String, got %v", got)
	}
}

func TestCheckStmtRefs_AssignUnknown(t *testing.T) {
	known := map[string]bool{"x": true}
	stmt := &ast.AssignStmt{
		Target: &ast.IdentExpr{Name: "y"},
		Op:     ast.AssignSet,
		Value:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
	}
	diags := checkStmtRefs(stmt, known)
	if len(diags) == 0 {
		t.Error("expected diagnostic for unknown variable")
	}
}

func TestCheckStmtRefs_AssignKnown(t *testing.T) {
	known := map[string]bool{"x": true}
	stmt := &ast.AssignStmt{
		Target: &ast.IdentExpr{Name: "x"},
		Op:     ast.AssignSet,
		Value:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
	}
	diags := checkStmtRefs(stmt, known)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostic, got %v", diags)
	}
}

func TestCheckStmtRefs_CallExprUnknown(t *testing.T) {
	known := map[string]bool{}
	stmt := &ast.CallExpr{Func: "bogus"}
	diags := checkStmtRefs(stmt, known)
	if len(diags) == 0 {
		t.Error("expected diagnostic for unknown function")
	}
}

func TestCheckStmtRefs_CallStmtUnknown(t *testing.T) {
	known := map[string]bool{}
	stmt := &ast.CallStmt{Call: &ast.CallExpr{Func: "bogus"}}
	diags := checkStmtRefs(stmt, known)
	if len(diags) == 0 {
		t.Error("expected diagnostic for unknown function in CallStmt")
	}
}

func TestCheckStmtRefs_Default(t *testing.T) {
	known := map[string]bool{}
	stmt := &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}
	diags := checkStmtRefs(stmt, known)
	if len(diags) != 0 {
		t.Errorf("expected no diagnostic for default case, got %v", diags)
	}
}

func TestIsConstantNode(t *testing.T) {
	c := &checker{
		constNames: map[string]bool{"MAX": true},
	}

	// Literal
	if !c.isConstantNode(&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}) {
		t.Error("expected literal to be constant")
	}

	// Const ident
	if !c.isConstantNode(&ast.IdentExpr{Name: "MAX"}) {
		t.Error("expected const ident to be constant")
	}

	// Non-const ident
	if c.isConstantNode(&ast.IdentExpr{Name: "x"}) {
		t.Error("expected non-const ident to not be constant")
	}

	// Method on constant receiver
	if !c.isConstantNode(&ast.MethodExpr{
		Receiver: &ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString},
		Method:   "length",
	}) {
		t.Error("expected method on literal to be constant")
	}

	// Method with non-constant arg
	if c.isConstantNode(&ast.MethodExpr{
		Receiver: &ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString},
		Method:   "contains",
		Args:     []ast.Node{&ast.IdentExpr{Name: "x"}},
	}) {
		t.Error("expected method with non-const arg to not be constant")
	}

	// Method with non-constant receiver
	if c.isConstantNode(&ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "x"},
		Method:   "length",
	}) {
		t.Error("expected method with non-const receiver to not be constant")
	}

	// Default (unknown node type)
	if c.isConstantNode(&ast.SelectExpr{Operand: &ast.IdentExpr{Name: "x"}, Field: "y"}) {
		t.Error("expected unknown node to not be constant")
	}
}

func TestResolveTypeHint_InlineEnum(t *testing.T) {
	c := &checker{
		enums:      nil,
		structs:    nil,
		namespaces: map[string]*importNS{},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "enum:red|green|blue")
	if typ != String {
		t.Errorf("expected String for inline enum, got %v", typ)
	}
}

func TestResolveTypeHint_NamedEnum(t *testing.T) {
	c := &checker{
		enums:      []*ast.EnumDef{{Name: "Color"}},
		structs:    nil,
		namespaces: map[string]*importNS{},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "Color")
	if typ != String {
		t.Errorf("expected String for named enum, got %v", typ)
	}
}

func TestResolveTypeHint_NamedStruct(t *testing.T) {
	c := &checker{
		enums:      nil,
		structs:    []*ast.StructDef{{Name: "Point"}},
		namespaces: map[string]*importNS{},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "Point")
	if typ != Struct {
		t.Errorf("expected Struct for named struct, got %v", typ)
	}
}

func TestResolveTypeHint_QualifiedEnum(t *testing.T) {
	c := &checker{
		namespaces: map[string]*importNS{
			"ui": {enums: []*ast.EnumDef{{Name: "Size"}}},
		},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "ui.Size")
	if typ != String {
		t.Errorf("expected String for qualified enum, got %v", typ)
	}
}

func TestResolveTypeHint_QualifiedStruct(t *testing.T) {
	c := &checker{
		namespaces: map[string]*importNS{
			"ui": {structs: []*ast.StructDef{{Name: "Point"}}},
		},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "ui.Point")
	if typ != Struct {
		t.Errorf("expected Struct for qualified struct, got %v", typ)
	}
}

func TestResolveTypeHint_QualifiedUnknown(t *testing.T) {
	c := &checker{
		namespaces: map[string]*importNS{
			"ui": {},
		},
	}
	// Unknown type in known namespace — should error and return Dyn
	typ := c.resolveTypeHint(ast.Pos{}, "ui.Bogus")
	if typ != Dyn {
		t.Errorf("expected Dyn for unknown qualified type, got %v", typ)
	}
}

func TestResolveTypeHint_UnknownNS(t *testing.T) {
	c := &checker{
		namespaces: map[string]*importNS{},
	}
	typ := c.resolveTypeHint(ast.Pos{}, "bogus.Type")
	if typ != Dyn {
		t.Errorf("expected Dyn for unknown namespace, got %v", typ)
	}
}

func TestResolveExprType(t *testing.T) {
	c := &checker{
		scope:      NewScope(nil),
		enums:      nil,
		structs:    nil,
		namespaces: map[string]*importNS{},
	}

	// SNGL path
	expr := &ast.Expr{SNGL: &ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}
	if got := c.resolveExprType(ast.Pos{}, expr); got != Int {
		t.Errorf("expected Int for SNGL literal, got %v", got)
	}

	// TypeHint path
	expr2 := &ast.Expr{TypeHint: "bool"}
	if got := c.resolveExprType(ast.Pos{}, expr2); got != Bool {
		t.Errorf("expected Bool for TypeHint, got %v", got)
	}

	// Literal path
	expr3 := &ast.Expr{Literal: "hello"}
	if got := c.resolveExprType(ast.Pos{}, expr3); got != String {
		t.Errorf("expected String for Literal, got %v", got)
	}

	// Empty expr
	expr4 := &ast.Expr{}
	if got := c.resolveExprType(ast.Pos{}, expr4); got != Dyn {
		t.Errorf("expected Dyn for empty expr, got %v", got)
	}
}

func TestResolveExprInScope(t *testing.T) {
	c := &checker{
		scope:      NewScope(nil),
		enums:      nil,
		structs:    nil,
		namespaces: map[string]*importNS{},
	}
	scope := NewScope(nil)
	scope.Declare("x", Int)

	// SNGL path
	expr := &ast.Expr{SNGL: &ast.IdentExpr{Name: "x"}}
	if got := c.resolveExprInScope(ast.Pos{}, expr, scope); got != Int {
		t.Errorf("expected Int for x in scope, got %v", got)
	}

	// TypeHint path
	expr2 := &ast.Expr{TypeHint: "float"}
	if got := c.resolveExprInScope(ast.Pos{}, expr2, scope); got != Float {
		t.Errorf("expected Float, got %v", got)
	}

	// Literal path
	expr3 := &ast.Expr{Literal: true}
	if got := c.resolveExprInScope(ast.Pos{}, expr3, scope); got != Bool {
		t.Errorf("expected Bool, got %v", got)
	}

	// Empty
	expr4 := &ast.Expr{}
	if got := c.resolveExprInScope(ast.Pos{}, expr4, scope); got != Dyn {
		t.Errorf("expected Dyn, got %v", got)
	}
}

func TestInferNodeTypeInScope(t *testing.T) {
	mainScope := NewScope(nil)
	mainScope.Declare("x", Int)
	mainScope.Declare("myFunc", String)

	c := &checker{
		scope:   mainScope,
		methods: map[string]map[string]*methodInfo{},
		namespaces: map[string]*importNS{
			"api": {
				data: []*ast.Data{
					{Name: "fetch", IsFunc: true, ReturnType: "string", Init: ast.Expr{}},
					{Name: "baseURL", Init: ast.Expr{TypeHint: "string"}},
				},
			},
		},
	}
	scope := mainScope

	// Binary arithmetic => narrowNumeric
	got := c.inferNodeTypeInScope(&ast.BinaryExpr{
		Op:    ast.BinAdd,
		Left:  &ast.IdentExpr{Name: "x"},
		Right: &ast.IdentExpr{Name: "x"},
	}, scope)
	if got != Int {
		t.Errorf("expected Int for x+x, got %v", got)
	}

	// String concat
	got = c.inferNodeTypeInScope(&ast.BinaryExpr{
		Op:    ast.BinAdd,
		Left:  &ast.LiteralExpr{Value: "a", Kind: ast.LiteralString},
		Right: &ast.LiteralExpr{Value: "b", Kind: ast.LiteralString},
	}, scope)
	if got != String {
		t.Errorf("expected String for string+string, got %v", got)
	}

	// Float promotion
	got = c.inferNodeTypeInScope(&ast.BinaryExpr{
		Op:    ast.BinAdd,
		Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
		Right: &ast.LiteralExpr{Value: 2.0, Kind: ast.LiteralFloat},
	}, scope)
	if got != Float {
		t.Errorf("expected Float for int+float, got %v", got)
	}

	// CallExpr default => scope lookup
	got = c.inferNodeTypeInScope(&ast.CallExpr{
		Func: "myFunc",
		Args: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
	}, scope)
	if got != String {
		t.Errorf("expected String for myFunc(), got %v", got)
	}

	// MethodExpr => namespace imported func
	got = c.inferNodeTypeInScope(&ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "api"},
		Method:   "fetch",
	}, scope)
	if got != String {
		t.Errorf("expected String for api.fetch(), got %v", got)
	}

	// SelectExpr => namespace imported var
	got = c.inferNodeTypeInScope(&ast.SelectExpr{
		Operand: &ast.IdentExpr{Name: "api"},
		Field:   "baseURL",
	}, scope)
	if got != String {
		t.Errorf("expected String for api.baseURL, got %v", got)
	}

	// IndexExpr => Dyn
	got = c.inferNodeTypeInScope(&ast.IndexExpr{
		Operand: &ast.IdentExpr{Name: "x"},
		Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
	}, scope)
	if got != Dyn {
		t.Errorf("expected Dyn for IndexExpr, got %v", got)
	}

	// ListExpr => List
	got = c.inferNodeTypeInScope(&ast.ListExpr{}, scope)
	if got != List {
		t.Errorf("expected List, got %v", got)
	}

	// StructExpr => Struct
	got = c.inferNodeTypeInScope(&ast.StructExpr{Name: "Point"}, scope)
	if got != Struct {
		t.Errorf("expected Struct, got %v", got)
	}

	// InterpolationExpr => String
	got = c.inferNodeTypeInScope(&ast.InterpolationExpr{}, scope)
	if got != String {
		t.Errorf("expected String for interpolation, got %v", got)
	}

	// ElementRefExpr => Dyn
	got = c.inferNodeTypeInScope(&ast.ElementRefExpr{Name: "btn"}, scope)
	if got != Dyn {
		t.Errorf("expected Dyn for element ref, got %v", got)
	}
}

func TestIsConstantExpr(t *testing.T) {
	c := &checker{
		constNames: map[string]bool{"MAX": true},
	}

	// Literal value
	if !c.isConstantExpr(&ast.Expr{Literal: 42}) {
		t.Error("expected literal to be constant")
	}

	// SNGL const
	if !c.isConstantExpr(&ast.Expr{SNGL: &ast.IdentExpr{Name: "MAX"}}) {
		t.Error("expected const ident to be constant")
	}

	// SNGL non-const
	if c.isConstantExpr(&ast.Expr{SNGL: &ast.IdentExpr{Name: "x"}}) {
		t.Error("expected non-const to not be constant")
	}

	// Empty
	if c.isConstantExpr(&ast.Expr{}) {
		t.Error("expected empty expr to not be constant")
	}
}

func TestValidateSpecialLiteral_Country2Valid(t *testing.T) {
	if err := validateSpecialLiteral("country2", "US"); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

func TestValidateSpecialLiteral_Country2Invalid(t *testing.T) {
	if err := validateSpecialLiteral("country2", "USA"); err == nil {
		t.Error("expected error for 3-letter country2")
	}
}

func TestValidateSpecialLiteral_Country3Valid(t *testing.T) {
	if err := validateSpecialLiteral("country3", "USA"); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

func TestValidateSpecialLiteral_Country3Invalid(t *testing.T) {
	if err := validateSpecialLiteral("country3", "US"); err == nil {
		t.Error("expected error for 2-letter country3")
	}
}

func TestValidateSpecialLiteral_NonStringValue(t *testing.T) {
	if err := validateSpecialLiteral("color", 42); err == nil {
		t.Error("expected error for non-string value")
	}
}

func TestIsAssignable_SpecialToString(t *testing.T) {
	if !isAssignable(Color, String) {
		t.Error("expected Color assignable to String")
	}
	if !isAssignable(String, Color) {
		t.Error("expected String assignable to Color")
	}
}

func TestIsAssignable_IncompatibleTypes(t *testing.T) {
	if isAssignable(Int, Bool) {
		t.Error("expected Int not assignable to Bool")
	}
}

func TestTypeFromHint_ListPrefix(t *testing.T) {
	if got := TypeFromHint("[]int"); got != List {
		t.Errorf("expected List, got %v", got)
	}
	if got := TypeFromHint("list:string"); got != List {
		t.Errorf("expected List, got %v", got)
	}
}

func TestInferNodeType(t *testing.T) {
	scope := NewScope(nil)
	scope.Declare("count", Int)
	scope.Declare("name", String)

	c := &checker{
		scope:   scope,
		methods: map[string]map[string]*methodInfo{},
		namespaces: map[string]*importNS{
			"api": {
				data: []*ast.Data{
					{Name: "fetch", IsFunc: true, ReturnType: "string", Init: ast.Expr{}},
					{Name: "baseURL", Init: ast.Expr{TypeHint: "string"}},
				},
			},
		},
	}

	tests := []struct {
		name string
		node ast.Node
		want Type
	}{
		{"literal bool", &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, Bool},
		{"literal int", &ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}, Int},
		{"literal float", &ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}, Float},
		{"literal string", &ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString}, String},
		{"literal null", &ast.LiteralExpr{Kind: ast.LiteralNull}, Dyn},
		{"literal color", &ast.LiteralExpr{Value: "#ff0000", Kind: ast.LiteralColor}, Color},
		{"literal unit", &ast.LiteralExpr{Value: ast.UnitLiteral{Number: "5", Suffix: "s"}, Kind: ast.LiteralUnit}, Unit},
		{"ident known", &ast.IdentExpr{Name: "count"}, Int},
		{"ident PLATFORM", &ast.IdentExpr{Name: "PLATFORM"}, String},
		{"ident LANGUAGE", &ast.IdentExpr{Name: "LANGUAGE"}, String},
		{"ident unknown", &ast.IdentExpr{Name: "unknown"}, Dyn},
		{"binary eq", &ast.BinaryExpr{Op: ast.BinEq, Left: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, Right: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}, Bool},
		{"binary add int", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}}, Int},
		{"binary add float", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.LiteralExpr{Value: 1.0, Kind: ast.LiteralFloat}, Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}}, Float},
		{"binary add string", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.LiteralExpr{Value: "a", Kind: ast.LiteralString}, Right: &ast.LiteralExpr{Value: "b", Kind: ast.LiteralString}}, String},
		{"unary not", &ast.UnaryExpr{Op: ast.UnaryNot, Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}}, Bool},
		{"unary neg", &ast.UnaryExpr{Op: ast.UnaryNeg, Operand: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}, Int},
		{"ternary", &ast.TernaryExpr{Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, Then: &ast.LiteralExpr{Value: "a", Kind: ast.LiteralString}, Else: &ast.LiteralExpr{Value: "b", Kind: ast.LiteralString}}, String},
		{"call string", &ast.CallExpr{Func: "string", Args: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}}, String},
		{"call int", &ast.CallExpr{Func: "int", Args: []ast.Node{&ast.LiteralExpr{Value: 1.0, Kind: ast.LiteralFloat}}}, Int},
		{"call float", &ast.CallExpr{Func: "float", Args: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}}, Float},
		{"call scope fn", &ast.CallExpr{Func: "name", Args: nil}, String},
		{"call unknown", &ast.CallExpr{Func: "bogus", Args: nil}, Dyn},
		{"method ns imported", &ast.MethodExpr{Receiver: &ast.IdentExpr{Name: "api"}, Method: "fetch"}, String},
		{"select ns imported", &ast.SelectExpr{Operand: &ast.IdentExpr{Name: "api"}, Field: "baseURL"}, String},
		{"index", &ast.IndexExpr{Operand: &ast.IdentExpr{Name: "count"}, Index: &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt}}, Dyn},
		{"list", &ast.ListExpr{}, List},
		{"struct expr", &ast.StructExpr{Name: "Point"}, Struct},
		{"interpolation", &ast.InterpolationExpr{}, String},
		{"element ref", &ast.ElementRefExpr{Name: "btn"}, Dyn},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.inferNodeType(tc.node)
			if got != tc.want {
				t.Errorf("inferNodeType(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsStatement(t *testing.T) {
	tests := []struct {
		name string
		node ast.Node
		want bool
	}{
		{"assign", &ast.AssignStmt{Target: &ast.IdentExpr{Name: "x"}, Op: ast.AssignSet, Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}, true},
		{"toggle", &ast.ToggleStmt{Target: &ast.IdentExpr{Name: "x"}}, true},
		{"emit", &ast.EmitStmt{Name: "click"}, true},
		{"call stmt", &ast.CallStmt{Call: &ast.CallExpr{Func: "f"}}, true},
		{"method expr", &ast.MethodExpr{Receiver: &ast.IdentExpr{Name: "api"}, Method: "save"}, true},
		{"stmt block", &ast.StmtBlock{Stmts: []ast.Node{&ast.AssignStmt{Target: &ast.IdentExpr{Name: "x"}, Op: ast.AssignSet, Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}}}, true},
		{"empty block", &ast.StmtBlock{Stmts: nil}, false},
		{"literal", &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, false},
		{"block with non-stmt", &ast.StmtBlock{Stmts: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isStatement(tc.node)
			if got != tc.want {
				t.Errorf("isStatement(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestFindComponent(t *testing.T) {
	c := &checker{
		components: []*ast.Component{{Name: "Counter"}, {Name: "Button"}},
		namespaces: map[string]*importNS{
			"ui": {components: []*ast.Component{{Name: "Card"}}},
		},
	}

	// local
	if comp := c.findComponent("Counter"); comp == nil || comp.Name != "Counter" {
		t.Error("expected to find Counter")
	}

	// namespaced
	if comp := c.findComponent("ui.Card"); comp == nil || comp.Name != "Card" {
		t.Error("expected to find ui.Card")
	}

	// not found
	if comp := c.findComponent("Bogus"); comp != nil {
		t.Error("expected nil for unknown component")
	}

	// unknown namespace
	if comp := c.findComponent("bogus.Thing"); comp != nil {
		t.Error("expected nil for unknown namespace")
	}
}

func TestInferNodeTypeInScope_MethodLookup(t *testing.T) {
	c := &checker{
		scope: NewScope(nil),
		methods: map[string]map[string]*methodInfo{
			"string": {"length": {ReturnType: "int"}},
		},
		namespaces: map[string]*importNS{},
	}
	scope := NewScope(nil)
	scope.Declare("name", String)

	// Instance method: name.length()
	got := c.inferNodeTypeInScope(&ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "name"},
		Method:   "length",
	}, scope)
	if got != Int {
		t.Errorf("expected Int for name.length(), got %v", got)
	}

	// Type-namespace method: string.length("hi")
	got = c.inferNodeTypeInScope(&ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "string"},
		Method:   "length",
		Args:     []ast.Node{&ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString}},
	}, scope)
	if got != Int {
		t.Errorf("expected Int for string.length('hi'), got %v", got)
	}
}

func TestInferNodeTypeInScope_Dyn(t *testing.T) {
	c := &checker{
		scope:      NewScope(nil),
		methods:    map[string]map[string]*methodInfo{},
		namespaces: map[string]*importNS{},
	}
	scope := NewScope(nil)

	// Dyn + Dyn => narrowNumeric returns Dyn
	got := c.inferNodeTypeInScope(&ast.BinaryExpr{
		Op:    ast.BinAdd,
		Left:  &ast.IdentExpr{Name: "a"},
		Right: &ast.IdentExpr{Name: "b"},
	}, scope)
	if got != Dyn {
		t.Errorf("expected Dyn for dyn+dyn, got %v", got)
	}
}

func TestValidateConstExpr(t *testing.T) {
	c := &checker{errs: nil}
	constNames := map[string]bool{"MAX": true}

	// Valid: literal
	c.validateConstExpr(ast.Pos{}, &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for literal, got %v", c.errs)
	}

	// Valid: const ident
	c.validateConstExpr(ast.Pos{}, &ast.IdentExpr{Name: "MAX"}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for const ident, got %v", c.errs)
	}

	// Invalid: non-const ident
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.IdentExpr{Name: "x"}, constNames)
	if len(c.errs) == 0 {
		t.Error("expected error for non-const ident")
	}

	// Valid: binary
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.BinaryExpr{
		Op:    ast.BinAdd,
		Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
		Right: &ast.IdentExpr{Name: "MAX"},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for binary const, got %v", c.errs)
	}

	// Valid: unary
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.UnaryExpr{
		Op:      ast.UnaryNeg,
		Operand: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for unary const, got %v", c.errs)
	}

	// Valid: ternary
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.TernaryExpr{
		Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
		Then: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
		Else: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for ternary const, got %v", c.errs)
	}

	// Valid: call
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.CallExpr{
		Func: "int",
		Args: []ast.Node{&ast.LiteralExpr{Value: 1.0, Kind: ast.LiteralFloat}},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for call const, got %v", c.errs)
	}

	// Valid: method
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.MethodExpr{
		Receiver: &ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString},
		Method:   "length",
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for method const, got %v", c.errs)
	}

	// Valid: interpolation
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.InterpolationExpr{
		Parts: []ast.Node{&ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString}},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for interpolation const, got %v", c.errs)
	}

	// Valid: list
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.ListExpr{
		Elements: []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for list const, got %v", c.errs)
	}

	// Valid: struct expr
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.StructExpr{
		Name:   "Point",
		Fields: []ast.StructFieldLit{{Name: "x", Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}}},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for struct const, got %v", c.errs)
	}

	// Valid: select expr
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.SelectExpr{
		Operand: &ast.IdentExpr{Name: "MAX"},
		Field:   "x",
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for select const, got %v", c.errs)
	}

	// Valid: index expr
	c.errs = nil
	c.validateConstExpr(ast.Pos{}, &ast.IndexExpr{
		Operand: &ast.IdentExpr{Name: "MAX"},
		Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
	}, constNames)
	if len(c.errs) != 0 {
		t.Errorf("expected no errors for index const, got %v", c.errs)
	}
}

func TestInferNodeTypeInScope_AllCases(t *testing.T) {
	mainScope := NewScope(nil)
	mainScope.Declare("x", Int)
	mainScope.Declare("s", String)

	c := &checker{
		scope:   mainScope,
		methods: map[string]map[string]*methodInfo{},
		namespaces: map[string]*importNS{
			"ns": {
				data: []*ast.Data{
					{Name: "fn", IsFunc: true, ReturnType: "int", Init: ast.Expr{}},
					{Name: "val", Init: ast.Expr{TypeHint: "float"}},
				},
			},
		},
	}
	scope := mainScope

	tests := []struct {
		name string
		node ast.Node
		want Type
	}{
		{"literal_bool", &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, Bool},
		{"literal_int", &ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}, Int},
		{"literal_float", &ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}, Float},
		{"literal_string", &ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString}, String},
		{"literal_null", &ast.LiteralExpr{Kind: ast.LiteralNull}, Dyn},
		{"literal_color", &ast.LiteralExpr{Value: "#ff0000", Kind: ast.LiteralColor}, Color},
		{"literal_unit", &ast.LiteralExpr{Value: ast.UnitLiteral{Number: "5", Suffix: "s"}, Kind: ast.LiteralUnit}, Unit},
		{"ident_x", &ast.IdentExpr{Name: "x"}, Int},
		{"ident_PLATFORM", &ast.IdentExpr{Name: "PLATFORM"}, String},
		{"binary_and", &ast.BinaryExpr{Op: ast.BinAnd, Left: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, Right: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}}, Bool},
		{"binary_add_int", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.IdentExpr{Name: "x"}, Right: &ast.IdentExpr{Name: "x"}}, Int},
		{"binary_add_str", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.IdentExpr{Name: "s"}, Right: &ast.IdentExpr{Name: "s"}}, String},
		{"binary_add_float", &ast.BinaryExpr{Op: ast.BinAdd, Left: &ast.LiteralExpr{Value: 1.0, Kind: ast.LiteralFloat}, Right: &ast.IdentExpr{Name: "x"}}, Float},
		{"unary_not", &ast.UnaryExpr{Op: ast.UnaryNot, Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}}, Bool},
		{"unary_neg", &ast.UnaryExpr{Op: ast.UnaryNeg, Operand: &ast.IdentExpr{Name: "x"}}, Int},
		{"ternary", &ast.TernaryExpr{Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, Then: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, Else: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}}, Int},
		{"call_string", &ast.CallExpr{Func: "string", Args: nil}, String},
		{"call_int", &ast.CallExpr{Func: "int", Args: nil}, Int},
		{"call_float", &ast.CallExpr{Func: "float", Args: nil}, Float},
		{"call_scope", &ast.CallExpr{Func: "x", Args: nil}, Int},
		{"call_unknown", &ast.CallExpr{Func: "bogus", Args: nil}, Dyn},
		{"ns_method", &ast.MethodExpr{Receiver: &ast.IdentExpr{Name: "ns"}, Method: "fn"}, Int},
		{"ns_select", &ast.SelectExpr{Operand: &ast.IdentExpr{Name: "ns"}, Field: "val"}, Float},
		{"index", &ast.IndexExpr{Operand: &ast.IdentExpr{Name: "x"}, Index: &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt}}, Dyn},
		{"list", &ast.ListExpr{}, List},
		{"struct_expr", &ast.StructExpr{Name: "P"}, Struct},
		{"interpolation", &ast.InterpolationExpr{}, String},
		{"element_ref", &ast.ElementRefExpr{Name: "btn"}, Dyn},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := c.inferNodeTypeInScope(tc.node, scope)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckEmptyDoc(t *testing.T) {
	// Empty doc with isMain=true and no app/tests => error
	doc := &ast.Document{}
	err := Check(doc, nil, "", nil, nil, nil, nil, true)
	if err == nil {
		t.Error("expected error for empty doc with isMain=true")
	}
}

func TestCheckEmptyDocLibrary(t *testing.T) {
	// Empty doc with isMain=false => no error
	doc := &ast.Document{}
	err := Check(doc, nil, "", nil, nil, nil, nil, false)
	if err != nil {
		t.Errorf("expected no error for library doc, got %v", err)
	}
}

func TestCheckDiagnosticsEmpty(t *testing.T) {
	doc := &ast.Document{}
	_, diags := CheckDiagnostics(doc, nil, "", nil, nil, nil)
	// Just verify it runs without panic
	_ = diags
}

func TestIsKnownDynHint(t *testing.T) {
	if !isKnownDynHint("dyn") {
		t.Error("expected 'dyn' to be known dyn hint")
	}
	if !isKnownDynHint("func(int) -> string") {
		t.Error("expected func hint to be known dyn hint")
	}
	if isKnownDynHint("unit:px") {
		t.Error("expected unit hint to NOT be known dyn hint")
	}
	if isKnownDynHint("int") {
		t.Error("expected 'int' to not be known dyn hint")
	}
}
