package interp

import (
	"encoding/base64"
	"fmt"
	"math"
	"slices"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"

	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/snglasync"
	"duckfam.us/sngl/pkg/go/snglgrpc"
)

// evalGRPCCall answers grpc.call: the request written as protobuf from the
// call's plan, sent through snglgrpc, and the response read back by the
// response type's. handled is false for any other call.
func (env *Env) evalGRPCCall(call *ir.Call) (any, bool, error) {
	req, resp, ok := ir.ProtoCall(call)
	if !ok {
		return nil, false, nil
	}
	values, err := env.evalCallArgs(call.Args)
	if err != nil {
		return nil, true, err
	}
	args, err := env.boundArgValues(call.Func, call.Args, values)
	if err != nil {
		return nil, true, err
	}
	arg := func(name string) any {
		for i, p := range call.Func.Params {
			if p.Name == name && i < len(args) {
				return args[i]
			}
		}
		return nil
	}
	rt := env.boundType(call.Type)
	server, _ := arg("server").(string)
	method, _ := arg("method").(string)
	if call.Func.Intrinsic == ir.GRPCStreamIntrinsic {
		return env.grpcStream(rt, server, method, arg("requests"), arg("each"), arg("life"), req, resp)
	}
	body := protoAppendMessage(nil, arg("request"), ir.PlanCodec(env.boundType(req), ir.CodecProto).Root)
	got, code, msg := snglgrpc.Invoke(snglasync.Context(heldID(arg("life"))), server, method, body)
	if code != 0 {
		return resultFailed(rt, "server", msg, strconv.Itoa(code)), true, nil
	}
	v, problem := protoReadMessage(got, ir.PlanCodec(env.boundType(resp), ir.CodecProto).Root, "$")
	if problem != "" {
		return resultFailed(rt, "decode", problem, ""), true, nil
	}
	return EnumValue{Type: rt, Member: "ok", Payload: v}, true, nil
}

// grpcStream answers grpc.stream: each request written as its queue is sent
// it, and each response read back and handed to each before the next is
// read.
func (env *Env) grpcStream(rt *ir.Type, server, method string, requests, each, life any, req, resp *ir.Type) (any, bool, error) {
	fn, ok := each.(*LambdaValue)
	if !ok {
		return nil, true, fmt.Errorf("grpc.stream: each is a %T, not a function", each)
	}
	reqPlan := ir.PlanCodec(env.boundType(req), ir.CodecProto).Root
	respPlan := ir.PlanCodec(env.boundType(resp), ir.CodecProto).Root
	write := func(x any) []byte { return protoAppendMessage(nil, x, reqPlan) }
	n := 0
	var problem string
	var failed error
	code, msg := snglgrpc.Stream(snglasync.Context(heldID(life)), server, method, heldID(requests), write, func(m []byte) bool {
		v, p := protoReadMessage(m, respPlan, "$")
		if p != "" {
			problem = p
			return false
		}
		n++
		if _, err := fn.Call([]any{v}); err != nil {
			failed = err
			return false
		}
		return true
	})
	switch {
	case failed != nil:
		return nil, true, failed
	case problem != "":
		return resultFailed(rt, "decode", problem, ""), true, nil
	case code != 0:
		return resultFailed(rt, "server", msg, strconv.Itoa(code)), true, nil
	}
	return EnumValue{Type: rt, Member: "ok", Payload: n}, true, nil
}

// protoAppendMessage appends the fields of s, a struct of c, to b. A field
// proto3 gives no presence is left out where it holds its zero, as every
// protobuf writer leaves it.
func protoAppendMessage(b []byte, s any, c *ir.Codec) []byte {
	st, _ := s.(*Struct)
	for _, f := range c.Fields {
		b = protoAppendField(b, f, structField(st, f.Name))
	}
	return b
}

