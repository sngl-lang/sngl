package golang

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

const (
	jsonRuntime = "duckfam.us/sngl/pkg/go/sngljson"
	httpRuntime = "duckfam.us/sngl/pkg/go/snglhttp"
)

func init() {
	codegen.DeclareLangImplements(langGo, ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic)
	regImp("http.pathEscape", []string{jsonRuntime}, func(a []string) string { return "sngljson.Escape(" + a[0] + ")" })
}

// codecCall renders a call of json.decode, json.encode, http.send or
// http.get: a call into pkg/go/sngljson or pkg/go/snglhttp handed a
// descriptor of the type, which reads and writes the program's own Go types
// by reflection, and the async.Result built from what it handed back.
func (gc *GoIRContext) codecCall(n *ir.Call) (string, bool) {
	if !codegen.IsCodecCall(n) {
		return "", false
	}
	t, format, _ := ir.CodecCall(n)
	gc.RequireImport(jsonRuntime)
	// The type is spelled at the call, and a date is a time.Time.
	if t.Any(func(x *ir.Type) bool { return ir.IsDateStruct(x) || ir.IsDateTimeStruct(x) }) {
		gc.RequireImport("time")
	}
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return gc.EvalExpr(a)
		}
		return ""
	}
	rt := IRTypeToGo(n.Type)
	str := ir.TypString
	switch n.Func.Intrinsic {
	case ir.JSONEncodeIntrinsic:
		return fmt.Sprintf("sngljson.Encode[%s](%s, %s)", IRTypeToGo(t), arg("value"), goCodec(ir.PlanCodec(t, format))), true
	case ir.JSONDecodeIntrinsic:
		var b strings.Builder
		fmt.Fprintf(&b, "func() %s { __snglV, __snglMsg := sngljson.Decode[%s](%s, %s); ", rt, IRTypeToGo(t), arg("text"), goCodec(ir.PlanCodec(t, format)))
		fmt.Fprintf(&b, "if __snglMsg != \"\" { return %s }; ", gc.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__snglMsg", str))))
		fmt.Fprintf(&b, "return %s }()", gc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__snglV", t))))
		return b.String(), true
	}
	gc.RequireImport(httpRuntime)
	method := `"GET"`
	if n.Func.Intrinsic == ir.HTTPSendIntrinsic {
		method = arg("method")
	}
	url := arg("url")
	// A query with no fields writes nothing, and is read only so that a
	// binding passAsyncOffload made of it is used.
	keep := ""
	if t != nil && len(ir.PlanCodec(t, format).Root.Fields) > 0 {
		url = fmt.Sprintf("sngljson.WithQuery(%s, sngljson.Query(%s, %s))", url, arg("query"), goCodec(ir.PlanCodec(t, format)))
	} else if _, ok := n.ArgFor("query").(*ir.Ident); ok {
		keep = "_ = " + arg("query") + "; "
	}
	headers, body := arg("headers"), arg("body")
	if headers == "" {
		headers = "nil"
	}
	if body == "" {
		body = "nil"
	}
	st, ht, bt := codegen.ResponseFieldTypes(n.Type)
	msg := codegen.HostValue("__snglF.Message", str)
	var b strings.Builder
	fmt.Fprintf(&b, "func() %s { %s__snglR, __snglF := snglhttp.Send(%s, %s, %s, %s); ", rt, keep, method, url, headers, body)
	fmt.Fprintf(&b, "if __snglF != nil { if __snglF.Timeout { return %s }; return %s }; ",
		gc.EvalExpr(codegen.ResultFailed(n.Type, "timeout", msg)), gc.EvalExpr(codegen.ResultFailed(n.Type, "transport", msg)))
	resp := codegen.CodecResponse(n.Type, codegen.HostValue("__snglR.Status", st), codegen.HostValue("__snglR.Headers", ht), codegen.HostValue("__snglR.Body", bt))
	fmt.Fprintf(&b, "return %s }()", gc.EvalExpr(codegen.ResultOK(n.Type, resp)))
	return b.String(), true
}

// goCodec spells a codec plan as a *sngljson.Codec: an expression, or a
// function literal declaring each struct and enum first where the plan names
// any, so a type that holds itself points back at its own.
func goCodec(p *ir.CodecPlan) string {
	if len(p.Named) == 0 {
		return goCodecNode(p.Root)
	}
	var b strings.Builder
	b.WriteString("func() *sngljson.Codec { ")
	for _, n := range p.Named {
		if n.Kind == ir.CodecEnum {
			wires := make([]string, len(n.Members))
			names := make([]string, len(n.Members))
			for i, m := range n.Members {
				wires[i] = strconv.Quote(m.WireName())
				names[i] = strconv.Quote(m.Name)
			}
			fmt.Fprintf(&b, "c%d := &sngljson.Codec{Kind: sngljson.Enum, Wires: []string{%s}, Names: []string{%s}}; ", n.ID, strings.Join(wires, ", "), strings.Join(names, ", "))
			continue
		}
		fmt.Fprintf(&b, "c%d := &sngljson.Codec{Kind: sngljson.Struct}; ", n.ID)
	}
	for _, n := range p.Named {
		if n.Kind != ir.CodecStruct {
			continue
		}
		fields := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			fields[i] = fmt.Sprintf("{Wire: %s, Codec: %s}", strconv.Quote(f.Wire), goCodecNode(f.Codec))
		}
		fmt.Fprintf(&b, "c%d.Fields = []sngljson.Field{%s}; ", n.ID, strings.Join(fields, ", "))
	}
	fmt.Fprintf(&b, "return %s }()", goCodecNode(p.Root))
	return b.String()
}

func goCodecNode(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecStruct, ir.CodecEnum:
		return "c" + strconv.Itoa(c.ID)
	case ir.CodecOption, ir.CodecList, ir.CodecMap:
		return fmt.Sprintf("&sngljson.Codec{Kind: sngljson.%s, Elem: %s}", goCodecKind[c.Kind], goCodecNode(c.Elem))
	}
	return fmt.Sprintf("&sngljson.Codec{Kind: sngljson.%s}", goCodecKind[c.Kind])
}

var goCodecKind = map[ir.CodecKind]string{
	ir.CodecBool:     "Bool",
	ir.CodecInt:      "Int",
	ir.CodecFloat:    "Float",
	ir.CodecString:   "String",
	ir.CodecDate:     "Date",
	ir.CodecDatetime: "Datetime",
	ir.CodecOption:   "Option",
	ir.CodecList:     "List",
	ir.CodecMap:      "Map",
}
