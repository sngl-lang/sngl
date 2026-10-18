// Package openapi reads an OpenAPI 3 document into the data the `openapi:`
// scheme's handler writes a package from (lib/x/scheme/openapi). It answers
// the build intrinsic `openapi.parse`: what is the spec's, and only Go can
// read -- YAML, references, a schema's shape -- is decided here, and what the
// written package spells is the handler's.
package openapi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind is what a value of a Type is, the Go side of `openapi.Kind`.
type Kind string

const (
	KindText     Kind = "text"
	KindInteger  Kind = "integer"
	KindNumber   Kind = "number"
	KindBoolean  Kind = "boolean"
	KindDate     Kind = "date"
	KindDatetime Kind = "datetime"
	KindNamed    Kind = "named"
	KindList     Kind = "array"
	KindDict     Kind = "dict"
)

// Type is a schema as the type a declaration spells: a scalar, a declaration
// the package writes, or a list or string-keyed map of one.
type Type struct {
	Kind  Kind   `sngl:"kind"`
	Name  string `sngl:"name"`  // KindNamed: the declaration's SNGL name
	Elems []Type `sngl:"elems"` // KindList, KindDict: the element
}

// Field is one property of an object schema.
type Field struct {
	Wire     string `sngl:"wire"` // the key in JSON
	Name     string `sngl:"name"` // the SNGL field name
	Type     Type   `sngl:"type"`
	Optional bool   `sngl:"optional"` // may be absent or null
	Doc      string `sngl:"doc"`
}

// Member is one value of a string enum.
type Member struct {
	Wire string `sngl:"wire"`
	Name string `sngl:"name"`
}

// Schema is one declaration the package writes: a struct, or an enum when
// Members is set.
type Schema struct {
	Name    string   `sngl:"name"`
	Doc     string   `sngl:"doc"`
	Enum    bool     `sngl:"isEnum"`
	Fields  []Field  `sngl:"fields"`
	Members []Member `sngl:"members"`
}

// Segment is one piece of an operation's path: text as written, or the
// parameter it is filled from.
type Segment struct {
	Text  string `sngl:"text"`
	Param string `sngl:"param"` // the parameter's SNGL name
	Kind  Kind   `sngl:"kind"`  // the parameter's kind, where Param is set
}

// Param is one parameter of an operation, in the path or the query.
type Param struct {
	Wire     string  `sngl:"wire"`
	Name     string  `sngl:"name"`
	Path     bool    `sngl:"inPath"`
	Type     Type    `sngl:"type"`
	Optional bool    `sngl:"optional"` // may be left out: an option defaulting to null
	Default  *string `sngl:"default"`  // the spec's default, as YAML spells the scalar
	Doc      string  `sngl:"doc"`
}

// Operation is one request the package can make.
type Operation struct {
	Method    string    `sngl:"method"` // a member of http.Method
	PathText  string    `sngl:"path"`   // the path as the spec writes it
	Path      []Segment `sngl:"segments"`
	Func      string    `sngl:"call"`    // the function's name, `pokemonRetrieveCall`
	Component string    `sngl:"adapter"` // the adapter's name, `pokemonRetrieve`; "" for none
	Summary   string    `sngl:"summary"`
	Doc       string    `sngl:"doc"`
	Params    []Param   `sngl:"params"`
	Query     string    `sngl:"query"`    // the declared struct holding the query; "" for an anonymous one
	Body      *Type     `sngl:"body"`     // a JSON request body, sent last
	BodyName  string    `sngl:"bodyName"` // the parameter it is
	Result    Type      `sngl:"result"`
}

// Spec is what the handler is handed.
type Spec struct {
	Title      string      `sngl:"title"`
	Server     string      `sngl:"server"`
	Schemas    []Schema    `sngl:"schemas"`
	Operations []Operation `sngl:"operations"`
	// Notes are what the import says: what was left out, and why, and what
	// was renamed.
	Notes []string `sngl:"notes"`
	// Time is whether a type names time.date or datetime.
	Time bool `sngl:"usesTime"`
}

// reservedPackage are the names the written package declares beside the
// spec's: the context, and the helper every function shares.
var reservedPackage = []string{"server", "_answered"}

// reservedProps are the names an adapter's own props take, which a
// parameter may not.
var reservedProps = []string{"value", "pending", "failure", "load", "fail", "server"}

