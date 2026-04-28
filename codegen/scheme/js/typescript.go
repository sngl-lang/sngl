package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/sngl-lang/typescript-go/snglts"
)

// loadTypeScript parses a .ts/.d.ts/.js file and walks its top-level
// exports, mapping each to an ir.Func, ir.StructDef, ir.EnumDef, or ir.Var.
func loadTypeScript(spec string, r *resolved) (*ir.NativeImport, error) {
	src, err := os.ReadFile(r.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", r.path, err)
	}
	kind := pickScriptKind(r.path)
	opts := snglts.SourceFileParseOptions{
		FileName: r.path,
		Path:     snglts.ToPath(r.path, "", false),
	}
	sf := snglts.ParseSourceFile(opts, string(src), kind)
	if sf == nil {
		return nil, fmt.Errorf("parsing %s: nil SourceFile", r.path)
	}

	w := &walker{
		sf:         sf,
		filePath:   r.path,
		importPath: r.importPath,
		structs:    map[string]*ir.StructDef{},
		enums:      map[string]*ir.EnumDef{},
		aliases:    map[string]*ir.Type{},
	}
	w.walk()

	return &ir.NativeImport{
		ImportPath: r.importPath,
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
type walker struct {
	sf         *snglts.SourceFile
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
			sd := &ir.StructDef{Name: name, Native: name, AST: w.synthStructAST(name, s)}
			w.structs[name] = sd
			w.outStructs = append(w.outStructs, sd)
		case snglts.KindTypeAliasDeclaration:
			name := identText(s.Name())
			if name == "" {
				continue
			}
			// If the alias targets an object literal, treat it as a struct.
			if t := s.Type(); t != nil && t.Kind == snglts.KindTypeLiteral {
				sd := &ir.StructDef{Name: name, Native: name, AST: w.synthStructAST(name, s)}
				w.structs[name] = sd
				w.outStructs = append(w.outStructs, sd)
			}
		case snglts.KindEnumDeclaration:
			name := identText(s.Name())
			if name == "" {
				continue
			}
			ed := &ir.EnumDef{Name: name, AST: w.synthEnumAST(name, s)}
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
		Name:       name,
		NativePkg:  w.importPath,
		NativeName: name,
		Purity:     ir.PurityUnknown,
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
		if unusable != "" && f.Unusable == "" {
			f.Unusable = fmt.Sprintf("parameter %q: %s", paramName, unusable)
		}
		f.Params = append(f.Params, &ir.Param{Name: paramName, Type: t})
	}
	if rt := s.Type(); rt != nil {
		t, unusable := w.tsTypeToIR(rt)
		if isPromiseTypeNode(rt) {
			f.IsAsync = true
			t, unusable = w.tsTypeToIR(promiseInnerNode(rt))
		}
		if unusable != "" && f.Unusable == "" {
			f.Unusable = "return type: " + unusable
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
		Name:       lowerFirst(name),
		Type:       t,
		NativeName: name,
	}
	if unusable != "" {
		sf.Unusable = unusable
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
		ed.Members = append(ed.Members, &ir.EnumMember{Name: name})
	}
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
		Name:       name,
		Type:       t,
		IsConst:    true,
		NativePkg:  w.importPath,
		NativeName: name,
	}
	if unusable != "" {
		v.Unusable = unusable
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
