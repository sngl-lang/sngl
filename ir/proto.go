package ir

import "fmt"

// GRPCCallIntrinsic is sngl:grpc's call: a request message written as
// protobuf, and a response read back as one.
const GRPCCallIntrinsic = "grpc.call"

// GRPCStreamIntrinsic is sngl:grpc's stream: a list of request messages, and
// each response handed to a function as it comes.
const GRPCStreamIntrinsic = "grpc.stream"

// IsGRPC reports whether f is one of sngl:grpc's calls.
func IsGRPC(f *Func) bool {
	return f != nil && (f.Intrinsic == GRPCCallIntrinsic || f.Intrinsic == GRPCStreamIntrinsic)
}

// ProtoKind is how a protobuf field is written on the wire, as `.proto`
// spells its type: `int32`, `sint64`, `string`, `enum`, `message`.
type ProtoKind string

const (
	ProtoInt32    ProtoKind = "int32"
	ProtoInt64    ProtoKind = "int64"
	ProtoUint32   ProtoKind = "uint32"
	ProtoUint64   ProtoKind = "uint64"
	ProtoSint32   ProtoKind = "sint32"
	ProtoSint64   ProtoKind = "sint64"
	ProtoFixed32  ProtoKind = "fixed32"
	ProtoFixed64  ProtoKind = "fixed64"
	ProtoSfixed32 ProtoKind = "sfixed32"
	ProtoSfixed64 ProtoKind = "sfixed64"
	ProtoFloat    ProtoKind = "float"
	ProtoDouble   ProtoKind = "double"
	ProtoBool     ProtoKind = "bool"
	ProtoString   ProtoKind = "string"
	ProtoBytes    ProtoKind = "bytes"
	ProtoEnum     ProtoKind = "enum"
	ProtoMessage  ProtoKind = "message"
	// ProtoOneof is a field holding a payload enum: no record of its own,
	// but one of whichever member it holds.
	ProtoOneof ProtoKind = "oneof"
)

// ProtoField is what `#[grpc.field]` says of a struct field: its number, and
// its kind where the mark names one (empty is the kind its type infers).
type ProtoField struct {
	Number int       `json:",omitempty"`
	Kind   ProtoKind `json:",omitempty"`
}

// ProtoNumber is the protobuf number of the member at index i of its enum:
// the one `#[grpc.value]` gave it, or its place.
func (m *EnumMember) ProtoNumber(i int) int {
	if m.Proto != nil {
		return *m.Proto
	}
	return i
}

// ProtoCall reports the request and response types of a call of grpc.call
// or grpc.stream.
func ProtoCall(c *Call) (req, resp *Type, ok bool) {
	if c == nil || !IsGRPC(c.Func) || PayloadEnum(c.Type) == nil {
		return nil, nil, false
	}
	if c.Func.Intrinsic == GRPCStreamIntrinsic {
		if a := c.ArgFor("requests"); a != nil {
			if t := codecUnwrap(a.ExprType()); t != nil && t.Kind == TypeList {
				req = t.Elems[0]
			}
		}
		if a := c.ArgFor("each"); a != nil {
			if t := codecUnwrap(a.ExprType()); t != nil && t.Kind == TypeFunc && t.Sig != nil && len(t.Sig.Params) == 1 {
				resp = t.Sig.Params[0].Type
			}
		}
		return req, resp, true
	}
	if a := c.ArgFor("request"); a != nil {
		req = a.ExprType()
	}
	return req, MemberPayload(c.Type, "ok"), true
}