// Parse reads an OpenAPI 3 document, YAML or JSON.
func Parse(src []byte) (*Spec, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("not YAML or JSON: %w", err)
	}
	root := node{&doc}
	if root.n.Kind == yaml.DocumentNode && len(root.n.Content) == 1 {
		root = node{root.n.Content[0]}
	}
	if root.n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("not an OpenAPI document: the top level is no mapping")
	}
	version := root.get("openapi").str()
	if version == "" {
		if root.get("swagger").ok() {
			return nil, fmt.Errorf("a Swagger %s document: only OpenAPI 3 is read", root.get("swagger").str())
		}
		return nil, fmt.Errorf("not an OpenAPI document: it has no `openapi` version")
	}
	if !strings.HasPrefix(version, "3.") {
		return nil, fmt.Errorf("OpenAPI %s: only OpenAPI 3 is read", version)
	}
	r := &reader{root: root, names: newNames(reservedPackage), refs: map[string]*schemaRef{}}
	return r.read()
}

// schemaRef is a named schema of components.schemas, read once.
type schemaRef struct {
	key      string
	node     node
	name     string // the SNGL name, once claimed
	state    int    // 0 unread, 1 reading, 2 read
	left     string // why it was left out; "" when written
	nullable bool   // a reference to it may be null
	decl     *Schema
}

type reader struct {
	root  node
	names *names
	refs  map[string]*schemaRef
	order []*schemaRef
	// synthesized are the declarations an inline object or enum became, in
	// the order they were met, each written after the schema that held it.
	synthesized map[*schemaRef][]Schema
	spec        Spec
}

func (r *reader) note(format string, args ...any) {
	r.spec.Notes = append(r.spec.Notes, fmt.Sprintf(format, args...))
}

func (r *reader) read() (*Spec, error) {
	r.spec.Title = r.root.get("info").get("title").str()
	if servers := r.root.get("servers"); servers.len() > 0 {
		r.spec.Server = strings.TrimSuffix(servers.at(0).get("url").str(), "/")
	} else {
		r.note("the spec names no server: server is empty until a program provides one")
	}
	r.synthesized = map[*schemaRef][]Schema{}
	schemas := r.root.get("components").get("schemas")
	for _, kv := range schemas.pairs() {
		ref := &schemaRef{key: kv.key, node: kv.val}
		r.refs["#/components/schemas/"+kv.key] = ref
		r.order = append(r.order, ref)
	}
	// Every name is claimed before any body is read, so a reference to a
	// schema later in the document names what it will be called.
	for _, ref := range r.order {
		ref.name = r.names.claimType(ref.key, r)
	}
	for _, ref := range r.order {
		r.schema(ref)
	}
	paths := r.root.get("paths")
	for _, kv := range paths.pairs() {
		item := kv.val
		shared := item.get("parameters")
		for _, m := range item.pairs() {
			switch m.key {
			case "get", "put", "post", "delete", "patch", "head", "options", "trace":
				r.operation(kv.key, m.key, m.val, shared)
			}
		}
	}
	for _, ref := range r.order {
		if ref.decl != nil && ref.left == "" {
			r.spec.Schemas = append(r.spec.Schemas, *ref.decl)
		}
		r.spec.Schemas = append(r.spec.Schemas, r.synthesized[ref]...)
	}
	r.spec.Schemas = append(r.spec.Schemas, r.synthesized[nil]...)
	return &r.spec, nil
}

// schema reads a named schema into its declaration, once. A schema that
// cannot be written is left out, and so is whatever reaches it.
func (r *reader) schema(ref *schemaRef) {
	if ref.state != 0 {
		return
	}
	ref.state = 1
	defer func() { ref.state = 2 }()
	n := ref.node
	ref.nullable = nullable(n)
	if members, ok, why := enumMembers(n); ok || why != "" {
		if why != "" {
			ref.left = why
			r.note("schema %s is left out: %s", ref.key, why)
			return
		}
		ref.decl = &Schema{Name: ref.name, Doc: doc(n), Enum: true, Members: r.members(members, ref.key)}
		return
	}
	decl := &Schema{Name: ref.name, Doc: doc(n)}
	if why := r.object(n, decl, ref, ref.name); why != "" {
		ref.left = why
		r.note("schema %s is left out: %s", ref.key, why)
		return
	}
	ref.decl = decl
}