func protoAppendField(b []byte, f ir.CodecField, v any) []byte {
	num := protowire.Number(f.Number)
	switch c := f.Codec; c.Kind {
	case ir.CodecOption:
		if v == nil {
			return b
		}
		return protoAppendValue(b, num, f.Proto, v, c.Elem)
	case ir.CodecList:
		xs, _ := v.([]any)
		if len(xs) == 0 {
			return b
		}
		if protoPacked(f.Proto) {
			var packed []byte
			for _, x := range xs {
				packed = protoAppendScalar(packed, f.Proto, x, c.Elem)
			}
			b = protowire.AppendTag(b, num, protowire.BytesType)
			return protowire.AppendBytes(b, packed)
		}
		for _, x := range xs {
			b = protoAppendValue(b, num, f.Proto, x, c.Elem)
		}
		return b
	case ir.CodecMap:
		m, _ := v.(map[string]any)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			var entry []byte
			entry = protowire.AppendTag(entry, 1, protowire.BytesType)
			entry = protowire.AppendString(entry, k)
			entry = protoAppendValue(entry, 2, f.Proto, m[k], c.Elem)
			b = protowire.AppendTag(b, num, protowire.BytesType)
			b = protowire.AppendBytes(b, entry)
		}
		return b
	case ir.CodecStruct:
		return protoAppendValue(b, num, f.Proto, v, c)
	case ir.CodecOneof:
		ev, _ := v.(EnumValue)
		for _, m := range c.Fields {
			if m.Name == ev.Member && m.Codec != nil {
				return protoAppendValue(b, protowire.Number(m.Number), m.Proto, ev.Payload, m.Codec)
			}
		}
		return b
	}
	if protoZero(v, f.Codec) {
		return b
	}
	return protoAppendValue(b, num, f.Proto, v, f.Codec)
}

// protoAppendValue appends one record: a tag, and v written as kind.
func protoAppendValue(b []byte, num protowire.Number, kind ir.ProtoKind, v any, c *ir.Codec) []byte {
	b = protowire.AppendTag(b, num, protoWireType(kind))
	switch kind {
	case ir.ProtoMessage:
		return protowire.AppendBytes(b, protoAppendMessage(nil, v, c))
	case ir.ProtoString:
		s, _ := v.(string)
		return protowire.AppendString(b, s)
	case ir.ProtoBytes:
		s, _ := v.(string)
		raw, _ := base64.StdEncoding.DecodeString(s)
		return protowire.AppendBytes(b, raw)
	}
	return protoAppendScalar(b, kind, v, c)
}

// protoAppendScalar appends v written as kind, with no tag: a record's
// value, or one of a packed field's.
func protoAppendScalar(b []byte, kind ir.ProtoKind, v any, c *ir.Codec) []byte {
	switch kind {
	case ir.ProtoBool:
		return protowire.AppendVarint(b, protowire.EncodeBool(v == true))
	case ir.ProtoEnum:
		return protowire.AppendVarint(b, uint64(int64(protoEnumNumber(c, v))))
	case ir.ProtoFloat:
		return protowire.AppendFixed32(b, math.Float32bits(float32(toFloat(v))))
	case ir.ProtoDouble:
		return protowire.AppendFixed64(b, math.Float64bits(toFloat(v)))
	}
	n := int64(ToInt(v))
	switch kind {
	case ir.ProtoInt32:
		return protowire.AppendVarint(b, uint64(int64(int32(n))))
	case ir.ProtoUint32:
		return protowire.AppendVarint(b, uint64(uint32(n)))
	case ir.ProtoSint32:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(int64(int32(n))))
	case ir.ProtoSint64:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(n))
	case ir.ProtoFixed32, ir.ProtoSfixed32:
		return protowire.AppendFixed32(b, uint32(n))
	case ir.ProtoFixed64, ir.ProtoSfixed64:
		return protowire.AppendFixed64(b, uint64(n))
	}
	return protowire.AppendVarint(b, uint64(n))
}

func protoWireType(kind ir.ProtoKind) protowire.Type {
	switch kind {
	case ir.ProtoMessage, ir.ProtoString, ir.ProtoBytes:
		return protowire.BytesType
	case ir.ProtoFloat, ir.ProtoFixed32, ir.ProtoSfixed32:
		return protowire.Fixed32Type
	case ir.ProtoDouble, ir.ProtoFixed64, ir.ProtoSfixed64:
		return protowire.Fixed64Type
	}
	return protowire.VarintType
}

