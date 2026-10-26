package interp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/pkg/go/snglhttp"
	"duckfam.us/sngl/pkg/go/sngljson"
)

// The interpreter answers sngl:json and sngl:http itself, reading the codec
// plan every compiled target renders (ir.PlanCodec) over its own values, so
// a program reads, writes and requests the same things in `sngl test` as on
// a target. Its failures are worded as each target's runtime words them.

func init() {
	intrinsics["http.pathEscape"] = func(args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("http.pathEscape: want one argument, got %d", len(args))
		}
		s, _ := args[0].(string)
		return sngljson.Escape(s), nil
	}
}

// evalCodecCall answers a call of json.decode, json.encode, http.send or
// http.get, which need the type the call reads or writes and not only its
// argument values; handled is false for any other call.
func (env *Env) evalCodecCall(call *ir.Call) (any, bool, error) {
	if call.Func == nil {
		return nil, false, nil
	}
	switch call.Func.Intrinsic {
	case ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic:
	default:
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
	t, format, _ := ir.CodecCall(call)
	t = env.boundType(t)
	switch call.Func.Intrinsic {
	case ir.JSONDecodeIntrinsic:
		text, _ := arg("text").(string)
		plan := ir.PlanCodec(t, format)
		tree, msg := sngljson.Parse(text)
		if msg == "" {
			var v any
			if v, msg = decodeValue(tree, plan.Root, "$"); msg == "" {
				return EnumValue{Type: env.boundType(call.Type), Member: "ok", Payload: v}, true, nil
			}
		}
		return resultFailed(env.boundType(call.Type), "decode", msg, ""), true, nil
	case ir.JSONEncodeIntrinsic:
		var b bytes.Buffer
		encodeValue(&b, arg("value"), ir.PlanCodec(t, format).Root)
		return b.String(), true, nil
	}
	method := "get"
	if call.Func.Intrinsic == ir.HTTPSendIntrinsic {
		method, _ = arg("method").(string)
	}
	url, _ := arg("url").(string)
	if t != nil {
		url = sngljson.WithQuery(url, queryString(arg("query"), ir.PlanCodec(t, format).Root))
	}
	headers := map[string]string{}
	if hs, ok := arg("headers").(map[string]any); ok {
		for k, v := range hs {
			headers[k] = fmt.Sprint(v)
		}
	}
	var body *string
	if s, ok := arg("body").(string); ok {
		body = &s
	}
	resp, fail := snglhttp.Send(method, url, headers, body)
	if fail != nil {
		kind := "transport"
		if fail.Timeout {
			kind = "timeout"
		}
		return resultFailed(call.Type, kind, fail.Message, ""), true, nil
	}
	rt := ir.MemberPayload(call.Type, "ok")
	hs := make(map[string]any, len(resp.Headers))
	for k, v := range resp.Headers {
		hs[k] = v
	}
	r := newStructOf(rt, map[string]any{"status": resp.Status, "headers": hs, "body": resp.Body})
	return EnumValue{Type: call.Type, Member: "ok", Payload: r}, true, nil
}

// resultFailed is an async.Result of type t holding a Failure.
func resultFailed(t *ir.Type, kind, message, code string) EnumValue {
	ft := ir.MemberPayload(t, "failed")
	f := newStructOf(ft, map[string]any{"kind": kind, "message": message, "code": code})
	return EnumValue{Type: t, Member: "failed", Payload: f}
}

// newStructOf is a struct of type t holding vals by field name, its fields in
// the order t declares them.
func newStructOf(t *ir.Type, vals map[string]any) *Struct {
	def, _ := t.Decl.(*ir.StructDef)
	s := NewStruct(def, t)
	fields, _ := ir.FieldsOf(t)
	for _, f := range fields {
		s.Fields = append(s.Fields, Field{Name: f.Name, Value: vals[f.Name]})
	}
	return s
}

func want(path string, c *ir.Codec) string { return path + ": want " + describe(c) }

func describe(c *ir.Codec) string {
	switch c.Kind {
	case ir.CodecBool:
		return "a boolean"
	case ir.CodecInt:
		return "an integer"
	case ir.CodecFloat:
		return "a number"
	case ir.CodecString:
		return "a string"
	case ir.CodecDate:
		return "a date"
	case ir.CodecDatetime:
		return "a datetime"
	case ir.CodecOption:
		return describe(c.Elem)
	case ir.CodecList:
		return "an array"
	case ir.CodecEnum:
		wires := make([]string, len(c.Members))
		for i, m := range c.Members {
			wires[i] = m.WireName()
		}
		return "one of " + strings.Join(wires, ", ")
	}
	return "an object"
}

func decodeValue(v any, c *ir.Codec, path string) (any, string) {
	if c.Kind == ir.CodecOption {
		if v == nil {
			return nil, ""
		}
		return decodeValue(v, c.Elem, path)
	}
	switch c.Kind {
	case ir.CodecBool:
		if b, ok := v.(bool); ok {
			return b, ""
		}
	case ir.CodecInt:
		if n, ok := v.(json.Number); ok {
			if i, ok := sngljson.Integer(string(n)); ok {
				return int(i), ""
			}
		}
	case ir.CodecFloat:
		if n, ok := v.(json.Number); ok {
			if f, err := strconv.ParseFloat(string(n), 64); err == nil {
				return f, ""
			}
		}
	case ir.CodecString:
		if s, ok := v.(string); ok {
			return s, ""
		}
	case ir.CodecDate, ir.CodecDatetime:
		layout := "2006-01-02"
		if c.Kind == ir.CodecDatetime {
			layout = time.RFC3339Nano
		}
		if s, ok := v.(string); ok {
			if _, err := time.Parse(layout, s); err == nil {
				return s, ""
			}
		}
	case ir.CodecEnum:
		if s, ok := v.(string); ok {
			for _, m := range c.Members {
				if m.WireName() == s {
					return m.Name, ""
				}
			}
		}
	case ir.CodecList:
		xs, ok := v.([]any)
		if !ok {
			break
		}
		out := make([]any, len(xs))
		for i, x := range xs {
			el, msg := decodeValue(x, c.Elem, path+"["+strconv.Itoa(i)+"]")
			if msg != "" {
				return nil, msg
			}
			out[i] = el
		}
		return out, ""
	case ir.CodecMap:
		obj, ok := v.(map[string]any)
		if !ok {
			break
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		out := make(map[string]any, len(obj))
		for _, k := range keys {
			el, msg := decodeValue(obj[k], c.Elem, path+"."+k)
			if msg != "" {
				return nil, msg
			}
			out[k] = el
		}
		return out, ""
	case ir.CodecStruct:
		obj, ok := v.(map[string]any)
		if !ok {
			break
		}
		vals := map[string]any{}
		for _, f := range c.Fields {
			x, present := obj[f.Wire]
			if !present && f.Codec.Kind != ir.CodecOption {
				return nil, path + "." + f.Wire + ": missing"
			}
			el, msg := decodeValue(x, f.Codec, path+"."+f.Wire)
			if msg != "" {
				return nil, msg
			}
			vals[f.Name] = el
		}
		return newStructOf(c.Type, vals), ""
	}
	return nil, want(path, c)
}

func encodeValue(b *bytes.Buffer, v any, c *ir.Codec) {
	switch c.Kind {
	case ir.CodecOption:
		if v == nil {
			b.WriteString("null")
			return
		}
		encodeValue(b, v, c.Elem)
	case ir.CodecBool:
		b.WriteString(strconv.FormatBool(v == true))
	case ir.CodecInt:
		b.WriteString(strconv.Itoa(ToInt(v)))
	case ir.CodecFloat:
		b.WriteString(sngljson.Number(toFloat(v)))
	case ir.CodecString, ir.CodecDate, ir.CodecDatetime:
		s, _ := v.(string)
		sngljson.Quote(b, s)
	case ir.CodecEnum:
		sngljson.Quote(b, enumWire(c, v))
	case ir.CodecList:
		xs, _ := v.([]any)
		b.WriteByte('[')
		for i, x := range xs {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeValue(b, x, c.Elem)
		}
		b.WriteByte(']')
	case ir.CodecMap:
		m, _ := v.(map[string]any)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			sngljson.Quote(b, k)
			b.WriteByte(':')
			encodeValue(b, m[k], c.Elem)
		}
		b.WriteByte('}')
	case ir.CodecStruct:
		s, _ := v.(*Struct)
		b.WriteByte('{')
		first := true
		for _, f := range c.Fields {
			fv := structField(s, f.Name)
			if f.Codec.Kind == ir.CodecOption && fv == nil {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			sngljson.Quote(b, f.Wire)
			b.WriteByte(':')
			encodeValue(b, fv, f.Codec)
		}
		b.WriteByte('}')
	}
}

func structField(s *Struct, name string) any {
	if s == nil {
		return nil
	}
	for _, f := range s.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return nil
}

func enumWire(c *ir.Codec, v any) string {
	name := fmt.Sprint(v)
	for _, m := range c.Members {
		if m.Name == name {
			return m.WireName()
		}
	}
	return name
}

// queryString writes a query struct's fields as sngljson.Query does.
func queryString(q any, c *ir.Codec) string {
	s, _ := q.(*Struct)
	if s == nil || c.Kind != ir.CodecStruct {
		return ""
	}
	var parts []string
	for _, f := range c.Fields {
		for _, v := range queryValues(structField(s, f.Name), f.Codec) {
			parts = append(parts, sngljson.Escape(f.Wire)+"="+sngljson.Escape(v))
		}
	}
	return strings.Join(parts, "&")
}

func queryValues(v any, c *ir.Codec) []string {
	switch c.Kind {
	case ir.CodecOption:
		if v == nil {
			return nil
		}
		return queryValues(v, c.Elem)
	case ir.CodecList:
		xs, _ := v.([]any)
		var out []string
		for _, x := range xs {
			out = append(out, queryValues(x, c.Elem)...)
		}
		return out
	case ir.CodecBool:
		return []string{strconv.FormatBool(v == true)}
	case ir.CodecInt:
		return []string{strconv.Itoa(ToInt(v))}
	case ir.CodecFloat:
		return []string{sngljson.Number(toFloat(v))}
	case ir.CodecEnum:
		return []string{enumWire(c, v)}
	}
	s, _ := v.(string)
	return []string{s}
}