// object fills decl's fields from an object schema, its allOf merged in.
func (r *reader) object(n node, decl *Schema, owner *schemaRef, outer string) string {
	if why := unsupported(n); why != "" {
		return why
	}
	if !isObject(n) {
		return "it is no object and no string enum"
	}
	fields := map[string]bool{}
	var merge func(n node) string
	merge = func(n node) string {
		for _, part := range n.get("allOf").items() {
			if ref := part.get("$ref").str(); ref != "" {
				target, why := r.resolve(ref)
				if why != "" {
					return why
				}
				if target.state == 1 {
					return "its allOf reaches itself"
				}
				r.schema(target)
				if target.left != "" {
					return "its allOf names " + target.key + ", which is left out"
				}
				if target.decl.Enum {
					return "its allOf names " + target.key + ", which is no object"
				}
				for _, f := range target.decl.Fields {
					if !fields[f.Wire] {
						fields[f.Wire] = true
						decl.Fields = append(decl.Fields, f)
					}
				}
				continue
			}
			if why := merge(part); why != "" {
				return why
			}
		}
		required := map[string]bool{}
		for _, s := range n.get("required").items() {
			required[s.str()] = true
		}
		for _, kv := range n.get("properties").pairs() {
			if fields[kv.key] {
				continue
			}
			fields[kv.key] = true
			name := r.names.field(decl, kv.key, outer, r)
			t, opt, why := r.typeOf(kv.val, owner, outer+capital(name))
			if why != "" {
				r.note("field %s of %s is left out: %s", kv.key, outer, why)
				continue
			}
			decl.Fields = append(decl.Fields, Field{Wire: kv.key, Name: name, Type: t, Optional: opt || !required[kv.key], Doc: doc(kv.val)})
		}
		return ""
	}
	return merge(n)
}

// typeOf is the type a schema in a position is, whether it may be null, and
// why it has none. synth is the name an inline object or enum is declared
// under.
func (r *reader) typeOf(n node, owner *schemaRef, synth string) (Type, bool, string) {
	if !n.ok() {
		return Type{}, false, "it has no schema"
	}
	if why := unsupported(n); why != "" {
		return Type{}, false, why
	}
	opt := nullable(n)
	if ref := n.get("$ref").str(); ref != "" {
		target, why := r.resolve(ref)
		if why != "" {
			return Type{}, false, why
		}
		if target.state == 0 {
			r.schema(target)
		}
		if target.left != "" {
			return Type{}, false, "it names " + target.key + ", which is left out"
		}
		return Type{Kind: KindNamed, Name: target.name}, opt || target.nullable, ""
	}
	if one := n.get("oneOf"); one.ok() {
		inner, ok := nullUnion(one)
		if !ok {
			return Type{}, false, "a oneOf that is not one schema or null"
		}
		t, _, why := r.typeOf(inner, owner, synth)
		return t, true, why
	}
	if all := n.get("allOf"); all.ok() && !n.get("properties").ok() {
		if all.len() == 1 {
			t, o, why := r.typeOf(all.at(0), owner, synth)
			return t, o || opt, why
		}
	}
	if members, ok, why := enumMembers(n); ok || why != "" {
		if why != "" {
			return Type{}, false, why
		}
		name := r.names.claimType(synth, r)
		r.synthesized[owner] = append(r.synthesized[owner], Schema{Name: name, Doc: doc(n), Enum: true, Members: r.members(members, synth)})
		return Type{Kind: KindNamed, Name: name}, opt, ""
	}
	switch typeName(n) {
	case "string":
		switch n.get("format").str() {
		case "date":
			r.spec.Time = true
			return Type{Kind: KindDate}, opt, ""
		case "date-time":
			r.spec.Time = true
			return Type{Kind: KindDatetime}, opt, ""
		}
		return Type{Kind: KindText}, opt, ""
	case "integer":
		return Type{Kind: KindInteger}, opt, ""
	case "number":
		return Type{Kind: KindNumber}, opt, ""
	case "boolean":
		return Type{Kind: KindBoolean}, opt, ""
	case "array":
		elem, eopt, why := r.typeOf(n.get("items"), owner, synth+"Item")
		if why != "" {
			return Type{}, false, "its items: " + why
		}
		if eopt {
			return Type{}, false, "its items may be null, and a list holds no option"
		}
		return Type{Kind: KindList, Elems: []Type{elem}}, opt, ""
	case "object", "":
		ap := n.get("additionalProperties")
		if !n.get("properties").ok() && !n.get("allOf").ok() && ap.ok() {
			if ap.n.Kind != yaml.MappingNode || len(ap.n.Content) == 0 {
				return Type{}, false, "a map of any value"
			}
			elem, eopt, why := r.typeOf(ap, owner, synth+"Value")
			if why != "" {
				return Type{}, false, "its values: " + why
			}
			if eopt {
				return Type{}, false, "its values may be null, and a map holds no option"
			}
			return Type{Kind: KindDict, Elems: []Type{{Kind: KindText}, elem}}, opt, ""
		}
		if typeName(n) == "" && !n.get("properties").ok() && !n.get("allOf").ok() {
			return Type{}, false, "a schema saying nothing of its type"
		}
		name := r.names.claimType(synth, r)
		decl := &Schema{Name: name, Doc: doc(n)}
		if why := r.object(n, decl, owner, name); why != "" {
			return Type{}, false, why
		}
		r.synthesized[owner] = append(r.synthesized[owner], *decl)
		return Type{Kind: KindNamed, Name: name}, opt, ""
	}
	return Type{}, false, "a type of " + typeName(n)
}