// protoPacked reports whether a repeated field of kind is written packed, as
// proto3 writes every repeated scalar.
func protoPacked(kind ir.ProtoKind) bool {
	return protoWireType(kind) != protowire.BytesType
}

func protoZero(v any, c *ir.Codec) bool {
	switch c.Kind {
	case ir.CodecBool:
		return v != true
	case ir.CodecInt:
		return ToInt(v) == 0
	case ir.CodecFloat:
		f := toFloat(v)
		return f == 0 && !math.Signbit(f)
	case ir.CodecString:
		return v == ""
	case ir.CodecEnum:
		return protoEnumNumber(c, v) == 0
	}
	return false
}

func protoEnumNumber(c *ir.Codec, v any) int {
	name := fmt.Sprint(v)
	for i, m := range c.Members {
		if m.Name == name {
			return m.ProtoNumber(i)
		}
	}
	return 0
}

// protoReadMessage reads b as a message of c: each field it names from its
// last record, a repeated one from every record, packed or not, and one it
// does not name skipped. A field with no record holds its zero.
func protoReadMessage(b []byte, c *ir.Codec, path string) (any, string) {
	vals := map[string]any{}
	byNumber := map[protowire.Number]ir.CodecField{}
	members := map[protowire.Number]ir.CodecField{}
	for _, f := range c.Fields {
		if f.Codec.Kind != ir.CodecOneof {
			byNumber[protowire.Number(f.Number)] = f
			continue
		}
		for _, m := range f.Codec.Fields {
			if m.Codec != nil {
				byNumber[protowire.Number(m.Number)] = f
				members[protowire.Number(m.Number)] = m
			}
		}
	}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, path + ": not protobuf"
		}
		b = b[n:]
		f, known := byNumber[num]
		if !known {
			n = protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, path + ": not protobuf"
			}
			b = b[n:]
			continue
		}
		at := path + "." + f.Name
		if m, ok := members[num]; ok {
			v, n, problem := protoReadScalar(b, typ, m.Proto, m.Codec, at+"."+m.Name)
			if problem != "" {
				return nil, problem
			}
			vals[f.Name] = EnumValue{Type: f.Codec.Type, Member: m.Name, Payload: v}
			b = b[n:]
			continue
		}
		n, problem := protoReadField(b, typ, f, vals, at)
		if problem != "" {
			return nil, problem
		}
		b = b[n:]
	}
	for _, f := range c.Fields {
		if _, set := vals[f.Name]; !set {
			vals[f.Name] = protoZeroValue(f.Codec)
		}
	}
	return newStructOf(c.Type, vals), ""
}

// protoReadField reads one record of f into vals, and answers how much of b
// it took.
func protoReadField(b []byte, typ protowire.Type, f ir.CodecField, vals map[string]any, path string) (int, string) {
	c := f.Codec
	switch c.Kind {
	case ir.CodecList:
		xs, _ := vals[f.Name].([]any)
		if typ == protowire.BytesType && protoPacked(f.Proto) {
			packed, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return 0, path + ": not protobuf"
			}
			for len(packed) > 0 {
				v, m, problem := protoReadScalar(packed, protoWireType(f.Proto), f.Proto, c.Elem, path)
				if problem != "" {
					return 0, problem
				}
				xs = append(xs, v)
				packed = packed[m:]
			}
			vals[f.Name] = xs
			return n, ""
		}
		v, n, problem := protoReadScalar(b, typ, f.Proto, c.Elem, path)
		if problem != "" {
			return 0, problem
		}
		vals[f.Name] = append(xs, v)
		return n, ""
	case ir.CodecMap:
		entry, n := protowire.ConsumeBytes(b)
		if n < 0 || typ != protowire.BytesType {
			return 0, path + ": not protobuf"
		}
		m, _ := vals[f.Name].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		key, val := "", protoZeroValue(c.Elem)
		for len(entry) > 0 {
			num, et, k := protowire.ConsumeTag(entry)
			if k < 0 {
				return 0, path + ": not protobuf"
			}
			entry = entry[k:]
			switch num {
			case 1:
				s, k := protowire.ConsumeString(entry)
				if k < 0 {
					return 0, path + ": not protobuf"
				}
				key, entry = s, entry[k:]
			case 2:
				v, k, problem := protoReadScalar(entry, et, f.Proto, c.Elem, path+"."+key)
				if problem != "" {
					return 0, problem
				}
				val, entry = v, entry[k:]
			default:
				k = protowire.ConsumeFieldValue(num, et, entry)
				if k < 0 {
					return 0, path + ": not protobuf"
				}
				entry = entry[k:]
			}
		}
		m[key] = val
		vals[f.Name] = m
		return n, ""
	case ir.CodecOption:
		c = c.Elem
	}
	v, n, problem := protoReadScalar(b, typ, f.Proto, c, path)
	if problem != "" {
		return 0, problem
	}
	vals[f.Name] = v
	return n, ""
}

