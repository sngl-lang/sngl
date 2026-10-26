package ir

import (
	"fmt"
	"strings"
)

// A codec is how a value of a declared type is written in a format the
// program does not spell itself: JSON for sngl:json, a URL's query for
// sngl:http. The type is its own schema -- a struct's fields are an object's
// keys, an option a key that may be missing -- so what a backend needs is the
// type and the names its marks give each field on the wire, and every
// language renders the one CodecPlan below into a descriptor its own runtime
// reads.

// The ids of the intrinsics that encode or decode a declared type.
const (
	JSONDecodeIntrinsic = "json.decode"
	JSONEncodeIntrinsic = "json.encode"
	HTTPSendIntrinsic   = "http.send"
	HTTPGetIntrinsic    = "http.get"
)

// CodecFormat is which wire format a codec writes.
type CodecFormat int

const (
	// CodecJSON is JSON text: sngl:json's decode and encode.
	CodecJSON CodecFormat = iota
	// CodecQuery is a URL's query: the struct sngl:http's send and get take.
	CodecQuery
)

// CodecCall reports the type a call to a codec intrinsic reads or writes, and
// in which format. json.decode's is what its result holds, json.encode's its
// argument, and a request's the query it is handed.
func CodecCall(c *Call) (*Type, CodecFormat, bool) {
	if c == nil || c.Func == nil {
		return nil, 0, false
	}
	switch c.Func.Intrinsic {
	case JSONDecodeIntrinsic:
		if PayloadEnum(c.Type) == nil {
			return nil, 0, false
		}
		return MemberPayload(c.Type, "ok"), CodecJSON, true
	case JSONEncodeIntrinsic:
		if len(c.Args) == 0 {
			return nil, 0, false
		}
		return c.Args[0].Value.ExprType(), CodecJSON, true
	case HTTPSendIntrinsic, HTTPGetIntrinsic:
		if a := c.ArgFor("query"); a != nil {
			return a.ExprType(), CodecQuery, true
		}
		return nil, CodecQuery, true
	}
	return nil, 0, false
}

// ArgFor is the argument c hands the parameter called name, or nil.
func (c *Call) ArgFor(name string) Expr {
	for i, a := range c.Args {
		if a.Name == name {
			return a.Value
		}
		if a.Name == "" && c.Func != nil && i < len(c.Func.Params) && c.Func.Params[i].Name == name {
			return a.Value
		}
	}
	return nil
}

// WireName is the name a struct field is written under in format.
func (f *StructField) WireName(format CodecFormat) string {
	switch {
	case format == CodecJSON && f.JSON != "":
		return f.JSON
	case format == CodecQuery && f.HTTP != "":
		return f.HTTP
	}
	return f.Name
}

// WireName is the name an enum member is written under, in JSON and in a
// query alike: a member names one value however it travels.
func (m *EnumMember) WireName() string {
	if m.JSON != "" {
		return m.JSON
	}
	return m.Name
}

// CodecKind is the shape of a value on the wire.
type CodecKind int

const (
	CodecBool CodecKind = iota
	CodecInt
	CodecFloat
	CodecString
	// CodecDate is a time.date, written as an RFC 3339 full date.
	CodecDate
	// CodecDatetime is a datetime, written as an RFC 3339 date-time.
	CodecDatetime
	CodecOption
	CodecList
	// CodecMap is a map keyed by string, an object of any keys.
	CodecMap
	// CodecEnum is an enum whose members carry nothing, written as the
	// member's wire name.
	CodecEnum
	CodecStruct
)

// Codec is one node of a CodecPlan: a type and how it is written.
type Codec struct {
	Kind CodecKind
	// Type is the SNGL type the node reads or writes, for a backend that has
	// to spell it.
	Type *Type
	// Elem is an option's element, a list's or a map's value.
	Elem *Codec
	// Fields are a struct's, in the order the type declares them.
	Fields []CodecField
	// Members are an enum's, in the order the type declares them.
	Members []*EnumMember
	// ID is a struct's or an enum's index in CodecPlan.Named.
	ID int
}

// CodecField is one field of a struct node.
type CodecField struct {
	// Name is the field's SNGL name, which is what every host spells it as.
	Name string
	// Wire is the name it is written under.
	Wire  string
	Codec *Codec
}

