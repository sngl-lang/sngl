package c

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"modernc.org/cc/v4"
)

// mapCType converts a cc.Type to an *ir.Type.
// structs is a mutable map from C struct name → ir.StructDef; entries are
// added on first encounter so recursive/shared struct pointers reuse the same def.
// Returns nil for unmappable types (caller should set Func.Unusable).
func mapCType(t cc.Type, ast *cc.AST, structs map[string]*ir.StructDef) *ir.Type {
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case cc.Bool:
		return &ir.Type{Kind: ir.TypeBool}
	case cc.Char, cc.SChar, cc.UChar:
		return &ir.Type{Kind: ir.TypeInt}
	case cc.Int, cc.Short, cc.Long, cc.LongLong,
		cc.UInt, cc.UShort, cc.ULong, cc.ULongLong:
		return &ir.Type{Kind: ir.TypeInt}
	case cc.Float, cc.Double, cc.LongDouble:
		return &ir.Type{Kind: ir.TypeFloat}
	case cc.Void:
		return nil // void return
	case cc.Ptr:
		return mapPointerType(t, ast, structs)
	case cc.Struct:
		return mapStructType(t, ast, structs)
	case cc.Enum:
		return mapEnumType(t)
	case cc.Array:
		// T[] → []T
		at, ok := t.(*cc.ArrayType)
		if !ok {
			return nil
		}
		elem := mapCType(at.Elem(), ast, structs)
		if elem == nil {
			return nil
		}
		return &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{elem}}
	case cc.Function, cc.Union:
		return nil // unmappable
	default:
		return nil
	}
}

func mapPointerType(t cc.Type, ast *cc.AST, structs map[string]*ir.StructDef) *ir.Type {
	pt, ok := t.(*cc.PointerType)
	if !ok {
		return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
	}
	elem := pt.Elem()
	if elem == nil {
		return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
	}
	switch elem.Kind() {
	case cc.Void:
		// void* → unsafe.Pointer
		return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
	case cc.Char, cc.SChar:
		// char* → string (cgo handles the C.GoString / C.CString conversion)
		return &ir.Type{Kind: ir.TypeString}
	case cc.Struct:
		inner := mapStructType(elem, ast, structs)
		if inner == nil {
			return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
		}
		// T* → optional<ref<T>>
		return &ir.Type{Kind: ir.TypeOption, Elems: []*ir.Type{
			{Kind: ir.TypeRef, Elems: []*ir.Type{inner}},
		}}
	case cc.Union:
		// union* → unsafe.Pointer (unusable in SNGL)
		return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
	default:
		inner := mapCType(elem, ast, structs)
		if inner == nil {
			return &ir.Type{Kind: ir.TypeDyn, Meta: "unsafe.Pointer"}
		}
		return &ir.Type{Kind: ir.TypeOption, Elems: []*ir.Type{
			{Kind: ir.TypeRef, Elems: []*ir.Type{inner}},
		}}
	}
}

func mapStructType(t cc.Type, ast *cc.AST, structs map[string]*ir.StructDef) *ir.Type {
	st, ok := t.(*cc.StructType)
	if !ok {
		return nil
	}
	tag := st.Tag()
	name := tag.SrcStr()
	if name == "" {
		return nil // anonymous struct → skip
	}
	if structs == nil {
		structs = map[string]*ir.StructDef{}
	}
	if sd, ok := structs[name]; ok {
		return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	}
	// Create struct def with Native set to "C.Name" so IRTypeToGo emits the cgo type.
	sd := &ir.StructDef{
		Name:   name,
		Native: "C." + name,
	}
	structs[name] = sd
	// Map fields (best-effort; unmappable fields are skipped).
	for i := 0; i < st.NumFields(); i++ {
		f := st.FieldByIndex(i)
		if f == nil || f.Name() == "" || f.IsBitfield() {
			continue
		}
		ft := mapCType(f.Type(), ast, structs)
		if ft == nil {
			continue
		}
		sd.Fields = append(sd.Fields, &ir.StructField{
			Name:       f.Name(),
			NativeName: f.Name(),
			Type:       ft,
		})
	}
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
}

func mapEnumType(t cc.Type) *ir.Type {
	et, ok := t.(*cc.EnumType)
	if !ok {
		return &ir.Type{Kind: ir.TypeInt}
	}
	etag := et.Tag()
	name := etag.SrcStr()
	ed := &ir.EnumDef{Name: name}
	for _, e := range et.Enumerators() {
		tok := e.Token
		ed.Members = append(ed.Members, &ir.EnumMember{
			Name: tok.SrcStr(),
		})
	}
	return &ir.Type{Kind: ir.TypeEnum, Decl: ed}
}