// protoKindOf is the kind a field of type t is written as, given the kind its
// mark names: what an element of a list or a map's value is, a list being a
// repeated field of it.
func protoKindOf(t *Type, marked ProtoKind) ProtoKind {
	t = codecUnwrap(t)
	for t != nil && (t.Kind == TypeOption || t.Kind == TypeList) {
		t = codecUnwrap(t.Elems[0])
	}
	if t != nil && t.Kind == TypeMap {
		t = codecUnwrap(t.Elems[1])
	}
	if marked != "" {
		return marked
	}
	switch {
	case t == nil:
		return ""
	case t.Kind == TypeBool:
		return ProtoBool
	case t.Kind == TypeInt && t.Bits > 0 && t.Bits <= 32 && t.Unsigned:
		return ProtoUint32
	case t.Kind == TypeInt && t.Bits > 0 && t.Bits <= 32:
		return ProtoInt32
	case t.Kind == TypeInt && t.Unsigned:
		return ProtoUint64
	case t.Kind == TypeInt:
		return ProtoInt64
	case t.Kind == TypeFloat:
		return ProtoDouble
	case t.Kind == TypeString:
		return ProtoString
	case t.Kind == TypeEnum && PayloadEnum(t) != nil:
		return ProtoOneof
	case t.Kind == TypeEnum:
		return ProtoEnum
	case t.Kind == TypeStruct:
		return ProtoMessage
	}
	return ""
}

// protoField is f's declared mark, read off the declaration as wireName reads
// its name.
func protoField(t *Type, f *StructField) ProtoField {
	if sd, ok := t.Decl.(*StructDef); ok {
		for _, df := range sd.Fields {
			if df.Name == f.Name {
				return df.Proto
			}
		}
	}
	return f.Proto
}

func protoMessage(t *Type) string {
	if sd, ok := t.Decl.(*StructDef); ok {
		if sd.InstanceOf != nil && sd.Proto == "" {
			return sd.InstanceOf.Proto
		}
		return sd.Proto
	}
	return ""
}

// protoProblem says why a value of t has no protobuf form, or returns "".
// A message is a struct whose every field is numbered, once each, with a
// kind its type can be written as; a field is a scalar, an enum, a message,
// an option of one, a list of one -- a repeated field -- or a map of one
// keyed by string.
func protoProblem(t *Type, seen map[string]bool, open bool) string {
	t = codecUnwrap(t)
	if t == nil {
		return "a value of no known type"
	}
	if t.Kind == TypeTypeParam {
		if open {
			return ""
		}
		return fmt.Sprintf("%s, a type parameter: the type has to be known where the call is", t)
	}
	if t.Kind != TypeStruct {
		return fmt.Sprintf("%s, which is no message", t)
	}
	sd, ok := t.Decl.(*StructDef)
	if !ok || sd.Builtin != "" || sd.Intrinsic != "" || IsDateStruct(t) || IsDateTimeStruct(t) || IsTimeStruct(t) || IsColorStruct(t) {
		return fmt.Sprintf("%s, which has no protobuf form", t)
	}
	key := codecKey(t)
	if seen[key] {
		return ""
	}
	seen[key] = true
	fields, _ := FieldsOf(t)
	numbers := map[int]string{}
	for _, f := range fields {
		pf := protoField(t, f)
		if e := codecUnwrap(f.Type); PayloadEnum(e) != nil {
			if pf.Number != 0 {
				return fmt.Sprintf("field %s of %s, a oneof, which #[grpc.field] numbers: its members carry the numbers", f.Name, sd.DisplayName())
			}
			if p := oneofProblem(e, numbers, seen, open); p != "" {
				return fmt.Sprintf("field %s of %s: %s", f.Name, sd.DisplayName(), p)
			}
			continue
		}
		if pf.Number <= 0 {
			return fmt.Sprintf("field %s of %s, which no #[grpc.field] numbers", f.Name, sd.DisplayName())
		}
		if prev, dup := numbers[pf.Number]; dup {
			return fmt.Sprintf("%s, whose fields %s and %s are both numbered %d", sd.DisplayName(), prev, f.Name, pf.Number)
		}
		numbers[pf.Number] = f.Name
		if p := protoFieldProblem(f.Type, pf.Kind, seen, open); p != "" {
			return fmt.Sprintf("field %s of %s: %s", f.Name, sd.DisplayName(), p)
		}
	}
	return ""
}

