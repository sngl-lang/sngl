package gtk4

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

// styleFlexes reports whether a `style` value asks for a share of its box. A
// flex that is not a literal is none, as on fyne: the expand is set where the
// widget is built.
func styleFlexes(style ir.Expr) bool {
	lit, ok := style.(*ir.StructLit)
	if !ok {
		return false
	}
	for _, f := range lit.Fields {
		if f.Name == "flex" {
			v, ok := codegen.MeasurementPx(f.Value, 0)
			return ok && v > 0
		}
	}
	return false
}

// styleFields are the fields of a `ui.Style` this platform reads, in the
// order gtk4rt.Style declares them. The rest of a Style is GTK's own.
var styleFields = []string{
	"width", "minWidth", "height", "gap",
	"padding", "paddingX", "paddingY", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
	"margin", "marginX", "marginY", "marginTop", "marginRight", "marginBottom", "marginLeft",
	"fontSize", "fontWeight", "fontStyle", "color",
	"background", "borderRadius",
}

// rtStyleDef is gtk4rt.Style, which a literal of names without the file
// declaring it.
var rtStyleDef = &ir.StructDef{Name: "Style", Foreign: ir.Foreign{Path: gtk4rtPkg, Name: "gtk4rt.Style"}}

// heldStyleFn is the helper reading a Style held as a value into the
// gtk4rt.Style it is drawn with, written once into a file that holds one.
const heldStyleFn = "snglStyle"

// setStyle is the gtk4rt.SetStyle call that applies a `style` to node, or
// nothing where the style sets no field this platform reads. A literal is read
// field by field, which is every constant style and any field reading state.
// A Style held as a value -- a prop, a merge of one, a var -- goes through
// snglStyle, which reads the same fields off it at run time, so a style the
// program computes is drawn as one it wrote. Only sngl:ui's: a shape's style
// is a drawing's, not a widget's.
func (t *gtk4Translator) setStyle(node, value ir.Expr) ir.Stmt {
	var arg ir.Expr
	if lit, ok := value.(*ir.StructLit); ok {
		read := map[string]ir.Expr{}
		for _, f := range lit.Fields {
			read[f.Name] = f.Value
		}
		lit := rtStyleLit(read)
		if lit == nil {
			return nil
		}
		arg = lit
	} else {
		sd, ok := value.ExprType().Decl.(*ir.StructDef)
		if !ok || sd.Name != "Style" || sd.PkgPath() != "sngl:ui" {
			return nil
		}
		t.shared.heldStyle = sd
		arg = &ir.Call{Type: rtStyleType(), Func: &ir.Func{Name: heldStyleFn}, Args: []ir.CallArg{{Value: value}}}
	}
	return &ir.CallStmt{Call: rtCall("SetStyle", ir.TypVoid, t.rtHandle(t.qualifyNodeExpr(node)), arg)}
}

// rtStyleLit is the gtk4rt.Style literal of the fields read names, nil where
// it names none this platform reads.
func rtStyleLit(read map[string]ir.Expr) *ir.StructLit {
	var fields []ir.FieldInit
	for _, name := range styleFields {
		if v, ok := read[name]; ok {
			fields = append(fields, ir.FieldInit{Name: name, Value: stylePx(v)})
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ir.StructLit{Type: rtStyleType(), Def: rtStyleDef, Fields: fields}
}

func rtStyleType() *ir.Type { return &ir.Type{Kind: ir.TypeStruct, Decl: rtStyleDef} }

// heldStyleDecl is snglStyle, for the Style declaration sd.
func heldStyleDecl(gc *golang.GoIRContext, sd *ir.StructDef) string {
	s := &ir.Param{Name: "s", Type: &ir.Type{Kind: ir.TypeStruct, Decl: sd}}
	read := map[string]ir.Expr{}
	for _, f := range sd.Fields {
		read[f.Name] = &ir.Select{Operand: &ir.Ident{Name: s.Name, Type: s.Type, Sym: s}, Field: f.Name, Type: f.Type}
	}
	fn := &ir.Func{Name: heldStyleFn, Params: []*ir.Param{s}, Return: rtStyleType(), Synthesized: true,
		Block: []ir.Stmt{&ir.Return{Value: rtStyleLit(read)}}}
	return strings.Join(gc.EmitFuncDef(fn), "\n") + "\n"
}

// stylePx is a length as the pixels gtk4rt takes: a constant folded, any
// other read off its bases. Anything that is not a length is itself.
func stylePx(v ir.Expr) ir.Expr {
	if px, ok := codegen.MeasurementPx(v, emPx); ok {
		return &ir.Literal{Type: ir.TypFloat, Value: strconv.FormatFloat(px, 'f', -1, 64)}
	}
	if ud := ir.UnitDeclOf(v.ExprType()); ud != nil {
		return codegen.MeasurementPxExpr(ud, v, emPx)
	}
	return v
}
