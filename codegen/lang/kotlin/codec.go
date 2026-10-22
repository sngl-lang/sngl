package kotlin

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// The runtime behind the codec intrinsics is SnglCodec, a class android
// writes beside MainScreen.kt in the program's own package
// (codegen/platform/android/codec.kt) when the program reaches one; so the
// emitted code names it bare.

func init() {
	codegen.DeclareLangImplements(langKt, ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic)
	codegen.RegisterIntrinsic(langKt, "http.pathEscape", func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return "SnglCodec.escape(" + tr(args[0]) + ")", nil
	})
}

// UsesCodec reports whether pkg calls a codec intrinsic anywhere, tests
// included, or pathEscape: what makes android write SnglCodec.
func UsesCodec(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && (codegen.IsCodecCall(c) || c.Func != nil && c.Func.Intrinsic == "http.pathEscape") {
			found = true
		}
		return nil
	})
	return found
}

// codecCall renders a call of a codec intrinsic over SnglCodec. A request is
// a call of a suspend function, made where its caller waits for it.
func (kc *KtIRContext) codecCall(n *ir.Call) (string, bool) {
	if !codegen.IsCodecCall(n) {
		return "", false
	}
	t, format, _ := ir.CodecCall(n)
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return kc.EvalExpr(a)
		}
		return "null"
	}
	str := ir.TypString
	switch n.Func.Intrinsic {
	case ir.JSONEncodeIntrinsic:
		return fmt.Sprintf("SnglCodec.encode(%s, %s)", arg("value"), ktCodec(ir.PlanCodec(t, format))), true
	case ir.JSONDecodeIntrinsic:
		failed := kc.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__r.error!!", str)))
		ok := kc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("(__r.value as "+IRTypeToKt(t)+")", t)))
		return fmt.Sprintf("run { val __r = SnglCodec.decode(%s, %s); if (__r.error != null) %s else %s }", arg("text"), ktCodec(ir.PlanCodec(t, format)), failed, ok), true
	}
	method := `"get"`
	if n.Func.Intrinsic == ir.HTTPSendIntrinsic {
		method = arg("method") + ".toString()"
	}
	url := arg("url")
	if t != nil && len(ir.PlanCodec(t, format).Root.Fields) > 0 {
		url = fmt.Sprintf("SnglCodec.withQuery(%s, %s, %s)", url, arg("query"), ktCodec(ir.PlanCodec(t, format)))
	}
	headers := arg("headers")
	if n.ArgFor("headers") == nil {
		headers = "emptyMap()"
	}
	st, ht, bt := codegen.ResponseFieldTypes(n.Type)
	msg := codegen.HostValue("__r.message", str)
	resp := codegen.CodecResponse(n.Type, codegen.HostValue("__r.status", st), codegen.HostValue("__r.headers", ht), codegen.HostValue("__r.body", bt))
	return fmt.Sprintf("run { val __r = SnglCodec.send(%s, %s, %s, %s); if (__r.failed) (if (__r.timeout) %s else %s) else %s }",
		method, url, headers, arg("body"),
		kc.EvalExpr(codegen.ResultFailed(n.Type, "timeout", msg)), kc.EvalExpr(codegen.ResultFailed(n.Type, "transport", msg)),
		kc.EvalExpr(codegen.ResultOK(n.Type, resp))), true
}

// ktCodec spells a codec plan as a SnglCodec: an expression, or a block
// declaring each struct and enum first where the plan names any, so a type
// that holds itself points back at its own. A struct carries how to build
// one from its fields' values and how to read them back, which is what
// Kotlin, having no reflection here, cannot find out for itself.
func ktCodec(p *ir.CodecPlan) string {
	if len(p.Named) == 0 {
		return ktCodecNode(p.Root)
	}
	var b strings.Builder
	b.WriteString("run { ")
	for _, n := range p.Named {
		kind := "STRUCT"
		if n.Kind == ir.CodecEnum {
			kind = "ENUM"
		}
		fmt.Fprintf(&b, "val c%d = SnglCodec(SnglCodec.%s); ", n.ID, kind)
	}
	for _, n := range p.Named {
		if n.Kind == ir.CodecEnum {
			wires := make([]string, len(n.Members))
			values := make([]string, len(n.Members))
			for i, m := range n.Members {
				wires[i] = Quote(m.WireName())
				values[i] = EnumName(n.Type.Decl) + "." + EnumEntry(m.Name)
			}
			fmt.Fprintf(&b, "c%d.wires = listOf(%s); c%d.values = listOf(%s); ", n.ID, strings.Join(wires, ", "), n.ID, strings.Join(values, ", "))
			continue
		}
		cls := IRTypeToKt(n.Type)
		fields := make([]string, len(n.Fields))
		args := make([]string, len(n.Fields))
		reads := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			fields[i] = fmt.Sprintf("SnglCodecField(%s, %s)", Quote(f.Wire), ktCodecNode(f.Codec))
			args[i] = fmt.Sprintf("%s = a[%d] as %s", SafeIdent(f.Name), i, IRTypeToKt(f.Codec.Type))
			reads[i] = "it." + SafeIdent(f.Name)
		}
		fmt.Fprintf(&b, "c%d.fields = listOf(%s); ", n.ID, strings.Join(fields, ", "))
		fmt.Fprintf(&b, "c%d.make = { a -> %s(%s) }; ", n.ID, cls, strings.Join(args, ", "))
		fmt.Fprintf(&b, "c%d.read = { v -> (v as %s).let { listOf<Any?>(%s) } }; ", n.ID, cls, strings.Join(reads, ", "))
	}
	fmt.Fprintf(&b, "%s }", ktCodecNode(p.Root))
	return b.String()
}

func ktCodecNode(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecStruct, ir.CodecEnum:
		return "c" + strconv.Itoa(c.ID)
	case ir.CodecOption, ir.CodecList, ir.CodecMap:
		return fmt.Sprintf("SnglCodec.of(SnglCodec.%s, %s)", ktCodecKind(c), ktCodecNode(c.Elem))
	}
	return fmt.Sprintf("SnglCodec.of(SnglCodec.%s)", ktCodecKind(c))
}

func ktCodecKind(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecBool:
		return "BOOL"
	case ir.CodecInt:
		if ktIntType(c.Type) == "Long" {
			return "LONG"
		}
		return "INT"
	case ir.CodecFloat:
		return "FLOAT"
	case ir.CodecString:
		return "STRING"
	case ir.CodecDate:
		return "DATE"
	case ir.CodecDatetime:
		return "DATETIME"
	case ir.CodecOption:
		return "OPTION"
	case ir.CodecList:
		return "LIST"
	case ir.CodecMap:
		return "MAP"
	}
	return "STRUCT"
}
