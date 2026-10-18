package codegen

import "duckfam.us/sngl/ir"

// The codec intrinsics -- sngl:json's decode and encode, sngl:http's send and
// get -- are answered by each language's own runtime, handed a descriptor of
// the type (ir.PlanCodec). What the call answers is an async.Result, built in
// the program's own types, so each language spells it by translating the IR
// these helpers build around the host names its runtime handed back: the
// language's own struct and enum spelling then names the Result, the Failure
// and the Response, whichever package declared them and however a target
// copied them per instantiation.

// IsCodecCall reports whether c is a call of a codec intrinsic.
func IsCodecCall(c *ir.Call) bool {
	if c == nil || c.Func == nil {
		return false
	}
	switch c.Func.Intrinsic {
	case ir.JSONDecodeIntrinsic, ir.JSONEncodeIntrinsic, ir.HTTPSendIntrinsic, ir.HTTPGetIntrinsic:
		return true
	}
	return false
}

// HostValue is a name the emitted code binds itself, as IR: a language
// renders it as written.
func HostValue(name string, t *ir.Type) *ir.Ident {
	return &ir.Ident{Name: name, Type: t, Sym: &Receiver{Name: name, Type: t}, Synthesized: true}
}

// ResultOK is the async.Result of type t holding v.
func ResultOK(t *ir.Type, v ir.Expr) ir.Expr {
	return &ir.StructLit{Type: t, Fields: []ir.FieldInit{{Name: "ok", Value: v}}}
}

// ResultFailed is the async.Result of type t holding a Failure of kind (a
// FailureKind member's name) and message, with no code.
func ResultFailed(t *ir.Type, kind string, message ir.Expr) ir.Expr {
	ft := ir.MemberPayload(t, "failed")
	f := StructOf(ft, map[string]ir.Expr{
		"message": message,
		"code":    &ir.Literal{Type: ir.TypString, Value: ""},
	})
	for i, fi := range f.Fields {
		if fi.Name == "kind" {
			f.Fields[i].Value = &ir.Ident{Name: kind, Member: kind, Type: fieldType(ft, "kind")}
		}
	}
	return &ir.StructLit{Type: t, Fields: []ir.FieldInit{{Name: "failed", Value: f}}}
}

// StructOf is a literal of struct type t setting each field from vals, in the
// order t declares them.
func StructOf(t *ir.Type, vals map[string]ir.Expr) *ir.StructLit {
	def, _ := t.Decl.(*ir.StructDef)
	lit := &ir.StructLit{Type: t, Def: def}
	fields, _ := ir.FieldsOf(t)
	for _, f := range fields {
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: f.Name, Value: vals[f.Name]})
	}
	return lit
}

func fieldType(t *ir.Type, name string) *ir.Type {
	fields, _ := ir.FieldsOf(t)
	for _, f := range fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

// CodecResponse is the Response a request's Result of type t holds, built
// from what the runtime handed back.
func CodecResponse(t *ir.Type, status, headers, body ir.Expr) ir.Expr {
	return StructOf(ir.MemberPayload(t, "ok"), map[string]ir.Expr{"status": status, "headers": headers, "body": body})
}

// ResponseFieldTypes are the types of the Response a Result of type t holds:
// its status, headers and body.
func ResponseFieldTypes(t *ir.Type) (status, headers, body *ir.Type) {
	rt := ir.MemberPayload(t, "ok")
	return fieldType(rt, "status"), fieldType(rt, "headers"), fieldType(rt, "body")
}