// resolve finds a local reference to a named schema.
func (r *reader) resolve(ref string) (*schemaRef, string) {
	target := r.refs[ref]
	if target == nil {
		if !strings.HasPrefix(ref, "#/") {
			return nil, "it names " + ref + ", outside the document"
		}
		return nil, "it names " + ref + ", which the document does not declare"
	}
	return target, ""
}

func (r *reader) members(values []string, owner string) []Member {
	used := newNames(nil)
	out := make([]Member, 0, len(values))
	for _, v := range values {
		if v == "" {
			r.note("member \"\" of %s is left out: json.identifier names no empty key", owner)
			continue
		}
		name := camel(v)
		if name == "" {
			name = "none"
		}
		out = append(out, Member{Wire: v, Name: used.claim(name, func(from, to string) {
			r.note("member %q of %s is %s", v, owner, to)
		})})
	}
	return out
}

// operation reads one operation, or says why it is left out.
func (r *reader) operation(path, method string, n, shared node) {
	label := strings.ToUpper(method) + " " + path
	leave := func(why string) { r.note("%s is left out: %s", label, why) }
	switch method {
	case "options", "trace":
		leave("sngl:http sends no " + strings.ToUpper(method))
		return
	}
	id := n.get("operationId").str()
	if id == "" {
		id = method + "_" + path
	}
	base := camel(id)
	op := Operation{Method: method, PathText: path, Summary: oneLine(n.get("summary").str()), Doc: strings.TrimSpace(n.get("description").str())}
	pnames := newNames(reservedProps)
	var params []Param
	seen := map[string]bool{}
	for _, list := range []node{n.get("parameters"), shared} {
		for _, p := range list.items() {
			if ref := p.get("$ref").str(); ref != "" {
				p = r.componentRef(ref, "parameters")
				if !p.ok() {
					leave("a parameter names " + ref + ", which the document does not declare")
					return
				}
			}
			wire, in := p.get("name").str(), p.get("in").str()
			if seen[in+" "+wire] {
				continue
			}
			seen[in+" "+wire] = true
			if in != "path" && in != "query" {
				leave(fmt.Sprintf("parameter %s is in the %s, and only a path's and a query's are sent", wire, in))
				return
			}
			t, opt, why := r.typeOf(p.get("schema"), nil, capital(base)+capital(camel(wire)))
			if why != "" {
				leave(fmt.Sprintf("parameter %s: %s", wire, why))
				return
			}
			if t.Kind == KindNamed || t.Kind == KindDict || t.Kind == KindDate || t.Kind == KindDatetime || t.Kind == KindList && t.Elems[0].Kind == KindNamed {
				leave(fmt.Sprintf("parameter %s is a %s, which a %s does not carry", wire, describe(t), in))
				return
			}
			if in == "path" && t.Kind == KindList {
				leave(fmt.Sprintf("parameter %s is a list, which a path does not carry", wire))
				return
			}
			name := pnames.claim(camel(wire), func(from, to string) {
				r.note("parameter %s of %s is %s", wire, label, to)
			})
			pr := Param{Wire: wire, Name: name, Path: in == "path", Type: t, Doc: oneLine(p.get("description").str())}
			if !pr.Path && !p.get("required").bool() {
				d := p.get("schema").get("default")
				switch {
				case t.Kind == KindList:
					// A list sends its key once per element, so an empty one
					// sends nothing: it is the list's own absence, and a
					// URL has no place for an option of a list.
					empty := "[]"
					pr.Default = &empty
				case d.ok() && d.n.Kind == yaml.ScalarNode:
					v := d.n.Value
					pr.Default = &v
				default:
					pr.Optional = true
				}
			}
			if opt && pr.Default == nil {
				pr.Optional = true
			}
			params = append(params, pr)
		}
	}
	// Path parameters first, in the order the path names them, then the
	// query's, as the spec lists them.
	var segs []Segment
	rest := path
	for rest != "" {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			segs = append(segs, Segment{Text: rest})
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			leave("its path has an unclosed {")
			return
		}
		if i > 0 {
			segs = append(segs, Segment{Text: rest[:i]})
		}
		wire := rest[i+1 : i+j]
		k := slices.IndexFunc(params, func(p Param) bool { return p.Path && p.Wire == wire })
		if k < 0 {
			leave("its path names {" + wire + "}, which no path parameter declares")
			return
		}
		segs = append(segs, Segment{Param: params[k].Name, Kind: params[k].Type.Kind})
		rest = rest[i+j+1:]
	}
	op.Path = segs
	for _, p := range params {
		if p.Path {
			op.Params = append(op.Params, p)
		}
	}
	plain := true
	for _, p := range params {
		if !p.Path {
			op.Params = append(op.Params, p)
			plain = plain && p.Name == p.Wire
		}
	}
	if body := n.get("requestBody"); body.ok() {
		if ref := body.get("$ref").str(); ref != "" {
			body = r.componentRef(ref, "requestBodies")
		}
		media := body.get("content").get("application/json")
		if !media.ok() {
			leave("its request body is not JSON")
			return
		}
		t, _, why := r.typeOf(media.get("schema"), nil, capital(base)+"Body")
		if why != "" {
			leave("its request body: " + why)
			return
		}
		op.Body = &t
		op.BodyName = pnames.claim("body", func(from, to string) {
			r.note("the request body of %s is the parameter %s: %s is taken", label, to, from)
		})
	}
	result, ok, why := r.response(n.get("responses"), capital(base)+"Response")
	if !ok {
		leave(why)
		return
	}
	op.Result = result
	op.Func = r.names.claimFunc(base+"Call", r)
	if method == "get" {
		op.Component = r.names.claimFunc(base, r)
		for _, p := range op.Params {
			if p.Type.Kind == KindList {
				r.note("%s has no adapter: parameter %s is a list, which a query's key cannot compare", label, p.Wire)
				op.Component = ""
				break
			}
		}
	}
	if !plain && hasQuery(op.Params) {
		op.Query = r.names.claimType(capital(base)+"Query", r)
	}
	r.spec.Operations = append(r.spec.Operations, op)
}