// CodecPlan is everything a backend renders for one codec call: the root,
// and every struct and enum it reaches, each once, so a type that holds
// itself is a node that points back at its own. A backend declares the named
// nodes first and fills in their fields after, then writes the root.
type CodecPlan struct {
	Root  *Codec
	Named []*Codec
}

// PlanCodec builds the plan for writing t in format. It assumes
// CodecProblem(t, format) == "", which the checker has held every call to.
func PlanCodec(t *Type, format CodecFormat) *CodecPlan {
	p := &CodecPlan{}
	seen := map[string]*Codec{}
	p.Root = p.node(t, format, seen)
	return p
}

func (p *CodecPlan) node(t *Type, format CodecFormat, seen map[string]*Codec) *Codec {
	t = codecUnwrap(t)
	switch t.Kind {
	case TypeBool:
		return &Codec{Kind: CodecBool, Type: t}
	case TypeInt:
		return &Codec{Kind: CodecInt, Type: t}
	case TypeFloat:
		return &Codec{Kind: CodecFloat, Type: t}
	case TypeString:
		return &Codec{Kind: CodecString, Type: t}
	case TypeOption:
		return &Codec{Kind: CodecOption, Type: t, Elem: p.node(t.Elems[0], format, seen)}
	case TypeList:
		return &Codec{Kind: CodecList, Type: t, Elem: p.node(t.Elems[0], format, seen)}
	case TypeMap:
		return &Codec{Kind: CodecMap, Type: t, Elem: p.node(t.Elems[1], format, seen)}
	case TypeEnum:
		key := codecKey(t)
		if n := seen[key]; n != nil {
			return n
		}
		ed := t.Decl.(*EnumDef)
		n := &Codec{Kind: CodecEnum, Type: t, Members: ed.Members, ID: len(p.Named)}
		seen[key] = n
		p.Named = append(p.Named, n)
		return n
	case TypeStruct:
		switch {
		case IsDateStruct(t):
			return &Codec{Kind: CodecDate, Type: t}
		case IsDateTimeStruct(t):
			return &Codec{Kind: CodecDatetime, Type: t}
		}
		key := codecKey(t)
		if n := seen[key]; n != nil {
			return n
		}
		n := &Codec{Kind: CodecStruct, Type: t, ID: len(p.Named)}
		seen[key] = n
		p.Named = append(p.Named, n)
		fields, _ := FieldsOf(t)
		for _, f := range fields {
			n.Fields = append(n.Fields, CodecField{Name: f.Name, Wire: wireName(t, f, format), Codec: p.node(f.Type, format, seen)})
		}
		return n
	}
	panic(fmt.Sprintf("ir: PlanCodec reached %s, which CodecProblem refuses", t))
}

// wireName is f's name on the wire, read off the declaration: FieldsOf hands
// back a fresh field for a generic struct's, which carries no mark.
func wireName(t *Type, f *StructField, format CodecFormat) string {
	if sd, ok := t.Decl.(*StructDef); ok {
		for _, df := range sd.Fields {
			if df.Name == f.Name {
				return df.WireName(format)
			}
		}
	}
	return f.WireName(format)
}

func codecKey(t *Type) string { return fmt.Sprintf("%p%s", t.Decl, t) }

func codecUnwrap(t *Type) *Type {
	for t != nil && t.Kind == TypeReadonly && len(t.Elems) == 1 {
		t = t.Elems[0]
	}
	return t
}

// CodecProblem says why a value of t has no form in format, naming the part
// of t that has none, or returns "" where it has one.
func CodecProblem(t *Type, format CodecFormat) string {
	return codecProblem(t, format, false)
}

// CodecProblemOpen is CodecProblem with each type parameter taken to have a
// form: what a generic body's call is held to, its parameters being checked
// at each call that binds them (GenericCodecs).
func CodecProblemOpen(t *Type, format CodecFormat) string {
	return codecProblem(t, format, true)
}

func codecProblem(t *Type, format CodecFormat, open bool) string {
	if format == CodecQuery {
		return queryProblem(t, open)
	}
	return jsonProblem(t, map[string]bool{}, open)
}

