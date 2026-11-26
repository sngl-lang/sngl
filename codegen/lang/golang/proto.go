package golang

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// sngl:grpc's call is written as code, not handed a descriptor: each message
// a call reaches gets a writer, a reader and a zero of its own, over the
// program's own Go struct, so a call costs what protoc-gen-go's would.
// snglgrpc carries the bytes.

const (
	grpcRuntime = "duckfam.us/sngl/pkg/go/snglgrpc"
	protowire   = "google.golang.org/protobuf/encoding/protowire"
)

func init() {
	codegen.DeclareLangImplements(langGo, ir.GRPCCallIntrinsic)
}

// protoMessages are the messages and enums the package's grpc.call calls
// reach, each once, in the order first reached.
type protoMessages struct {
	nodes []*ir.Codec
	seen  map[string]bool
}

func (p *protoMessages) add(t *ir.Type) {
	if t == nil || t.HasTypeParam() {
		return
	}
	for _, n := range ir.PlanCodec(t, ir.CodecProto).Named {
		key := protoFuncName(n)
		if p.seen == nil {
			p.seen = map[string]bool{}
		}
		if !p.seen[key] {
			p.seen[key] = true
			p.nodes = append(p.nodes, n)
		}
	}
}

// recordProtoHelpers records the messages a grpc.call reaches.
func recordProtoHelpers(h *HelperSet, e ir.Expr) {
	call, ok := e.(*ir.Call)
	if !ok {
		return
	}
	req, resp, ok := ir.ProtoCall(call)
	if !ok {
		return
	}
	h.Proto.add(req)
	h.Proto.add(resp)
}