func hasQuery(ps []Param) bool {
	return slices.ContainsFunc(ps, func(p Param) bool { return !p.Path })
}

// response is the type the operation's success answers: its first 2xx
// response holding JSON.
func (r *reader) response(n node, synth string) (Type, bool, string) {
	for _, kv := range n.pairs() {
		if len(kv.key) != 3 || kv.key[0] != '2' {
			continue
		}
		resp := kv.val
		if ref := resp.get("$ref").str(); ref != "" {
			resp = r.componentRef(ref, "responses")
		}
		media := resp.get("content").get("application/json")
		if !media.ok() {
			return Type{}, false, "its " + kv.key + " response holds no JSON"
		}
		t, opt, why := r.typeOf(media.get("schema"), nil, synth)
		if why != "" {
			return Type{}, false, "its " + kv.key + " response: " + why
		}
		if opt {
			return Type{}, false, "its " + kv.key + " response may be null"
		}
		return t, true, ""
	}
	return Type{}, false, "it declares no 2xx response"
}

// componentRef is a reference to one of components.<section>.
func (r *reader) componentRef(ref, section string) node {
	key, ok := strings.CutPrefix(ref, "#/components/"+section+"/")
	if !ok {
		return node{}
	}
	return r.root.get("components").get(section).get(key)
}

