package kotlin

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// sngl:grpc's call is written as code, as Go's is: a writer and a reader per
// message a call reaches over the program's own classes, and a number and a
// member lookup per enum, in SnglProto, which android writes beside
// MainScreen.kt with the transport, SnglGrpc
// (codegen/platform/android/grpc.kt).

func init() {
	codegen.DeclareLangImplements(langKt, ir.GRPCCallIntrinsic)
}

// UsesGRPC reports whether pkg calls a gRPC method anywhere, tests included.
func UsesGRPC(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == ir.GRPCCallIntrinsic {
			found = true
		}
		return nil
	})
	return found
}

// ProtoHelpers is SnglProto's source: the helpers of every message and enum
// pkg's calls reach, each once, in pkg (the Kotlin package).
func ProtoHelpers(pkg *ir.Package, ktPkg string) string {
	var nodes []*ir.Codec
	seen := map[string]bool{}
	add := func(t *ir.Type) {
		if t == nil || t.HasTypeParam() {
			return
		}
		for _, n := range ir.PlanCodec(t, ir.CodecProto).Named {
			if k := protoName(n); !seen[k] {
				seen[k] = true
				nodes = append(nodes, n)
			}
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok {
			if req, resp, ok := ir.ProtoCall(c); ok {
				add(req)
				add(resp)
			}
		}
		return nil
	})
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", ktPkg)
	b.WriteString("@Suppress(\"UNUSED_PARAMETER\", \"NAME_SHADOWING\", \"UNUSED_VARIABLE\", \"RedundantExplicitType\")\nobject SnglProto {\n")
	b.WriteString("    fun write(write: (SnglPbWriter) -> Unit): ByteArray = SnglPbWriter().also(write).bytes()\n\n")
	b.WriteString("    fun <T> decode(read: () -> T): SnglPbDecoded<T> = try {\n        SnglPbDecoded(read(), null)\n    } catch (e: SnglPbFail) {\n        SnglPbDecoded(null, e.message)\n    }\n")
	for _, n := range nodes {
		switch n.Kind {
		case ir.CodecOneof:
		case ir.CodecEnum:
			emitKtProtoEnum(&b, n)
		default:
			emitKtProtoWriter(&b, n)
			emitKtProtoReader(&b, n)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// grpcCall renders a call of grpc.call: the request written by its message's
// writer, carried by SnglGrpc, and the response read by its reader, as the
// async.Result the call answers.
func (kc *KtIRContext) grpcCall(n *ir.Call) (string, bool) {
	req, resp, ok := ir.ProtoCall(n)
	if !ok || req == nil {
		return "", false
	}
	arg := func(name string) string {
		if a := n.ArgFor(name); a != nil {
			return kc.EvalExpr(a)
		}
		return `""`
	}
	life := "0"
	if id := codegen.LifetimeID(n); id != nil {
		life = kc.EvalExpr(id)
	}
	str := ir.TypString
	reqNode := ir.PlanCodec(req, ir.CodecProto).Root
	respNode := ir.PlanCodec(resp, ir.CodecProto).Root
	failed := kc.EvalExpr(codegen.ResultFailedCode(n.Type, "server", codegen.HostValue("__g.message", str), codegen.HostValue("__g.code.toString()", str)))
	undecoded := kc.EvalExpr(codegen.ResultFailed(n.Type, "decode", codegen.HostValue("__d.error", str)))
	got := kc.EvalExpr(codegen.ResultOK(n.Type, codegen.HostValue("__d.value!!", resp)))
	return fmt.Sprintf("run { val __g = SnglGrpc.call(%s, %s, SnglProto.write { SnglProto.write%s(it, %s) }, %s); if (__g.code != 0) %s else { val __d = SnglProto.decode { SnglProto.read%s(SnglPbReader(__g.body), \"$\") }; if (__d.error != null) %s else %s } }",
		arg("server"), arg("method"), protoName(reqNode), arg("request"), life, failed, protoName(respNode), undecoded, got), true
}

// protoName is what a message's or an enum's helpers are named after.
func protoName(n *ir.Codec) string {
	name := IRTypeToKt(n.Type)
	if ed, ok := n.Type.Decl.(*ir.EnumDef); ok {
		name = EnumName(ed)
	}
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func emitKtProtoEnum(b *strings.Builder, n *ir.Codec) {
	name, cls := protoName(n), EnumName(n.Type.Decl)
	fmt.Fprintf(b, "\n    fun num%s(v: %s): Long = when (v) {\n", name, cls)
	for i, m := range n.Members {
		fmt.Fprintf(b, "        %s.%s -> %dL\n", cls, EnumEntry(m.Name), m.ProtoNumber(i))
	}
	b.WriteString("    }\n")
	fmt.Fprintf(b, "\n    fun member%s(x: Long, path: String): %s = when (x.toInt()) {\n", name, cls)
	seen := map[int]bool{}
	for i, m := range n.Members {
		num := m.ProtoNumber(i)
		if seen[num] {
			continue
		}
		seen[num] = true
		fmt.Fprintf(b, "        %d -> %s.%s\n", num, cls, EnumEntry(m.Name))
	}
	b.WriteString("        else -> throw SnglPbFail(path + \": no member numbered \" + x.toInt())\n    }\n")
}

// ktWireType is the wire type a value of kind is written as.
func ktWireType(kind ir.ProtoKind) int {
	switch kind {
	case ir.ProtoMessage, ir.ProtoString, ir.ProtoBytes:
		return 2
	case ir.ProtoFloat, ir.ProtoFixed32, ir.ProtoSfixed32:
		return 5
	case ir.ProtoDouble, ir.ProtoFixed64, ir.ProtoSfixed64:
		return 1
	}
	return 0
}

func emitKtProtoWriter(b *strings.Builder, n *ir.Codec) {
	fmt.Fprintf(b, "\n    fun write%s(w: SnglPbWriter, m: %s) {\n", protoName(n), IRTypeToKt(n.Type))
	for _, f := range n.Fields {
		field := "m." + SafeIdent(f.Name)
		c := f.Codec
		switch c.Kind {
		case ir.CodecOption:
			fmt.Fprintf(b, "        %s?.let { x -> %s }\n", field, ktAppend("w", f.Number, f.Proto, "x", c.Elem))
		case ir.CodecList:
			if ktWireType(f.Proto) != 2 {
				fmt.Fprintf(b, "        if (%s.isNotEmpty()) {\n            w.tag(%d, 2)\n            w.sub { p -> for (v in %s) %s }\n        }\n", field, f.Number, field, ktAppendValue("p", f.Proto, "v", c.Elem))
			} else {
				fmt.Fprintf(b, "        for (v in %s) { %s }\n", field, ktAppend("w", f.Number, f.Proto, "v", c.Elem))
			}
		case ir.CodecMap:
			fmt.Fprintf(b, "        for (k in %s.keys.sorted()) {\n            w.tag(%d, 2)\n            w.sub { e -> e.tag(1, 2); e.string(k); %s }\n        }\n", field, f.Number, ktAppend("e", 2, f.Proto, field+".getValue(k)", c.Elem))
		case ir.CodecStruct:
			fmt.Fprintf(b, "        %s\n", ktAppend("w", f.Number, f.Proto, field, c))
		case ir.CodecOneof:
			fmt.Fprintf(b, "        when (val o = %s) {\n", field)
			for _, mem := range c.Fields {
				if mem.Codec != nil {
					fmt.Fprintf(b, "            is %s -> { %s }\n", enumVariant(c.Type, mem.Name), ktAppend("w", mem.Number, mem.Proto, "o.value", mem.Codec))
				}
			}
			b.WriteString("            else -> {}\n        }\n")
		default:
			fmt.Fprintf(b, "        if (%s != %s) { %s }\n", field, ktProtoZero(c), ktAppend("w", f.Number, f.Proto, field, c))
		}
	}
	b.WriteString("    }\n")
}

// ktAppend is the statements writing one record: the tag, and v written as
// kind.
func ktAppend(w string, num int, kind ir.ProtoKind, v string, c *ir.Codec) string {
	return fmt.Sprintf("%s.tag(%d, %d); %s", w, num, ktWireType(kind), ktAppendValue(w, kind, v, c))
}

// ktAppendValue writes v as kind, untagged.
func ktAppendValue(w string, kind ir.ProtoKind, v string, c *ir.Codec) string {
	switch kind {
	case ir.ProtoMessage:
		return fmt.Sprintf("%s.sub { s -> write%s(s, %s) }", w, protoName(c), v)
	case ir.ProtoString:
		return fmt.Sprintf("%s.string(%s)", w, v)
	case ir.ProtoBytes:
		return fmt.Sprintf("%s.base64(%s)", w, v)
	case ir.ProtoBool:
		return fmt.Sprintf("%s.varint(if (%s) 1L else 0L)", w, v)
	case ir.ProtoEnum:
		return fmt.Sprintf("%s.varint(num%s(%s))", w, protoName(c), v)
	case ir.ProtoFloat:
		return fmt.Sprintf("%s.fixed32(%s.toFloat().toRawBits())", w, v)
	case ir.ProtoDouble:
		return fmt.Sprintf("%s.fixed64(%s.toDouble().toRawBits())", w, v)
	case ir.ProtoInt32:
		return fmt.Sprintf("%s.varint(%s.toLong().toInt().toLong())", w, v)
	case ir.ProtoUint32:
		return fmt.Sprintf("%s.varint(%s.toLong() and 0xffffffffL)", w, v)
	case ir.ProtoSint32:
		return fmt.Sprintf("%s.zigzag32(%s.toLong().toInt())", w, v)
	case ir.ProtoSint64:
		return fmt.Sprintf("%s.zigzag64(%s.toLong())", w, v)
	case ir.ProtoFixed32, ir.ProtoSfixed32:
		return fmt.Sprintf("%s.fixed32(%s.toLong().toInt())", w, v)
	case ir.ProtoFixed64, ir.ProtoSfixed64:
		return fmt.Sprintf("%s.fixed64(%s.toLong())", w, v)
	}
	return fmt.Sprintf("%s.varint(%s.toLong())", w, v)
}

// ktProtoZero is proto3's zero for a scalar, typed as the field is: a field
// holding it is not written.
func ktProtoZero(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecBool:
		return "false"
	case ir.CodecString:
		return `""`
	case ir.CodecFloat:
		if IRTypeToKt(c.Type) == "Float" {
			return "0.0f"
		}
		return "0.0"
	case ir.CodecInt:
		switch ktIntType(c.Type) {
		case "Long":
			return "0L"
		case "Int":
			return "0"
		case "UInt":
			return "0u"
		case "ULong":
			return "0uL"
		}
		return "0" + ktFromLong(ktIntType(c.Type))
	case ir.CodecEnum:
		cls := EnumName(c.Type.Decl)
		for i, m := range c.Members {
			if m.ProtoNumber(i) == 0 {
				return cls + "." + EnumEntry(m.Name)
			}
		}
		return cls + "." + EnumEntry(c.Members[0].Name)
	}
	return "null"
}

// ktFromLong converts a Long to the Kotlin integer type kt.
func ktFromLong(kt string) string {
	if kt == "Long" {
		return ""
	}
	return ".to" + kt + "()"
}

// ktNarrow is the Long a value read as kind holds, checked to fit kt where
// kt is narrower than the wire: a value that does not is a decode failure,
// as sngl:json's is, never a different number.
func ktNarrow(read string, kind ir.ProtoKind, kt, at string) string {
	lo, hi := "", ""
	switch kt {
	case "Int":
		lo, hi = "Int.MIN_VALUE.toLong()", "Int.MAX_VALUE.toLong()"
	case "Short":
		lo, hi = "Short.MIN_VALUE.toLong()", "Short.MAX_VALUE.toLong()"
	case "Byte":
		lo, hi = "Byte.MIN_VALUE.toLong()", "Byte.MAX_VALUE.toLong()"
	case "UInt":
		lo, hi = "0L", "0xffffffffL"
	case "UShort":
		lo, hi = "0L", "0xffffL"
	case "UByte":
		lo, hi = "0L", "0xffL"
	}
	switch {
	case lo == "":
		return read + ktFromLong(kt)
	case kind == ir.ProtoInt32 || kind == ir.ProtoSint32 || kind == ir.ProtoSfixed32:
		if kt == "Int" {
			return read + ".toInt()"
		}
	case kind == ir.ProtoUint32 || kind == ir.ProtoFixed32:
		if kt == "UInt" {
			return read + ".toUInt()"
		}
	}
	return fmt.Sprintf("SnglPbReader.narrow(%s, %s, %s, %s)%s", read, lo, hi, at, ktFromLong(kt))
}

// ktProtoStart is what a field of c holds before any record of it is read.
func ktProtoStart(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecOption:
		return "null"
	case ir.CodecList:
		return "ArrayList()"
	case ir.CodecMap:
		return "LinkedHashMap()"
	case ir.CodecStruct:
		return "read" + protoName(c) + "(SnglPbReader(ByteArray(0)), path)"
	case ir.CodecOneof:
		return enumVariant(c.Type, c.Fields[0].Name)
	}
	return ktProtoZero(c)
}

// ktLocalType is the type of the local a field is read into: a list or a
// map the reader adds to.
func ktLocalType(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecList:
		return "ArrayList<" + IRTypeToKt(c.Elem.Type) + ">"
	case ir.CodecMap:
		return "LinkedHashMap<String, " + IRTypeToKt(c.Elem.Type) + ">"
	}
	return IRTypeToKt(c.Type)
}

func emitKtProtoReader(b *strings.Builder, n *ir.Codec) {
	fmt.Fprintf(b, "\n    fun read%s(r: SnglPbReader, path: String): %s {\n", protoName(n), IRTypeToKt(n.Type))
	for i, f := range n.Fields {
		fmt.Fprintf(b, "        var f%d: %s = %s\n", i, ktLocalType(f.Codec), ktProtoStart(f.Codec))
	}
	b.WriteString("        while (r.more()) {\n            val t = r.tag(path)\n            when ((t ushr 3).toInt()) {\n")
	for i, f := range n.Fields {
		local := "f" + strconv.Itoa(i)
		at := `path + ".` + f.Name + `"`
		c := f.Codec
		switch c.Kind {
		case ir.CodecOneof:
			for _, mem := range c.Fields {
				if mem.Codec == nil {
					continue
				}
				mat := `path + ".` + f.Name + "." + mem.Name + `"`
				fmt.Fprintf(b, "                %d -> { r.want(t, %d, %s, %q); %s = %s(%s) }\n", mem.Number, ktWireType(mem.Proto), mat, mem.Proto, local, enumVariant(c.Type, mem.Name), ktReadValue("r", mem.Proto, mem.Codec, mat))
			}
			continue
		case ir.CodecList:
			if wt := ktWireType(f.Proto); wt != 2 {
				fmt.Fprintf(b, "                %d -> if ((t and 7L).toInt() == 2) { val p = r.sub(); while (p.more()) %s.add(%s) } else { r.want(t, %d, %s, %q); %s.add(%s) }\n",
					f.Number, local, ktReadValue("p", f.Proto, c.Elem, at), wt, at, f.Proto, local, ktReadValue("r", f.Proto, c.Elem, at))
			} else {
				fmt.Fprintf(b, "                %d -> { r.want(t, 2, %s, %q); %s.add(%s) }\n", f.Number, at, f.Proto, local, ktReadValue("r", f.Proto, c.Elem, at))
			}
		case ir.CodecMap:
			fmt.Fprintf(b, "                %d -> {\n                    r.want(t, 2, %s, \"map\")\n                    val e = r.sub()\n                    var k = \"\"\n                    var v: %s = %s\n", f.Number, at, IRTypeToKt(c.Elem.Type), ktProtoStart(c.Elem))
			b.WriteString("                    while (e.more()) {\n                        val et = e.tag(path)\n                        when ((et ushr 3).toInt()) {\n")
			fmt.Fprintf(b, "                            1 -> { e.want(et, 2, %s, \"string\"); k = e.string() }\n", at)
			fmt.Fprintf(b, "                            2 -> { e.want(et, %d, %s, %q); v = %s }\n", ktWireType(f.Proto), at, f.Proto, ktReadValue("e", f.Proto, c.Elem, at))
			fmt.Fprintf(b, "                            else -> e.skip(et, %s)\n                        }\n                    }\n                    %s[k] = v\n                }\n", at, local)
		case ir.CodecOption:
			fmt.Fprintf(b, "                %d -> { r.want(t, %d, %s, %q); %s = %s }\n", f.Number, ktWireType(f.Proto), at, f.Proto, local, ktReadValue("r", f.Proto, c.Elem, at))
		default:
			fmt.Fprintf(b, "                %d -> { r.want(t, %d, %s, %q); %s = %s }\n", f.Number, ktWireType(f.Proto), at, f.Proto, local, ktReadValue("r", f.Proto, c, at))
		}
	}
	b.WriteString("                else -> r.skip(t, path)\n            }\n        }\n")
	args := make([]string, len(n.Fields))
	for i, f := range n.Fields {
		args[i] = fmt.Sprintf("%s = f%d", SafeIdent(f.Name), i)
	}
	fmt.Fprintf(b, "        return %s(%s)\n    }\n", IRTypeToKt(n.Type), strings.Join(args, ", "))
}

// ktReadValue reads one value of kind from r, as the Kotlin type c is.
func ktReadValue(r string, kind ir.ProtoKind, c *ir.Codec, at string) string {
	wrap := func(read string) string {
		return fmt.Sprintf("try { %s } catch (e: SnglPbFail) { throw SnglPbFail(%s + \": want %s\") }", read, at, kind)
	}
	num := func(read string) string {
		if c.Kind != ir.CodecInt {
			return read
		}
		return ktNarrow(read, kind, ktIntType(c.Type), at)
	}
	switch kind {
	case ir.ProtoMessage:
		return fmt.Sprintf("read%s(%s, %s)", protoName(c), wrap(r+".sub()"), at)
	case ir.ProtoEnum:
		return fmt.Sprintf("member%s(%s, %s)", protoName(c), wrap(r+".varint()"), at)
	case ir.ProtoString:
		return wrap(r + ".string()")
	case ir.ProtoBytes:
		return wrap(r + ".base64()")
	case ir.ProtoBool:
		return "(" + wrap(r+".varint()") + " != 0L)"
	case ir.ProtoFloat:
		if IRTypeToKt(c.Type) == "Float" {
			return "Float.fromBits(" + wrap(r+".fixed32()") + ")"
		}
		return "Float.fromBits(" + wrap(r+".fixed32()") + ").toDouble()"
	case ir.ProtoDouble:
		if IRTypeToKt(c.Type) == "Float" {
			return "Double.fromBits(" + wrap(r+".fixed64()") + ").toFloat()"
		}
		return "Double.fromBits(" + wrap(r+".fixed64()") + ")"
	case ir.ProtoInt32:
		return num("(" + wrap(r+".varint()") + ".toInt().toLong())")
	case ir.ProtoUint32:
		return num("(" + wrap(r+".varint()") + " and 0xffffffffL)")
	case ir.ProtoSint32:
		return num("(" + wrap(r+".zigzag32()") + ".toLong())")
	case ir.ProtoSint64:
		return num("(" + wrap(r+".zigzag64()") + ")")
	case ir.ProtoFixed32:
		return num("(" + wrap(r+".fixed32()") + ".toLong() and 0xffffffffL)")
	case ir.ProtoSfixed32:
		return num("(" + wrap(r+".fixed32()") + ".toLong())")
	case ir.ProtoFixed64, ir.ProtoSfixed64:
		return num("(" + wrap(r+".fixed64()") + ")")
	}
	return num("(" + wrap(r+".varint()") + ")")
}