// protoReadScalar reads one value of kind, written as wire type typ.
func protoReadScalar(b []byte, typ protowire.Type, kind ir.ProtoKind, c *ir.Codec, path string) (any, int, string) {
	bad := func() (any, int, string) { return nil, 0, path + ": want " + string(kind) }
	if typ != protoWireType(kind) {
		return bad()
	}
	switch typ {
	case protowire.BytesType:
		raw, n := protowire.ConsumeBytes(b)
		if n < 0 {
			return bad()
		}
		switch kind {
		case ir.ProtoMessage:
			v, problem := protoReadMessage(raw, c, path)
			return v, n, problem
		case ir.ProtoBytes:
			return base64.StdEncoding.EncodeToString(raw), n, ""
		}
		return string(raw), n, ""
	case protowire.Fixed32Type:
		x, n := protowire.ConsumeFixed32(b)
		if n < 0 {
			return bad()
		}
		switch kind {
		case ir.ProtoFloat:
			return float64(math.Float32frombits(x)), n, ""
		case ir.ProtoSfixed32:
			return int(int32(x)), n, ""
		}
		return int(x), n, ""
	case protowire.Fixed64Type:
		x, n := protowire.ConsumeFixed64(b)
		if n < 0 {
			return bad()
		}
		if kind == ir.ProtoDouble {
			return math.Float64frombits(x), n, ""
		}
		return int(int64(x)), n, ""
	}
	x, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return bad()
	}
	switch kind {
	case ir.ProtoBool:
		return x != 0, n, ""
	case ir.ProtoEnum:
		num := int(int32(x))
		for i, m := range c.Members {
			if m.ProtoNumber(i) == num {
				return m.Name, n, ""
			}
		}
		return nil, 0, fmt.Sprintf("%s: no member numbered %d", path, num)
	case ir.ProtoInt32:
		return int(int32(x)), n, ""
	case ir.ProtoUint32:
		return int(uint32(x)), n, ""
	case ir.ProtoSint32, ir.ProtoSint64:
		return int(protowire.DecodeZigZag(x)), n, ""
	}
	return int(int64(x)), n, ""
}

// protoZeroValue is what a field with no record holds: proto3's zero for a
// scalar, the member numbered 0, nothing for an option, and a message of
// zeros for a struct.
func protoZeroValue(c *ir.Codec) any {
	switch c.Kind {
	case ir.CodecBool:
		return false
	case ir.CodecInt:
		return 0
	case ir.CodecFloat:
		return 0.0
	case ir.CodecString:
		return ""
	case ir.CodecEnum:
		for i, m := range c.Members {
			if m.ProtoNumber(i) == 0 {
				return m.Name
			}
		}
		if len(c.Members) > 0 {
			return c.Members[0].Name
		}
	case ir.CodecList:
		return []any{}
	case ir.CodecMap:
		return map[string]any{}
	case ir.CodecStruct:
		vals := map[string]any{}
		for _, f := range c.Fields {
			vals[f.Name] = protoZeroValue(f.Codec)
		}
		return newStructOf(c.Type, vals)
	case ir.CodecOneof:
		return EnumValue{Type: c.Type, Member: c.Fields[0].Name}
	}
	return nil
}