// unsupported is why a schema has no type here, read off the keywords that
// say so wherever they are.
func unsupported(n node) string {
	switch {
	case n.get("anyOf").ok():
		return "an anyOf, which no declaration spells"
	case n.get("discriminator").ok():
		return "a oneOf with a discriminator, which sngl:json does not read yet"
	case n.get("not").ok():
		return "a not, which no declaration spells"
	}
	if t := n.get("type"); t.n != nil && t.n.Kind == yaml.SequenceNode {
		var kinds []string
		for _, k := range t.items() {
			if k.str() != "null" {
				kinds = append(kinds, k.str())
			}
		}
		if len(kinds) != 1 {
			return "a value of " + strings.Join(kinds, " or ") + ", which no declaration spells"
		}
	} else if t.n != nil && t.n.Kind != yaml.ScalarNode {
		return "a type that is no name"
	}
	return ""
}

// typeName is the schema's type, the one beside null in a 3.1 union.
func typeName(n node) string {
	t := n.get("type")
	if t.n != nil && t.n.Kind == yaml.SequenceNode {
		for _, k := range t.items() {
			if k.str() != "null" {
				return k.str()
			}
		}
		return "null"
	}
	return t.str()
}

func isObject(n node) bool {
	t := typeName(n)
	return t == "object" || t == "" && (n.get("properties").ok() || n.get("allOf").ok())
}

// nullable is whether a schema admits null: 3.0's `nullable: true`, or null
// beside its type in 3.1's union.
func nullable(n node) bool {
	if n.get("nullable").bool() {
		return true
	}
	t := n.get("type")
	if t.n != nil && t.n.Kind == yaml.SequenceNode {
		for _, k := range t.items() {
			if k.str() == "null" {
				return true
			}
		}
	}
	for _, v := range n.get("enum").items() {
		if v.n.Tag == "!!null" {
			return true
		}
	}
	return false
}

// nullUnion is the schema of a oneOf that is that schema or null.
func nullUnion(one node) (node, bool) {
	var inner []node
	for _, s := range one.items() {
		if typeName(s) == "null" && !s.get("$ref").ok() {
			continue
		}
		inner = append(inner, s)
	}
	if len(inner) != 1 || len(inner) == one.len() {
		return node{}, false
	}
	return inner[0], true
}

// enumMembers is a string enum's values; ok is false where the schema is no
// enum, and why is set where it is one no declaration spells.
func enumMembers(n node) ([]string, bool, string) {
	e := n.get("enum")
	if !e.ok() {
		return nil, false, ""
	}
	if typeName(n) != "string" && typeName(n) != "" {
		return nil, false, "an enum of " + typeName(n) + ", and only a string enum is a declaration"
	}
	var out []string
	for _, v := range e.items() {
		if v.n.Tag == "!!null" {
			continue
		}
		if v.n.Kind != yaml.ScalarNode {
			return nil, false, "an enum holding something other than strings"
		}
		out = append(out, v.n.Value)
	}
	return out, true, ""
}

func describe(t Type) string {
	switch t.Kind {
	case KindNamed:
		return t.Name
	case KindList:
		return "list of " + describe(t.Elems[0])
	case KindDict:
		return "map"
	}
	return string(t.Kind)
}

// doc is a schema's description, trimmed.
func doc(n node) string {
	return strings.TrimSpace(n.get("description").str())
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// node reads a YAML node with absence passed through, so a chain of gets
// on a document that lacks a key answers an empty node.
type node struct{ n *yaml.Node }

type pair struct {
	key string
	val node
}

func (x node) ok() bool { return x.n != nil }

func (x node) get(key string) node {
	if x.n == nil || x.n.Kind != yaml.MappingNode {
		return node{}
	}
	for i := 0; i+1 < len(x.n.Content); i += 2 {
		if x.n.Content[i].Value == key {
			return node{deref(x.n.Content[i+1])}
		}
	}
	return node{}
}

func (x node) pairs() []pair {
	if x.n == nil || x.n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]pair, 0, len(x.n.Content)/2)
	for i := 0; i+1 < len(x.n.Content); i += 2 {
		out = append(out, pair{x.n.Content[i].Value, node{deref(x.n.Content[i+1])}})
	}
	return out
}

func (x node) items() []node {
	if x.n == nil || x.n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]node, len(x.n.Content))
	for i, c := range x.n.Content {
		out[i] = node{deref(c)}
	}
	return out
}

func (x node) len() int { return len(x.items()) }

func (x node) at(i int) node { return x.items()[i] }

func (x node) str() string {
	if x.n == nil || x.n.Kind != yaml.ScalarNode {
		return ""
	}
	return x.n.Value
}

func (x node) bool() bool {
	b, _ := strconv.ParseBool(x.str())
	return b
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}