// protoFuncName is what a message's or an enum's helpers are named after: its
// Go type, as a name.
func protoFuncName(n *ir.Codec) string {
	name := IRTypeToGo(n.Type)
	if ed, ok := n.Type.Decl.(*ir.EnumDef); ok && !ed.Carries() {
		name = ExportName(ed.Name)
	}
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// grpcCall renders a call of grpc.call: the request written by its message's
// writer, sent through snglgrpc, and the response read by its reader, as the
// async.Result the call answers.
func (gc *GoIRContext) grpcCall(n *ir.Call) (string, bool) {
	req, resp, ok := ir.ProtoCall(n)
	if !ok || req == nil {
		return "", false
	}
	gc.RequireImport(grpcRuntime)
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return gc.EvalExpr(a)
		}
		return `""`
	}
	ctx := "context.Background()"
	if life := n.ArgFor("life"); life != nil {
		gc.RequireImport(asyncRuntime)
		ctx = "snglasync.Context(" + gc.EvalExpr(&ir.Select{Operand: life, Field: "_id", Type: ir.TypInt}) + ")"
	} else {
		gc.RequireImport("context")
	}
	gc.RequireImport("strconv")
	str := ir.TypString
	reqNode := ir.PlanCodec(req, ir.CodecProto).Root
	respNode := ir.PlanCodec(resp, ir.CodecProto).Root
	failed := codegen.ResultFailedCode(n.Type, "server", codegen.HostValue("__snglM", str), codegen.HostValue("strconv.Itoa(__snglC)", str))
	var b strings.Builder
	fmt.Fprintf(&b, "func() %s { ", typeToGo(n.Type, gc.site()))
	fmt.Fprintf(&b, "__snglB, __snglC, __snglM := snglgrpc.Invoke(%s, %s, %s, snglPbWrite%s(nil, %s)); ", ctx, arg("server"), arg("method"), protoFuncName(reqNode), arg("request"))
	fmt.Fprintf(&b, "if __snglC != 0 { return %s }; ", gc.EvalExpr(failed))
	fmt.Fprintf(&b, "__snglV, __snglP := snglPbRead%s(__snglB, \"$\"); ", protoFuncName(respNode))
	fmt.Fprintf(&b, "if __snglP != \"\" { return %s }; ", gc.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__snglP", str))))
	fmt.Fprintf(&b, "return %s }()", gc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__snglV", resp))))
	return b.String(), true
}

// emit writes each recorded message's writer, reader and zero, and each
// enum's number and member.
func (p protoMessages) emit() string {
	var b strings.Builder
	for _, n := range p.nodes {
		switch n.Kind {
		case ir.CodecOneof:
		case ir.CodecEnum:
			emitProtoEnum(&b, n)
		default:
			emitProtoWriter(&b, n)
			emitProtoReader(&b, n)
			emitProtoZero(&b, n)
		}
	}
	return b.String()
}

func emitProtoEnum(b *strings.Builder, n *ir.Codec) {
	name := protoFuncName(n)
	fmt.Fprintf(b, "func snglPbNum%s(v string) uint64 {\n\tswitch v {\n", name)
	for i, m := range n.Members {
		fmt.Fprintf(b, "\tcase %q:\n\t\treturn uint64(int64(%d))\n", m.Name, m.ProtoNumber(i))
	}
	b.WriteString("\t}\n\treturn 0\n}\n\n")
	fmt.Fprintf(b, "func snglPbMember%s(x uint64) (string, bool) {\n\tswitch int32(x) {\n", name)
	seen := map[int]bool{}
	for i, m := range n.Members {
		num := m.ProtoNumber(i)
		if seen[num] {
			continue
		}
		seen[num] = true
		fmt.Fprintf(b, "\tcase %d:\n\t\treturn %q, true\n", num, m.Name)
	}
	b.WriteString("\t}\n\treturn \"\", false\n}\n\n")
}

func emitProtoWriter(b *strings.Builder, n *ir.Codec) {
	fmt.Fprintf(b, "func snglPbWrite%s(b []byte, m %s) []byte {\n", protoFuncName(n), IRTypeToGo(n.Type))
	for _, f := range n.Fields {
		field := "m." + ExportName(f.Name)
		num := strconv.Itoa(f.Number)
		c := f.Codec
		switch c.Kind {
		case ir.CodecOption:
			fmt.Fprintf(b, "\tif %s != nil {\n\t\tb = %s\n\t}\n", field, protoAppend("b", num, f.Proto, "(*"+field+")", c.Elem))
		case ir.CodecList:
			if protoPackedKind(f.Proto) {
				fmt.Fprintf(b, "\tif len(%s) > 0 {\n\t\tvar p []byte\n\t\tfor _, v := range %s {\n\t\t\tp = %s\n\t\t}\n", field, field, protoAppendValue("p", f.Proto, "v", c.Elem))
				fmt.Fprintf(b, "\t\tb = protowire.AppendTag(b, %s, protowire.BytesType)\n\t\tb = protowire.AppendBytes(b, p)\n\t}\n", num)
			} else {
				fmt.Fprintf(b, "\tfor _, v := range %s {\n\t\tb = %s\n\t}\n", field, protoAppend("b", num, f.Proto, "v", c.Elem))
			}
		case ir.CodecMap:
			fmt.Fprintf(b, "\tfor _, k := range snglgrpc.SortedKeys(%s) {\n", field)
			b.WriteString("\t\te := protowire.AppendTag(nil, 1, protowire.BytesType)\n\t\te = protowire.AppendString(e, k)\n")
			fmt.Fprintf(b, "\t\te = %s\n", protoAppend("e", "2", f.Proto, field+"[k]", c.Elem))
			fmt.Fprintf(b, "\t\tb = protowire.AppendTag(b, %s, protowire.BytesType)\n\t\tb = protowire.AppendBytes(b, e)\n\t}\n", num)
		case ir.CodecStruct:
			fmt.Fprintf(b, "\tb = %s\n", protoAppend("b", num, f.Proto, field, c))
		case ir.CodecOneof:
			fmt.Fprintf(b, "\tswitch %s.tag {\n", field)
			for i, m := range c.Fields {
				if m.Codec != nil {
					fmt.Fprintf(b, "\tcase %d:\n\t\tb = %s\n", i, protoAppend("b", strconv.Itoa(m.Number), m.Proto, field+"."+ExportName(m.Name), m.Codec))
				}
			}
			b.WriteString("\t}\n")
		default:
			fmt.Fprintf(b, "\tif %s != %s {\n\t\tb = %s\n\t}\n", field, protoGoZero(c), protoAppend("b", num, f.Proto, field, c))
		}
	}
	b.WriteString("\treturn b\n}\n\n")
}

// protoAppend is b with one record appended: the tag, and v written as kind.
func protoAppend(b, num string, kind ir.ProtoKind, v string, c *ir.Codec) string {
	tagged := fmt.Sprintf("protowire.AppendTag(%s, %s, %s)", b, num, protoWireType(kind))
	return protoAppendValue(tagged, kind, v, c)
}

// protoAppendValue is b with v written as kind, untagged.
func protoAppendValue(b string, kind ir.ProtoKind, v string, c *ir.Codec) string {
	switch kind {
	case ir.ProtoMessage:
		return fmt.Sprintf("protowire.AppendBytes(%s, snglPbWrite%s(nil, %s))", b, protoFuncName(c), v)
	case ir.ProtoString:
		return fmt.Sprintf("protowire.AppendString(%s, %s)", b, v)
	case ir.ProtoBytes:
		return fmt.Sprintf("protowire.AppendBytes(%s, snglgrpc.FromBase64(%s))", b, v)
	case ir.ProtoBool:
		return fmt.Sprintf("protowire.AppendVarint(%s, protowire.EncodeBool(%s))", b, v)
	case ir.ProtoEnum:
		return fmt.Sprintf("protowire.AppendVarint(%s, snglPbNum%s(%s))", b, protoFuncName(c), v)
	case ir.ProtoFloat:
		return fmt.Sprintf("protowire.AppendFixed32(%s, snglgrpc.Float32(float64(%s)))", b, v)
	case ir.ProtoDouble:
		return fmt.Sprintf("protowire.AppendFixed64(%s, snglgrpc.Float64(float64(%s)))", b, v)
	case ir.ProtoInt32:
		return fmt.Sprintf("protowire.AppendVarint(%s, uint64(int64(int32(%s))))", b, v)
	case ir.ProtoUint32:
		return fmt.Sprintf("protowire.AppendVarint(%s, uint64(uint32(%s)))", b, v)
	case ir.ProtoSint32:
		return fmt.Sprintf("protowire.AppendVarint(%s, protowire.EncodeZigZag(int64(int32(%s))))", b, v)
	case ir.ProtoSint64:
		return fmt.Sprintf("protowire.AppendVarint(%s, protowire.EncodeZigZag(int64(%s)))", b, v)
	case ir.ProtoFixed32, ir.ProtoSfixed32:
		return fmt.Sprintf("protowire.AppendFixed32(%s, uint32(%s))", b, v)
	case ir.ProtoFixed64, ir.ProtoSfixed64:
		return fmt.Sprintf("protowire.AppendFixed64(%s, uint64(%s))", b, v)
	}
	return fmt.Sprintf("protowire.AppendVarint(%s, uint64(%s))", b, v)
}

func protoWireType(kind ir.ProtoKind) string {
	switch kind {
	case ir.ProtoMessage, ir.ProtoString, ir.ProtoBytes:
		return "protowire.BytesType"
	case ir.ProtoFloat, ir.ProtoFixed32, ir.ProtoSfixed32:
		return "protowire.Fixed32Type"
	case ir.ProtoDouble, ir.ProtoFixed64, ir.ProtoSfixed64:
		return "protowire.Fixed64Type"
	}
	return "protowire.VarintType"
}

func protoPackedKind(kind ir.ProtoKind) bool {
	return protoWireType(kind) != "protowire.BytesType"
}

// protoGoZero is the Go spelling of proto3's zero for a scalar: a field
// holding it is not written.
func protoGoZero(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecBool:
		return "false"
	case ir.CodecString:
		return `""`
	case ir.CodecEnum:
		for i, m := range c.Members {
			if m.ProtoNumber(i) == 0 {
				return strconv.Quote(m.Name)
			}
		}
		return `""`
	}
	return "0"
}

