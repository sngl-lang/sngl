package html

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/htmlutil"
	"duckfam.us/sngl/ir"
)

// The page's functions from a SNGL value to the CSS text for it, written when
// a style field is only known at run time. Each spells its value the way
// htmlutil spells the same value when the build can read it.
const (
	styleColorHelper   = "_snglStyleColor"
	styleLengthHelper  = "_snglStyleLength"
	styleKeywordHelper = "_snglStyleKeyword"
)

var styleHelperJS = map[string]string{
	styleColorHelper: `function _snglStyleColor(c) {
  if (typeof c === "string") return c;
  if (c.a < 255) return "rgba(" + c.r + "," + c.g + "," + c.b + "," + c.a / 255 + ")";
  return "#" + [c.r, c.g, c.b].map(v => v.toString(16).padStart(2, "0")).join("");
}
`,
	styleLengthHelper: `function _snglStyleLength(v) {
  if (typeof v === "number") return v + "px";
  const t = Object.keys(v).filter(k => v[k] !== 0).map(k => v[k] + k);
  if (t.length === 0) return "0px";
  return t.length === 1 ? t[0] : "calc(" + t.join(" + ") + ")";
}
`,
	styleKeywordHelper: `function _snglStyleKeyword(m) {
  return m.replace(/[A-Z]/g, c => "-" + c.toLowerCase());
}
`,
}

// styleWrite is one CSS declaration a style field writes where the page runs.
// A declaration beside it that the field always writes (borderWidth's
// border-style) carries its text in fixed.
type styleWrite struct {
	field  string
	css    string
	fixed  string
	value  ir.Expr
	helper string
}

// dynamicStyleWrites is what htmlutil.BuildCSSStyleIR leaves out of a style
// literal: the fields whose value the build cannot read.
func dynamicStyleWrites(sl *ir.StructLit) []styleWrite {
	// A fixed declaration is a default, so a field that names the same
	// property (borderStyle beside borderWidth) wins; written after the
	// static attribute, the default would otherwise replace it on every write.
	named := map[string]bool{}
	for _, f := range sl.Fields {
		for _, d := range htmlutil.StylePropDecls(f.Name) {
			if d.Dynamic {
				named[d.Name] = true
			}
		}
	}
	var out []styleWrite
	for _, f := range sl.Fields {
		if f.Name == "" || f.Value == nil || staticStyleValue(f.Value) {
			continue
		}
		for _, d := range htmlutil.StylePropDecls(f.Name) {
			w := styleWrite{field: f.Name, css: d.Name}
			if d.Dynamic {
				w.value = f.Value
				w.helper = styleHelperFor(styleFieldType(sl, f), d.Name)
			} else if named[d.Name] {
				continue
			} else {
				w.fixed = d.Value
			}
			out = append(out, w)
		}
	}
	return out
}

func staticStyleValue(e ir.Expr) bool {
	if htmlutil.ExprToStaticValueIR(e) != "" {
		return true
	}
	switch x := e.(type) {
	// A literal with no CSS spelling (null, "") writes nothing, statically or here.
	case *ir.Literal:
		return true
	case *ir.Ident:
		return x.Member != ""
	}
	return false
}

func styleFieldType(sl *ir.StructLit, f ir.FieldInit) *ir.Type {
	if sl.Def != nil {
		for _, sf := range sl.Def.Fields {
			if sf.Name == f.Name {
				return sf.Type
			}
		}
	}
	return f.Value.ExprType()
}

func styleHelperFor(t *ir.Type, css string) string {
	switch {
	case t != nil && ir.IsColorStruct(t):
		return styleColorHelper
	case t != nil && t.Kind == ir.TypeEnum:
		return styleKeywordHelper
	case t != nil && t.Kind == ir.TypeUnit, htmlutil.IsSizeProp(css):
		return styleLengthHelper
	}
	return ""
}

// styleWriteStmts is the writes as IR, for a node the translator reaches
// through its handle: a handler's assignment or a factory's creation.
func (t *htmlTranslator) styleWriteStmts(node ir.Expr, writes []styleWrite) []ir.Stmt {
	style := &ir.Select{Operand: node, Field: "style", Type: ir.NativeNamed("CSSStyleDeclaration")}
	var out []ir.Stmt
	for _, w := range writes {
		var v ir.Expr = &ir.Literal{Type: ir.TypString, Value: w.fixed}
		if w.value != nil {
			v = w.value
			if w.helper != "" {
				t.jc.Ctx.Helpers[w.helper] = true
				v = &ir.Call{Type: ir.TypString, Func: &ir.Func{Name: w.helper}, Args: []ir.CallArg{{Value: v}}}
			}
		}
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: style,
			Func:     &ir.Func{Name: "setProperty"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Value: w.css}},
				{Value: v},
			},
		}})
	}
	return out
}

