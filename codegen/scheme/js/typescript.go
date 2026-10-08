package js

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
	"github.com/sngl-lang/typescript-go/snglts"
)

// loadTypeScript parses a .ts/.d.ts/.js file and walks its top-level
// exports, mapping each to an ir.Func, ir.StructDef, ir.EnumDef, or ir.Var.
// abs is the resolved virtual absolute path (used by typescript-go's
// parser, which requires absolute filenames); rel is the matching
// io/fs.FS path used to read bytes; spec is the original js: spec.
func loadTypeScript(spec, abs, rel string, fsys fs.FS) (*ir.NativeImport, error) {
	src, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	kind := pickScriptKind(abs)
	opts := snglts.SourceFileParseOptions{
		FileName: abs,
		Path:     snglts.ToPath(abs, "", false),
	}
	sf := snglts.ParseSourceFile(opts, string(src), kind)
	if sf == nil {
		return nil, fmt.Errorf("parsing %s: nil SourceFile", abs)
	}

	w := &walker{
		sf:         sf,
		src:        string(src),
		filePath:   abs,
		importPath: spec,
		structs:    map[string]*ir.StructDef{},
		enums:      map[string]*ir.EnumDef{},
		aliases:    map[string]*ir.Type{},
	}
	w.walk()

	return &ir.NativeImport{
		ImportPath: spec,
		Structs:    w.outStructs,
		Enums:      w.outEnums,
		Funcs:      w.outFuncs,
		Vars:       w.outVars,
	}, nil
}

func pickScriptKind(path string) snglts.ScriptKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts":
		// .d.ts is matched here by suffix below.
		if strings.HasSuffix(strings.ToLower(path), ".d.ts") {
			return snglts.ScriptKindTS
		}
		return snglts.ScriptKindTS
	case ".tsx":
		return snglts.ScriptKindTSX
	case ".jsx":
		return snglts.ScriptKindJSX
	case ".mjs", ".cjs", ".js":
		return snglts.ScriptKindJS
	case ".json":
		return snglts.ScriptKindJSON
	}
	return snglts.ScriptKindTS
}

// walker collects the IR symbols produced from a single SourceFile.
// jsTypeID identifies a type by the module that declares it. A bare name is
// not enough: every module has its own namespace, so two modules exporting
// Config export two types.
type jsTypeID struct {
	Module string
	Name   string
}

type walker struct {
	sf         *snglts.SourceFile
	src        string
	filePath   string
	importPath string

	// Indices for cross-references during the second pass.
	structs map[string]*ir.StructDef
	enums   map[string]*ir.EnumDef
	aliases map[string]*ir.Type

	outStructs []*ir.StructDef
	outEnums   []*ir.EnumDef
	outFuncs   []*ir.Func
	outVars    []*ir.Var
}

func (w *walker) walk() {
	stmts := w.sf.Statements
	if stmts == nil {
		return
	}

	// Pass 1: register named types so type references can resolve.
	for _, s := range stmts.Nodes {
		if !isExported(s) {
			continue
		}
		switch s.Kind {
		case snglts.KindInterfaceDeclaration:
			name := identText(s.Name())
			if name == "" {
				continue
			}
			sd := &ir.StructDef{Name: name, Foreign: ir.Foreign{Name: name, Origin: jsTypeID{Module: w.importPath, Name: name}}, AST: w.synthStructAST(name, s)}
			w.structs[name] = sd
			w.outStructs = append(w.outStructs, sd)
		case snglts.KindTypeAliasDeclaration:
			name := identText(s.Name())
			if name == "" {
				continue
			}
			// If the alias targets an object literal, treat it as a struct.
			if t := s.Type(); t != nil && t.Kind == snglts.KindTypeLiteral {
				sd := &ir.StructDef{Name: name, Foreign: ir.Foreign{Name: name, Origin: jsTypeID{Module: w.importPath, Name: name}}, AST: w.synthStructAST(name, s)}
				w.structs[name] = sd
				w.outStructs = append(w.outStructs, sd)
			}
		case snglts.KindEnumDeclaration:
			name := identText(s.Name())
			if name == "" {
				continue
			}
			ed := &ir.EnumDef{Name: name, Foreign: ir.Foreign{Origin: jsTypeID{Module: w.importPath, Name: name}}, AST: w.synthEnumAST(name, s)}
			w.enums[name] = ed
			w.outEnums = append(w.outEnums, ed)
		}
	}

	// Pass 2: populate fields/members and emit funcs/vars.
	for _, s := range stmts.Nodes {
		if !isExported(s) {
			continue
		}
		switch s.Kind {
		case snglts.KindFunctionDeclaration:
			if fn := w.funcDeclToFunc(s); fn != nil {
				w.outFuncs = append(w.outFuncs, fn)
			}
		case snglts.KindInterfaceDeclaration:
			name := identText(s.Name())
			if sd, ok := w.structs[name]; ok {
				w.populateInterfaceFields(sd, s)
			}
		case snglts.KindTypeAliasDeclaration:
			name := identText(s.Name())
			if sd, ok := w.structs[name]; ok {
				if t := s.Type(); t != nil && t.Kind == snglts.KindTypeLiteral {
					w.populateTypeLiteralFields(sd, t)
				}
			}
		case snglts.KindEnumDeclaration:
			name := identText(s.Name())
			if ed, ok := w.enums[name]; ok {
				w.populateEnumMembers(ed, s)
			}
		case snglts.KindVariableStatement:
			w.outVars = append(w.outVars, w.varStmtToVars(s)...)
		}
	}
}

