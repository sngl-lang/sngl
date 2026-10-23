package ir

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
)

// Owned reports whether a value of t is something the program must give back:
// a struct declared `#[owned]`, or a type that holds one -- a struct with an
// owned field, a list, a map or an option of an owned type.
func Owned(t *Type) bool { return owned(t, nil) }

// Allocates reports whether e makes an owned value, rather than reading one
// something else holds. An intrinsic only reads one out of a collection: a
// cell's `map.get` is the copy's value, not a new one. A literal of a type
// owned by what it holds makes a value only where something in it does:
// `[f]` holds what the var holds, and `[open(1)]` what nothing else does.
func Allocates(e Expr) bool {
	if e == nil || !Owned(e.ExprType()) {
		return false
	}
	switch x := e.(type) {
	case *Call:
		return x.Func != nil && x.Func.Intrinsic == ""
	case *StructLit:
		return ReleaseOf(x.Type) != nil || slices.ContainsFunc(x.Fields, func(f FieldInit) bool { return Allocates(f.Value) })
	case *ListLit:
		return slices.ContainsFunc(x.Elems, Allocates)
	case *MapLitIR:
		return slices.ContainsFunc(x.Entries, func(en MapEntry) bool { return Allocates(en.Value) })
	}
	return false
}

func owned(t *Type, seen map[*StructDef]bool) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case TypeList, TypeOption:
		return len(t.Elems) == 1 && owned(t.Elems[0], seen)
	case TypeMap:
		return len(t.Elems) == 2 && owned(t.Elems[1], seen)
	case TypeStruct:
		sd, ok := t.Decl.(*StructDef)
		if !ok {
			return false
		}
		if sd.Release != nil {
			return true
		}
		if seen[sd] {
			return false
		}
		if seen == nil {
			seen = map[*StructDef]bool{}
		}
		seen[sd] = true
		for _, f := range sd.Fields {
			if f != nil && owned(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

// ReleaseFor is the function that gives a value of t back: the release its
// struct declares, or, for a type owned by what it holds, one synthesized on p
// once per type (`__release_<type>`) that releases what the value holds,
// newest first -- a list's last element first, a struct's last owned field
// first. Nil for a type nothing owns.
func (p *Package) ReleaseFor(t *Type) *Func {
	if fn := ReleaseOf(t); fn != nil {
		return fn
	}
	if !Owned(t) {
		return nil
	}
	key := releaseKey(t)
	if fn := p.Releases[key]; fn != nil {
		return fn
	}
	if p.Releases == nil {
		p.Releases = map[string]*Func{}
	}
	param := &Param{Name: "__v", Type: t}
	fn := &Func{
		Name: p.releaseName(t), Pkg: p,
		Params:      []*Param{param},
		Return:      TypVoid,
		Purity:      PurityMutates,
		Synthesized: true,
		Releases:    t,
	}
	// Registered before the body is built: a struct holding a list of itself
	// reaches its own release.
	p.Releases[key] = fn
	p.Funcs = append(p.Funcs, fn)
	v := &Ident{Name: param.Name, Sym: param, Type: t}
	fn.Block = p.releaseBody(t, v)
	return fn
}

func (p *Package) releaseBody(t *Type, v Expr) []Stmt {
	release := func(t *Type, e Expr) Stmt {
		return &CallStmt{Call: &Call{Type: TypVoid, Func: p.ReleaseFor(t), Args: []CallArg{{Value: e}}}}
	}
	switch t.Kind {
	case TypeList:
		elem := t.Elems[0]
		x := &LoopVar{Name: "__x", Type: elem}
		reversed := &Call{Type: t, Func: IntrinsicMethod("reverse", "list.reverse", t, t), Args: []CallArg{{Value: v}}}
		return []Stmt{&For{Key: x.Name, KeySym: x, Iter: reversed, ElemType: elem, Body: []Stmt{
			release(elem, &Ident{Name: x.Name, Sym: x, Type: elem}),
		}}}
	case TypeMap:
		k := &LoopVar{Name: "__k", Type: t.Elems[0]}
		x := &LoopVar{Name: "__x", Type: t.Elems[1]}
		return []Stmt{&For{Key: k.Name, KeySym: k, Value: x.Name, ValueSym: x, Iter: v, ElemType: x.Type, Body: []Stmt{
			release(x.Type, &Ident{Name: x.Name, Sym: x, Type: x.Type}),
		}}}
	case TypeOption:
		inner := t.Elems[0]
		null := &Literal{Type: TypNull, Value: "null"}
		return []Stmt{&If{
			Cond: &Binary{Type: TypBool, Op: ast.BinNeq, Left: v, Right: null},
			Body: []Stmt{release(inner, &Conversion{Type: inner, Operand: CloneExprSharingDecls(v)})},
		}}
	case TypeStruct:
		var out []Stmt
		for _, f := range slices.Backward(t.Decl.(*StructDef).Fields) {
			if Owned(f.Type) {
				out = append(out, release(f.Type, &Select{Operand: CloneExprSharingDecls(v), Field: f.Name, Type: f.Type}))
			}
		}
		return out
	}
	return nil
}

// releaseKey identifies a composed type by what it holds, down to the
// declaration of the owned struct, since two packages may each declare one
// of a name.
func releaseKey(t *Type) string {
	switch t.Kind {
	case TypeList:
		return "list[" + releaseKey(t.Elems[0]) + "]"
	case TypeOption:
		return "option[" + releaseKey(t.Elems[0]) + "]"
	case TypeMap:
		return "map[" + t.Elems[0].String() + "," + releaseKey(t.Elems[1]) + "]"
	case TypeStruct:
		return fmt.Sprintf("%s#%p", t.String(), t.Decl)
	}
	return t.String()
}

// releaseName spells `__release_<type>` as an identifier, and tells apart two
// types that spell alike.
func (p *Package) releaseName(t *Type) string {
	var b strings.Builder
	b.WriteString("__release_")
	under := false
	for _, r := range t.String() {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			under = false
		} else if !under {
			b.WriteByte('_')
			under = true
		}
	}
	name := strings.TrimSuffix(b.String(), "_")
	taken := func(n string) bool {
		for _, fn := range p.Releases {
			if fn.Name == n {
				return true
			}
		}
		return false
	}
	out := name
	for n := 1; taken(out); n++ {
		out = fmt.Sprintf("%s_%d", name, n)
	}
	return out
}