func emitProtoReader(b *strings.Builder, n *ir.Codec) {
	name := protoFuncName(n)
	fmt.Fprintf(b, "func snglPbRead%s(b []byte, path string) (%s, string) {\n", name, IRTypeToGo(n.Type))
	fmt.Fprintf(b, "\tm := snglPbZero%s()\n", name)
	b.WriteString("\tfor len(b) > 0 {\n\t\tnum, typ, n := protowire.ConsumeTag(b)\n\t\tif n < 0 {\n\t\t\treturn m, path + \": not protobuf\"\n\t\t}\n\t\tb = b[n:]\n\t\tswitch num {\n")
	for _, f := range n.Fields {
		field := "m." + ExportName(f.Name)
		at := strconv.Quote("." + f.Name)
		c := f.Codec
		if c.Kind == ir.CodecOneof {
			for i, m := range c.Fields {
				if m.Codec == nil {
					continue
				}
				fmt.Fprintf(b, "\t\tcase %d:\n", m.Number)
				protoReadValue(b, "\t\t\t", "b", "typ", m.Proto, m.Codec, strconv.Quote("."+f.Name+"."+m.Name), func(v string) string {
					return fmt.Sprintf("%s = %s{tag: %d, %s: %s}", field, IRTypeToGo(c.Type), i, ExportName(m.Name), v)
				})
				b.WriteString("\t\t\tb = b[n:]\n")
			}
			continue
		}
		fmt.Fprintf(b, "\t\tcase %d:\n", f.Number)
		switch c.Kind {
		case ir.CodecList:
			if protoPackedKind(f.Proto) {
				b.WriteString("\t\t\tif typ == protowire.BytesType {\n")
				b.WriteString("\t\t\t\tp, k := protowire.ConsumeBytes(b)\n\t\t\t\tif k < 0 {\n\t\t\t\t\treturn m, path + " + at + " + \": not protobuf\"\n\t\t\t\t}\n")
				b.WriteString("\t\t\t\tfor len(p) > 0 {\n")
				protoReadValue(b, "\t\t\t\t\t", "p", protoWireType(f.Proto), f.Proto, c.Elem, at, func(v string) string {
					return field + " = append(" + field + ", " + v + ")"
				})
				b.WriteString("\t\t\t\t\tp = p[n:]\n\t\t\t\t}\n\t\t\t\tn = k\n\t\t\t} else {\n")
				protoReadValue(b, "\t\t\t\t", "b", "typ", f.Proto, c.Elem, at, func(v string) string {
					return field + " = append(" + field + ", " + v + ")"
				})
				b.WriteString("\t\t\t}\n")
			} else {
				protoReadValue(b, "\t\t\t", "b", "typ", f.Proto, c.Elem, at, func(v string) string {
					return field + " = append(" + field + ", " + v + ")"
				})
			}
		case ir.CodecMap:
			b.WriteString("\t\t\te, k := snglgrpc.Bytes(b, typ)\n\t\t\tif k < 0 {\n\t\t\t\treturn m, path + " + at + " + \": not protobuf\"\n\t\t\t}\n")
			if z := protoGoZeroValue(c.Elem); z != "" {
				fmt.Fprintf(b, "\t\t\tvar key string\n\t\t\tval := %s(%s)\n", IRTypeToGo(c.Elem.Type), z)
			} else {
				fmt.Fprintf(b, "\t\t\tvar key string\n\t\t\tvar val %s\n", IRTypeToGo(c.Elem.Type))
			}
			b.WriteString("\t\t\tfor len(e) > 0 {\n\t\t\t\tnum, typ, n := protowire.ConsumeTag(e)\n\t\t\t\tif n < 0 {\n\t\t\t\t\treturn m, path + " + at + " + \": not protobuf\"\n\t\t\t\t}\n\t\t\t\te = e[n:]\n")
			b.WriteString("\t\t\t\tswitch num {\n\t\t\t\tcase 1:\n")
			protoReadValue(b, "\t\t\t\t\t", "e", "typ", ir.ProtoString, &ir.Codec{Kind: ir.CodecString}, at, func(v string) string { return "key = " + v })
			b.WriteString("\t\t\t\t\te = e[n:]\n\t\t\t\tcase 2:\n")
			protoReadValue(b, "\t\t\t\t\t", "e", "typ", f.Proto, c.Elem, at, func(v string) string { return "val = " + v })
			b.WriteString("\t\t\t\t\te = e[n:]\n\t\t\t\tdefault:\n\t\t\t\t\tn = protowire.ConsumeFieldValue(num, typ, e)\n\t\t\t\t\tif n < 0 {\n\t\t\t\t\t\treturn m, path + " + at + " + \": not protobuf\"\n\t\t\t\t\t}\n\t\t\t\t\te = e[n:]\n\t\t\t\t}\n\t\t\t}\n")
			fmt.Fprintf(b, "\t\t\t%s[key] = val\n\t\t\tn = k\n", field)
		case ir.CodecOption:
			protoReadValue(b, "\t\t\t", "b", "typ", f.Proto, c.Elem, at, func(v string) string {
				return "{\n\t\t\t\tx := " + v + "\n\t\t\t\t" + field + " = &x\n\t\t\t}"
			})
		default:
			protoReadValue(b, "\t\t\t", "b", "typ", f.Proto, c, at, func(v string) string { return field + " = " + v })
		}
		b.WriteString("\t\t\tb = b[n:]\n")
	}
	b.WriteString("\t\tdefault:\n\t\t\tn = protowire.ConsumeFieldValue(num, typ, b)\n\t\t\tif n < 0 {\n\t\t\t\treturn m, path + \": not protobuf\"\n\t\t\t}\n\t\t\tb = b[n:]\n\t\t}\n\t}\n\treturn m, \"\"\n}\n\n")
}

