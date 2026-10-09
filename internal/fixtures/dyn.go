package fixtures

import (
	"bytes"
	"regexp"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

var (
	dynKeyword = regexp.MustCompile(`\bdyn\b`)
	// Calls through these stdlib receivers produce TypDyn today because the
	// stdlib's `=>` funcs omit explicit return annotations (see
	// registerStdlibFunc). Fixtures that use these methods inherit inferred
	// dyn through no fault of the user-code checker paths, so the backstop
	// skips them. Tightening this list should come together with adding
	// return-type annotations to the stdlib.
	stdlibDynMethod = regexp.MustCompile(`\b(?:int|float|string|list|option|color|Alert|File|stdlib|alert|file)\.[a-zA-Z]|\.(?:length|upper|lower|trim|contains|startsWith|endsWith|indexOf|substring|replace|split|join|push|pop|slice|parse|hex|rgb|rgba|opacity|lighten|darken|abs|min|max|clamp|floor|ceil|round|sqrt|pow|sin|cos|tan|asin|acos|atan|atan2)\(`)
)

// assertNoSilentDyn guards the "dyn only when explicit" invariant: a fixture
// whose source never mentions `dyn` must not check to IR carrying TypeDyn.
//
// The package is the one the runner already checked. This used to glob and
// check testdata a third time with a Config naming no targets; sharing the
// runner's package means the targets are registered, which can only resolve
// more names, never fewer.
func assertNoSilentDyn(t *testing.T, s testutil.Sample, pkg *ir.Package) {
	if pkg == nil {
		return
	}
	// Strip comments before scanning so a directive mentioning `dyn` in its
	// expected message does not count as explicit use.
	stripped := stripLineComments([]byte(s.Source))
	if dynKeyword.Match(stripped) || stdlibDynMethod.Match(stripped) {
		return
	}
	if s.ExpectsError("parse") || s.ExpectsError("check") {
		return // TypDyn during error recovery is expected
	}
	report := func(where string, pos ast.Pos) {
		t.Errorf("inferred TypeDyn at %s (%s:%d:%d) — expected explicit annotation or an error",
			where, s.Filename, pos.Line, pos.Column)
	}
	visit := func(label string, pos ast.Pos, typ *ir.Type) {
		if typ != nil && typ.Kind == ir.TypeDyn {
			report(label, pos)
		}
	}
	for _, v := range pkg.Vars {
		visit("package var "+v.Name, stmtPos(v.AST), v.Type)
	}
	for _, v := range pkg.Consts {
		visit("package const "+v.Name, stmtPos(v.AST), v.Type)
	}
	for _, fn := range pkg.Funcs {
		pos := ast.Pos{}
		if fn.AST != nil {
			pos = fn.AST.Pos
		}
		for _, p := range fn.Params {
			visit("func "+fn.Name+" param "+p.Name, pos, p.Type)
		}
		visit("func "+fn.Name+" return", pos, fn.Return)
	}
	for _, comp := range pkg.Components {
		pos := ast.Pos{}
		if comp.AST != nil {
			pos = comp.AST.Pos
		}
		for _, p := range comp.Props {
			visit("component "+comp.Name+" prop "+p.Name, pos, p.Type)
		}
		for _, v := range comp.Vars {
			vpos := pos
			if p := stmtPos(v.AST); p.Line != 0 {
				vpos = p
			}
			visit("component "+comp.Name+" var "+v.Name, vpos, v.Type)
		}
		for _, fn := range comp.Funcs {
			for _, p := range fn.Params {
				visit("component "+comp.Name+" func "+fn.Name+" param "+p.Name, pos, p.Type)
			}
			visit("component "+comp.Name+" func "+fn.Name+" return", pos, fn.Return)
		}
	}
	for _, sd := range pkg.Structs {
		pos := ast.Pos{}
		if sd.AST != nil {
			pos = sd.AST.Pos
		}
		for _, f := range sd.Fields {
			visit("struct "+sd.Name+" field "+f.Name, pos, f.Type)
		}
	}
}

// stmtPos extracts the Pos from an ast.Stmt that wraps a decl with a Pos field.
func stmtPos(s ast.Stmt) ast.Pos {
	switch x := s.(type) {
	case *ast.VarDecl:
		return x.Pos
	case *ast.ConstDecl:
		return x.Pos
	}
	return ast.Pos{}
}

// stripLineComments removes // line comments from source so they don't pollute
// the backstop's `dyn` keyword scan.
func stripLineComments(src []byte) []byte {
	var out []byte
	for len(src) > 0 {
		i := bytes.Index(src, []byte("//"))
		if i < 0 {
			out = append(out, src...)
			break
		}
		out = append(out, src[:i]...)
		eol := bytes.IndexByte(src[i:], '\n')
		if eol < 0 {
			break
		}
		src = src[i+eol:]
	}
	return out
}