func (w *walker) funcDeclToFunc(s *snglts.Node) *ir.Func {
	name := identText(s.Name())
	if name == "" {
		return nil
	}
	f := &ir.Func{
		Name:    name,
		Foreign: ir.Foreign{Path: w.importPath, Name: name},
		Purity:  ir.PurityUnknown,
	}
	// The pure doc tag is the TypeScript spelling of `const func`.
	if isPureDoc(docComment(w.src, s.Pos())) {
		f.Purity = ir.PurityPure
		f.Const = true
	}
	if s.ModifierFlags()&snglts.ModifierFlagsAsync != 0 {
		f.IsAsync = true
	}
	for _, p := range s.Parameters() {
		paramName := identText(p.Name())
		if paramName == "" {
			paramName = fmt.Sprintf("arg%d", len(f.Params))
		}
		t, unusable := w.tsTypeToIR(p.Type())
		if unusable != "" && f.Foreign.Unusable == "" {
			f.Foreign.Unusable = fmt.Sprintf("parameter %q: %s", paramName, unusable)
		}
		f.Params = append(f.Params, &ir.Param{Name: paramName, Type: t})
	}
	if rt := s.Type(); rt != nil {
		t, unusable := w.tsTypeToIR(rt)
		if isPromiseTypeNode(rt) {
			f.IsAsync = true
			t, unusable = w.tsTypeToIR(promiseInnerNode(rt))
		}
		if unusable != "" && f.Foreign.Unusable == "" {
			f.Foreign.Unusable = "return type: " + unusable
		}
		if t != nil && t.Kind != ir.TypeVoid {
			f.Return = t
		}
	}
	return f
}

func (w *walker) populateInterfaceFields(sd *ir.StructDef, s *snglts.Node) {
	for _, m := range s.Members() {
		if m.Kind != snglts.KindPropertySignature {
			continue
		}
		w.appendField(sd, m)
	}
}

func (w *walker) populateTypeLiteralFields(sd *ir.StructDef, t *snglts.Node) {
	for _, m := range t.Members() {
		if m.Kind != snglts.KindPropertySignature {
			continue
		}
		w.appendField(sd, m)
	}
}

func (w *walker) appendField(sd *ir.StructDef, m *snglts.Node) {
	name := identText(m.Name())
	if name == "" {
		return
	}
	t, unusable := w.tsTypeToIR(m.Type())
	sf := &ir.StructField{
		Name:    lowerFirst(name),
		Type:    t,
		Foreign: ir.Foreign{Name: name},
	}
	if unusable != "" {
		sf.Foreign.Unusable = unusable
	}
	sd.Fields = append(sd.Fields, sf)
}

func (w *walker) populateEnumMembers(ed *ir.EnumDef, s *snglts.Node) {
	for _, m := range s.Members() {
		if m.Kind != snglts.KindEnumMember {
			continue
		}
		name := identText(m.Name())
		if name == "" {
			continue
		}
		ed.Members = append(ed.Members, &ir.EnumMember{Name: name, Value: w.enumMemberValue(m.Initializer())})
	}
}

// enumMemberValue records what a member is spelled as, which is what a member
// erases to at runtime: a folded call returns the value with nothing on it
// naming the member, so this is the only way back.
//
// An initializer that is not plainly a string or a number — a computed member,
// or an ambient one with no initializer at all — is left unrecorded rather
// than guessed at. TypeScript's implicit numbering is deliberately not
// reconstructed: it would put a value on a member the declaration does not
// give one, and a wrong guess resolves silently to the wrong member.
func (w *walker) enumMemberValue(init *snglts.Node) ir.Expr {
	if init == nil {
		return nil
	}
	raw := strings.TrimSpace(w.src[init.Pos():init.End()])
	if raw == "" {
		return nil
	}
	// snglts does not re-export the literal node kinds, so how the initializer
	// is spelled is what classifies it: a closing quote ends a string, and
	// anything else counts only if it reads as a number.
	if q := raw[len(raw)-1]; q == '"' || q == '\'' || q == '`' {
		return &ir.Literal{Type: ir.TypString, Value: init.Text()}
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return &ir.Literal{Type: ir.TypFloat, Value: raw}
	}
	// 0x/0o/0b forms are numbers TypeScript accepts and ParseFloat does not.
	if n, err := strconv.ParseInt(raw, 0, 64); err == nil {
		return &ir.Literal{Type: ir.TypFloat, Value: strconv.FormatInt(n, 10)}
	}
	return nil
}

