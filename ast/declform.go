package ast

import "fmt"

// DeclFormName names a declaration form the way the language spells it. A
// diagnostic about someone's source should not be the first place they meet the
// compiler's Go types.
func DeclFormName(decl any) string {
	switch decl.(type) {
	case *StructDef:
		return "a struct"
	case *StructField:
		return "a struct field"
	case *EnumDef:
		return "an enum"
	case *UnitDef:
		return "a unit"
	case *ComponentDecl:
		return "a component"
	case *FuncDef:
		return "a function"
	case *VarDecl:
		return "a var"
	case *ConstDecl:
		return "a const"
	case *Import:
		return "an import"
	case *Param:
		return "a parameter"
	case nil:
		return "nothing"
	default:
		return fmt.Sprintf("%T", decl)
	}
}