// protoReadValue writes the statements reading one value of kind from src,
// a record of wire type typ, into n (the bytes it took) and handing it to
// store; a value that is not one returns the failure.
func protoReadValue(b *strings.Builder, indent, src, typ string, kind ir.ProtoKind, c *ir.Codec, at string, store func(v string) string) {
	fail := func(what string) string {
		return indent + "if n < 0 {\n" + indent + "\treturn m, path + " + at + " + \": want " + what + "\"\n" + indent + "}\n"
	}
	goType := IRTypeToGo(c.Type)
	var read, value string
	switch kind {
	case ir.ProtoMessage:
		fmt.Fprintf(b, "%sraw, n := snglgrpc.Bytes(%s, %s)\n%s", indent, src, typ, fail(string(kind)))
		fmt.Fprintf(b, "%sv, problem := snglPbRead%s(raw, path+%s)\n%sif problem != \"\" {\n%s\treturn m, problem\n%s}\n", indent, protoFuncName(c), at, indent, indent, indent)
		fmt.Fprintf(b, "%s%s\n", indent, store("v"))
		return
	case ir.ProtoEnum:
		fmt.Fprintf(b, "%sx, n := snglgrpc.Varint(%s, %s)\n%s", indent, src, typ, fail(string(kind)))
		fmt.Fprintf(b, "%sv, ok := snglPbMember%s(x)\n%sif !ok {\n%s\treturn m, path + %s + \": no member numbered \" + strconv.Itoa(int(int32(x)))\n%s}\n", indent, protoFuncName(c), indent, indent, at, indent)
		fmt.Fprintf(b, "%s%s\n", indent, store("v"))
		return
	case ir.ProtoString:
		read, value = "raw, n := snglgrpc.Bytes", "string(raw)"
	case ir.ProtoBytes:
		read, value = "raw, n := snglgrpc.Bytes", "snglgrpc.ToBase64(raw)"
	case ir.ProtoBool:
		read, value = "x, n := snglgrpc.Varint", "x != 0"
	case ir.ProtoFloat:
		read, value = "x, n := snglgrpc.Fixed32", goType+"(snglgrpc.FromFloat32(x))"
	case ir.ProtoDouble:
		read, value = "x, n := snglgrpc.Fixed64", goType+"(snglgrpc.FromFloat64(x))"
	case ir.ProtoInt32:
		read, value = "x, n := snglgrpc.Varint", goType+"(int32(x))"
	case ir.ProtoUint32:
		read, value = "x, n := snglgrpc.Varint", goType+"(uint32(x))"
	case ir.ProtoSint32, ir.ProtoSint64:
		read, value = "x, n := snglgrpc.Varint", goType+"(protowire.DecodeZigZag(x))"
	case ir.ProtoFixed32:
		read, value = "x, n := snglgrpc.Fixed32", goType+"(x)"
	case ir.ProtoSfixed32:
		read, value = "x, n := snglgrpc.Fixed32", goType+"(int32(x))"
	case ir.ProtoFixed64:
		read, value = "x, n := snglgrpc.Fixed64", goType+"(x)"
	case ir.ProtoSfixed64:
		read, value = "x, n := snglgrpc.Fixed64", goType+"(int64(x))"
	default:
		read, value = "x, n := snglgrpc.Varint", goType+"(int64(x))"
	}
	fmt.Fprintf(b, "%s%s(%s, %s)\n%s", indent, read, src, typ, fail(string(kind)))
	fmt.Fprintf(b, "%s%s\n", indent, store(value))
}

// emitProtoZero writes what a message with no records reads as: proto3's
// zero for each field, the member numbered 0 for an enum, an empty list or
// map that a write may add to, and a message of zeros.
func emitProtoZero(b *strings.Builder, n *ir.Codec) {
	t := IRTypeToGo(n.Type)
	fmt.Fprintf(b, "func snglPbZero%s() %s {\n\treturn %s{", protoFuncName(n), t, t)
	var parts []string
	for _, f := range n.Fields {
		if v := protoGoZeroValue(f.Codec); v != "" {
			parts = append(parts, ExportName(f.Name)+": "+v)
		}
	}
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString("}\n}\n\n")
}

// protoGoZeroValue is the zero a field of c starts as, or "" where Go's own
// is proto3's.
func protoGoZeroValue(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecEnum:
		return protoGoZero(c)
	case ir.CodecList:
		return IRTypeToGo(c.Type) + "{}"
	case ir.CodecMap:
		return IRTypeToGo(c.Type) + "{}"
	case ir.CodecStruct:
		return "snglPbZero" + protoFuncName(c) + "()"
	}
	return ""
}