// styleWriteUpdaters writes a page node's run-time style fields on first
// render and, unless lowering already put the writes in the handlers, again
// whenever what they read changes. One updater per field, so a change
// rewrites only the declarations that read it.
func (g *htmlGen) styleWriteUpdaters(id string, style ir.Expr, lowered bool) {
	sl, ok := style.(*ir.StructLit)
	if !ok {
		if sd := styleValueDecl(style); sd != nil {
			g.useStyleValue(sd)
			js, requires := g.exprToJSReactiveCollect(style)
			g.initWrites = append(g.initWrites, updateFunc{
				funcName: fmt.Sprintf("$u_%s_style", id[1:]),
				body:     fmt.Sprintf(`%s.style.cssText = %s(%s);`, id, styleValueHelper, js),
				deps:     g.exprDeps(style),
				initOnly: lowered,
				requires: requires,
			})
		}
		return
	}
	byField := map[string][]styleWrite{}
	var order []string
	for _, w := range dynamicStyleWrites(sl) {
		if _, seen := byField[w.field]; !seen {
			order = append(order, w.field)
		}
		byField[w.field] = append(byField[w.field], w)
	}
	for _, field := range order {
		var body strings.Builder
		var deps map[string]bool
		var requires codegen.Requirement
		for _, w := range byField[field] {
			val := strconv.Quote(w.fixed)
			if w.value != nil {
				js, req := g.exprToJSReactiveCollect(w.value)
				requires = req
				deps = g.exprDeps(w.value)
				val = js
				if w.helper != "" {
					g.ctx.Helpers[w.helper] = true
					val = w.helper + "(" + js + ")"
				}
			}
			body.WriteString(fmt.Sprintf(`%s.style.setProperty(%q, %s);`, id, w.css, val))
		}
		g.initWrites = append(g.initWrites, updateFunc{
			funcName: fmt.Sprintf("$u_%s_style_%s", id[1:], field),
			body:     body.String(),
			deps:     deps,
			initOnly: lowered,
			requires: requires,
		})
	}
}

// styleValueHelper is the page's function from a whole Style value -- one held
// in state rather than written as a literal -- to the CSS text for it.
const styleValueHelper = "_snglStyleCSS"

func styleValueDecl(e ir.Expr) *ir.StructDef {
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	sd, _ := t.Decl.(*ir.StructDef)
	return sd
}

// styleValueWrite sets the node's inline style from a whole Style value.
// Through cssText rather than the attribute, so the browser parses the text
// and drops a declaration it refuses instead of keeping it verbatim.
func styleValueWrite(node, v ir.Expr) ir.Stmt {
	return &ir.Assign{
		Target: &ir.Select{Operand: &ir.Select{Operand: node, Field: "style", Type: ir.NativeNamed("CSSStyleDeclaration")}, Field: "cssText", Type: ir.TypString},
		Op:     ast.AssignSet,
		Value:  &ir.Call{Type: ir.TypString, Func: &ir.Func{Name: styleValueHelper}, Args: []ir.CallArg{{Value: v}}},
	}
}

func (g *htmlGen) useStyleValue(sd *ir.StructDef) {
	g.styleValueDef = sd
	g.ctx.Helpers[styleValueHelper] = true
}

// styleValueJS writes styleValueHelper for sd, one declaration per field the
// value sets. A field is unset when the object lacks it, which is how a
// literal the page builds leaves one, and a fixed declaration yields to a
// field naming the same property as dynamicStyleWrites' does.
func styleValueJS(sd *ir.StructDef, helpers map[string]bool) string {
	named := map[string]string{}
	for _, f := range sd.Fields {
		for _, d := range htmlutil.StylePropDecls(f.Name) {
			if d.Dynamic {
				named[d.Name] = f.Name
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(s) {\n  const d = [];\n", styleValueHelper)
	for _, f := range sd.Fields {
		for _, d := range htmlutil.StylePropDecls(f.Name) {
			cond := "s." + f.Name + " != null"
			var val string
			switch {
			case d.Dynamic:
				val = strconv.Quote(d.Name+":") + " + "
				if h := styleHelperFor(f.Type, d.Name); h != "" {
					helpers[h] = true
					val += h + "(s." + f.Name + ")"
				} else {
					val += "s." + f.Name
				}
			case named[d.Name] != "":
				cond += " && s." + named[d.Name] + " == null"
				val = strconv.Quote(d.Name + ":" + d.Value)
			default:
				val = strconv.Quote(d.Name + ":" + d.Value)
			}
			fmt.Fprintf(&b, "  if (%s) d.push(%s);\n", cond, val)
		}
	}
	b.WriteString("  return d.join(\";\");\n}\n")
	return b.String()
}