func jsonProblem(t *Type, seen map[string]bool, open bool) string {
	t = codecUnwrap(t)
	if t == nil {
		return "a value of no known type"
	}
	switch t.Kind {
	case TypeBool, TypeInt, TypeFloat, TypeString:
		return ""
	case TypeOption, TypeList:
		return jsonProblem(t.Elems[0], seen, open)
	case TypeMap:
		if k := codecUnwrap(t.Elems[0]); k == nil || k.Kind != TypeString {
			return fmt.Sprintf("%s, whose keys are not strings", t)
		}
		return jsonProblem(t.Elems[1], seen, open)
	case TypeEnum:
		return enumProblem(t)
	case TypeStruct:
		switch {
		case IsDateStruct(t), IsDateTimeStruct(t):
			return ""
		case IsTimeStruct(t), IsColorStruct(t):
			return fmt.Sprintf("%s, which has no JSON form yet", t)
		}
		sd, ok := t.Decl.(*StructDef)
		if !ok || sd.Builtin != "" || sd.Intrinsic != "" || (sd.Foreign.Name != "" && !sd.Foreign.Marked) {
			return fmt.Sprintf("%s, a host type with no fields to write", t)
		}
		key := codecKey(t)
		if seen[key] {
			return ""
		}
		seen[key] = true
		fields, _ := FieldsOf(t)
		wires := map[string]string{}
		for _, f := range fields {
			w := wireName(t, f, CodecJSON)
			if prev, dup := wires[w]; dup {
				return fmt.Sprintf("%s, whose fields %s and %s are both written %q", t, prev, f.Name, w)
			}
			wires[w] = f.Name
			if p := jsonProblem(f.Type, seen, open); p != "" {
				return p
			}
		}
		return ""
	case TypeTypeParam:
		if open {
			return ""
		}
		return fmt.Sprintf("%s, a type parameter: the type has to be known where the call is", t)
	}
	return fmt.Sprintf("%s, which has no JSON form", t)
}

func enumProblem(t *Type) string {
	if PayloadEnum(t) != nil {
		return fmt.Sprintf("%s, an enum whose members carry payloads, which has no JSON form yet", t)
	}
	ed, ok := t.Decl.(*EnumDef)
	if !ok {
		return fmt.Sprintf("%s, which has no JSON form", t)
	}
	wires := map[string]string{}
	for _, m := range ed.Members {
		if m.Value != nil {
			return fmt.Sprintf("%s, an enum whose members carry values, which has no JSON form", t)
		}
		w := m.WireName()
		if prev, dup := wires[w]; dup {
			return fmt.Sprintf("%s, whose members %s and %s are both written %q", t, prev, m.Name, w)
		}
		wires[w] = m.Name
	}
	return ""
}

// queryProblem: a query is a struct whose fields each have a string form, or
// are an option of one, or a list of one.
func queryProblem(t *Type, open bool) string {
	t = codecUnwrap(t)
	if t == nil || open && t.Kind == TypeTypeParam {
		return ""
	}
	if t.Kind == TypeTypeParam {
		return fmt.Sprintf("%s, a type parameter: the type has to be known where the call is", t)
	}
	// What is no struct at all is refused by Q's own constraint.
	if t.Kind != TypeStruct {
		return ""
	}
	sd, ok := t.Decl.(*StructDef)
	if !ok || StringReprStruct(t) {
		return fmt.Sprintf("%s, which is not a struct of parameters", t)
	}
	fields, _ := FieldsOf(t)
	wires := map[string]string{}
	for _, f := range fields {
		w := wireName(t, f, CodecQuery)
		if prev, dup := wires[w]; dup {
			return fmt.Sprintf("%s, whose fields %s and %s are both sent as %q", t, prev, f.Name, w)
		}
		wires[w] = f.Name
		ft := codecUnwrap(f.Type)
		if ft != nil && (ft.Kind == TypeOption || ft.Kind == TypeList) {
			ft = codecUnwrap(ft.Elems[0])
		}
		if !queryScalar(ft) {
			return fmt.Sprintf("field %s of %s, a %s, which has no place in a URL", f.Name, sd.DisplayName(), f.Type)
		}
	}
	return ""
}

func queryScalar(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeBool, TypeInt, TypeFloat, TypeString:
		return true
	case TypeEnum:
		return enumProblem(t) == ""
	case TypeStruct:
		return IsDateStruct(t) || IsDateTimeStruct(t)
	}
	return false
}

// displayName is how a struct is named in a diagnostic: an anonymous one by
// its fields.
func (sd *StructDef) DisplayName() string {
	if sd.Anon || sd.Name == "" {
		parts := make([]string, len(sd.Fields))
		for i, f := range sd.Fields {
			parts[i] = f.Name + " " + f.Type.String()
		}
		return "struct { " + strings.Join(parts, ", ") + " }"
	}
	return sd.Name
}
