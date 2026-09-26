package html

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/ir"
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
  const t = Object.keys(v).filter(k => v[k] !== 0).map(k => v[k] + (k === "pct" ? "%" : k));
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
	style := &ir.Select{Operand: node, Field: "style", Type: ir.TypDyn}
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
