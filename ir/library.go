package ir

import (
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
)

// Builtins is what one library load says that a phase holding no scope
// reads: sngl:builtin's `list` and `map`, sngl:time's `datetime` and `duration`, and the
// signature of every #[intrinsic] declaration it registered. The load fills it
// as each declaration registers, and a check reads its own library's.
//
// A phase with no checker in hand -- a scheme importer, an optimizer or
// lowering pass synthesizing a call -- reads Published, which every load
// writes too: the declarations the last load registered, and every
// intrinsic any load registered. Until a process holds one library that is
// equivalent declarations from different loads, which nothing compares by
// pointer.
type Builtins struct {
	list, mapDecl atomic.Pointer[StructDef]
	dateTime      atomic.Pointer[Type]
	duration      atomic.Pointer[Type]
	application   atomic.Pointer[Component]

	mu         sync.RWMutex
	intrinsics map[string]*IntrinsicDef
}

func NewBuiltins() *Builtins {
	return &Builtins{intrinsics: map[string]*IntrinsicDef{}}
}

var published = NewBuiltins()

// Published is what every library load has registered.
func Published() *Builtins { return published }

// RegisterStruct records a #[builtin]-marked declaration this library
// declares, ignoring one whose kind names none of the three. Called as the
// declaration registers, with the kind read off its source, so a type the
// package resolves after that attaches this declaration.
func (b *Builtins) RegisterStruct(sd *StructDef) {
	if sd == nil {
		return
	}
	sd.mustWrite("registering " + sd.Name)
	switch sd.Builtin {
	case BuiltinList:
		b.list.Store(sd)
	case BuiltinMap:
		b.mapDecl.Store(sd)
	case BuiltinDateTime:
		b.dateTime.Store(sd.SymType())
	}
}

// ListOf is list[elem], carrying this library's `list` once it has
// registered one.
//
// Not `Decl: b.list.Load()` unconditionally: that stores a non-nil Symbol
// holding a nil *StructDef, which every `if t.Decl != nil` in the compiler
// then dereferences.
func (b *Builtins) ListOf(elem *Type) *Type {
	t := &Type{Kind: TypeList, Elems: []*Type{elem}}
	if sd := b.list.Load(); sd != nil {
		t.Decl = sd
	}
	return t
}

// MapOf is map[k, v], carrying this library's `map` once it has registered
// one.
func (b *Builtins) MapOf(k, v *Type) *Type {
	t := &Type{Kind: TypeMap, Elems: []*Type{k, v}}
	if sd := b.mapDecl.Load(); sd != nil {
		t.Decl = sd
	}
	return t
}

// RegisterApplication records sngl:builtin's `_application`.
func (b *Builtins) RegisterApplication(c *Component) {
	b.application.Store(c)
}

// ApplicationType is the type of a handle to the application the package
// body's nodes attach to, or nil before sngl:builtin registers it -- IR built
// with no library loaded has no application to name.
func (b *Builtins) ApplicationType() *Type {
	if c := b.application.Load(); c != nil {
		return &Type{Kind: TypeComponent, Decl: c}
	}
	return nil
}

// RegisterDuration records sngl:time's duration unit.
func (b *Builtins) RegisterDuration(t *Type) {
	if d, ok := t.Decl.(*UnitDef); ok {
		d.mustWrite("registering " + d.Name)
	}
	b.duration.Store(t)
}

// DurationType is this library's duration unit, or int before it registers:
// a duration's magnitude is carried in its base unit, so int is the lossless
// fallback rather than dyn.
func (b *Builtins) DurationType() *Type {
	if t := b.duration.Load(); t != nil {
		return t
	}
	return TypInt
}

// DateTimeType is this library's datetime struct, or dyn before it registers.
func (b *Builtins) DateTimeType() *Type {
	if t := b.dateTime.Load(); t != nil {
		return t
	}
	return TypDyn
}

// RegisterIntrinsic records what a #[intrinsic] declaration says.
//
// Two declarations claiming one id panics, as a duplicate emitter does in
// codegen's registry: an id is the dispatch key every backend answers to, so
// one naming two declarations means half its call sites reach the wrong
// signature. The mark is reachable only from library source -- user source
// cannot import sngl:internal/marks -- so this is a compiler or stdlib
// programmer error and no program can provoke it.
//
// Re-registering the same declaration updates the entry. Same declaration
// means same package and same spelling; a duplicate of that within one
// package is already an error where it is declared.
func (b *Builtins) RegisterIntrinsic(def IntrinsicDef) {
	if def.Name == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if prev, ok := b.intrinsics[def.Name]; ok && !prev.sameDeclAs(def) {
		panic(fmt.Sprintf("ir: intrinsic %q is declared twice, as %s and as %s",
			def.Name, prev.declSite(), def.declSite()))
	}
	b.intrinsics[def.Name] = &def
}

func (d IntrinsicDef) sameDeclAs(other IntrinsicDef) bool {
	return d.Pkg == other.Pkg && d.DeclaredAs == other.DeclaredAs
}

func (d IntrinsicDef) declSite() string {
	if d.Pkg == "" {
		return d.DeclaredAs
	}
	return d.Pkg + "." + d.DeclaredAs
}

// LookupIntrinsic returns the definition registered for an id, or nil when
// no package this library loaded declares it.
func (b *Builtins) LookupIntrinsic(name string) *IntrinsicDef {
	if name == "" {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.intrinsics[name]
}

// IntrinsicByName returns the definition registered for the given id (e.g.
// "list.push"), or ok=false if no package this library loaded declares it.
func (b *Builtins) IntrinsicByName(name string) (IntrinsicDef, bool) {
	def := b.LookupIntrinsic(name)
	if def == nil {
		return IntrinsicDef{}, false
	}
	return *def, true
}

// AllIntrinsics yields every registered definition, ordered by id. The
// snapshot is taken under the lock and yielded without it, so a consumer may
// look one up as it goes.
func (b *Builtins) AllIntrinsics() iter.Seq[*IntrinsicDef] {
	b.mu.RLock()
	out := slices.Collect(maps.Values(b.intrinsics))
	b.mu.RUnlock()

	slices.SortFunc(out, func(a, b *IntrinsicDef) int { return cmp.Compare(a.Name, b.Name) })
	return slices.Values(out)
}

// ListOf returns a list[elem] type, carrying the published `list`.
func ListOf(elem *Type) *Type { return Published().ListOf(elem) }

// MapOf returns a map[K, V] type, carrying the published `map`.
func MapOf(k, v *Type) *Type { return Published().MapOf(k, v) }

// DateTimeType is the published datetime struct, for the foreign-type
// importers that synthesize one without a scope of their own; dyn before a
// library registers one.
func DateTimeType() *Type { return Published().DateTimeType() }

// DurationType is the published duration unit, for the Go importer's
// time.Duration.
func DurationType() *Type { return Published().DurationType() }

// RegisterIntrinsic records def in Published.
func RegisterIntrinsic(def IntrinsicDef) { Published().RegisterIntrinsic(def) }

// LookupIntrinsic is Published's definition for an id.
func LookupIntrinsic(name string) *IntrinsicDef { return Published().LookupIntrinsic(name) }

// IntrinsicByName is Published's definition for an id.
func IntrinsicByName(name string) (IntrinsicDef, bool) { return Published().IntrinsicByName(name) }

// AllIntrinsics yields Published's definitions, ordered by id.
func AllIntrinsics() iter.Seq[*IntrinsicDef] { return Published().AllIntrinsics() }