func (w *walker) varStmtToVars(s *snglts.Node) []*ir.Var {
	var out []*ir.Var
	stmtList := s.StatementList()
	if stmtList == nil {
		// Variable statement wraps a VariableDeclarationList — find it.
		s.ForEachChild(func(n *snglts.Node) bool {
			if n.Kind == snglts.KindVariableDeclarationList {
				for _, decl := range n.Members() {
					if v := w.varDeclToVar(decl); v != nil {
						out = append(out, v)
					}
				}
				return true
			}
			return false
		})
	}
	return out
}

func (w *walker) varDeclToVar(decl *snglts.Node) *ir.Var {
	name := identText(decl.Name())
	if name == "" {
		return nil
	}
	t, unusable := w.tsTypeToIR(decl.Type())
	v := &ir.Var{
		Name:    name,
		Type:    t,
		IsConst: true,
		Foreign: ir.Foreign{Path: w.importPath, Name: name},
	}
	if unusable != "" {
		v.Foreign.Unusable = unusable
	}
	return v
}

// isExported returns true if a top-level declaration carries the `export`
// modifier, or if the SourceFile is a .d.ts that implicitly exposes
// declarations.
func isExported(n *snglts.Node) bool {
	return n.ModifierFlags()&snglts.ModifierFlagsExport != 0
}

// childTypes collects the type nodes that are immediate children of n.
// Used for union/intersection/tuple types where the typed-list lives in
// a kind-specific field; ForEachChild walks them polymorphically.
func childTypes(n *snglts.Node) []*snglts.Node {
	var out []*snglts.Node
	n.ForEachChild(func(c *snglts.Node) bool {
		out = append(out, c)
		return false
	})
	return out
}

// identText extracts the identifier text from a name node.
func identText(n *snglts.Node) string {
	if n == nil {
		return ""
	}
	return n.Text()
}

// lowerFirst lowers the first letter of an exported name (TS convention →
// SNGL convention). Mirrors codegen/scheme/golang/importer.go.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// synthStructAST builds a synthetic ast.StructDef carrying the source
// position of a TS declaration so the LSP can navigate to it.
func (w *walker) synthStructAST(name string, n *snglts.Node) *ast.StructDef {
	pos := w.posFromOffset(n.Pos())
	return &ast.StructDef{Pos: pos, Name: name}
}

func (w *walker) synthEnumAST(name string, n *snglts.Node) *ast.EnumDef {
	pos := w.posFromOffset(n.Pos())
	return &ast.EnumDef{Pos: pos, Name: name}
}

// posFromOffset converts a typescript-go byte offset into an SNGL ast.Pos
// (file + 1-based line + 1-based column). typescript-go reports 0-based
// line and 0-based UTF-16 character; SNGL is 1-based.
func (w *walker) posFromOffset(offset int) ast.Pos {
	line, col := snglts.GetECMALineAndUTF16CharacterOfPosition(w.sf, offset)
	return ast.Pos{File: w.filePath, Line: line + 1, Column: int(col) + 1}
}

// DeclaredHere reports whether a declaration's Foreign says its Name is a name
// generated JavaScript should spell: either this importer read the declaration
// from a JavaScript module, or a #[foreign] mark named this scheme. A mark sets
// no Origin — that is what keeps it out of type identity — so the scheme it
// wrote is the only thing left to ask.
func DeclaredHere(f ir.Foreign) bool {
	if _, ok := f.Origin.(jsTypeID); ok {
		return true
	}
	return f.Scheme == Scheme
}

// CallsHere reports whether a call to f should be routed through this scheme's
// module machinery — the bundler alias and the `import * as` it records.
//
// A declaration this importer read has a module path and nothing else to say;
// a marked one has only what the mark wrote, so it is routed here when the
// mark named this language and left alone when it named another. Without that
// second case a go: mark would rewrite a JavaScript call to a name no
// JavaScript declares.
func CallsHere(f ir.Foreign) bool {
	if f.Marked {
		return f.Scheme == Scheme
	}
	// A path is what an imported declaration has; #[js.native] may name a
	// global instead, which has none. The scheme is what says the name is
	// JavaScript's either way -- without this a global fell through to the
	// ordinary call path and was emitted under the SNGL declaration's own
	// name, which is right only when the two happen to be spelled the same.
	return f.Path != "" || f.Scheme == Scheme
}