// oneofProblem says why e, a payload enum, is no oneof: its first member is
// the oneof set to none of them and carries nothing, and every member
// carrying a payload is numbered, apart from every other field of the
// message, with a payload a oneof can hold.
func oneofProblem(e *Type, numbers map[int]string, seen map[string]bool, open bool) string {
	ed := e.Decl.(*EnumDef)
	if ed.Members[0].Payload != nil {
		return fmt.Sprintf("%s, a oneof whose first member %s carries a payload: the first is the oneof set to none, and carries nothing", e, ed.Members[0].Name)
	}
	for _, m := range ed.Members {
		pt := MemberPayload(e, m.Name)
		if pt == nil {
			continue
		}
		if m.Oneof.Number <= 0 {
			return fmt.Sprintf("member %s of %s, which no #[grpc.field] numbers", m.Name, e)
		}
		if prev, dup := numbers[m.Oneof.Number]; dup {
			return fmt.Sprintf("%s and member %s of %s, both numbered %d", prev, m.Name, e, m.Oneof.Number)
		}
		numbers[m.Oneof.Number] = m.Name
		if u := codecUnwrap(pt); u != nil && (u.Kind == TypeOption || u.Kind == TypeList || u.Kind == TypeMap || PayloadEnum(u) != nil) {
			return fmt.Sprintf("member %s of %s carries %s, which a oneof cannot hold", m.Name, e, pt)
		}
		if p := protoFieldProblem(pt, m.Oneof.Kind, seen, open); p != "" {
			return fmt.Sprintf("member %s of %s: %s", m.Name, e, p)
		}
	}
	return ""
}

func protoFieldProblem(t *Type, marked ProtoKind, seen map[string]bool, open bool) string {
	t = codecUnwrap(t)
	if t == nil {
		return "a value of no known type"
	}
	switch t.Kind {
	case TypeOption, TypeList:
		e := codecUnwrap(t.Elems[0])
		if e != nil && (e.Kind == TypeOption || e.Kind == TypeList || e.Kind == TypeMap) {
			return fmt.Sprintf("%s, which protobuf has no field for", t)
		}
		return protoFieldProblem(e, marked, seen, open)
	case TypeMap:
		if k := codecUnwrap(t.Elems[0]); k == nil || k.Kind != TypeString {
			return fmt.Sprintf("%s, whose keys are not strings", t)
		}
		v := codecUnwrap(t.Elems[1])
		if v != nil && (v.Kind == TypeOption || v.Kind == TypeList || v.Kind == TypeMap) {
			return fmt.Sprintf("%s, which protobuf has no field for", t)
		}
		return protoFieldProblem(v, marked, seen, open)
	case TypeBool, TypeString, TypeEnum:
		want := protoKindOf(t, "")
		if marked != "" && marked != want && !(t.Kind == TypeString && marked == ProtoBytes) {
			return fmt.Sprintf("a %s, which is not written as %s", t, marked)
		}
		if PayloadEnum(t) != nil {
			return fmt.Sprintf("%s, an enum whose members carry payloads, which is a oneof only as a message's own field", t)
		}
		if t.Kind == TypeEnum {
			return enumProblem(t)
		}
		return ""
	case TypeInt:
		switch marked {
		case "", ProtoInt32, ProtoInt64, ProtoUint32, ProtoUint64, ProtoSint32, ProtoSint64, ProtoFixed32, ProtoFixed64, ProtoSfixed32, ProtoSfixed64:
			return ""
		}
		return fmt.Sprintf("an int, which is not written as %s", marked)
	case TypeFloat:
		if marked != "" && marked != ProtoFloat && marked != ProtoDouble {
			return fmt.Sprintf("a float, which is not written as %s", marked)
		}
		return ""
	case TypeStruct, TypeTypeParam:
		if marked != "" {
			return fmt.Sprintf("a %s, which is not written as %s", t, marked)
		}
		return protoProblem(t, seen, open)
	}
	return fmt.Sprintf("%s, which has no protobuf form", t)
}

// CodecUses are the types a codec call reads or writes, each in its format:
// one for sngl:json and sngl:http, a request and a response for grpc.call.
func CodecUses(c *Call) []CodecUse {
	if req, resp, ok := ProtoCall(c); ok {
		var uses []CodecUse
		for _, t := range []*Type{req, resp} {
			if t != nil {
				uses = append(uses, CodecUse{Type: t, Format: CodecProto, Intrinsic: c.Func.Intrinsic})
			}
		}
		return uses
	}
	if t, format, ok := CodecCall(c); ok && t != nil {
		return []CodecUse{{Type: t, Format: format, Intrinsic: c.Func.Intrinsic}}
	}
	return nil
}
