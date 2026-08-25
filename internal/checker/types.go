package checker

import "git.duckfam.us/jonathan/sngl/ir"

// Type aliases re-export all IR types so existing code using checker.X
// continues to work. New code should import ir directly.

//go:fix inline
type TypeKind = ir.TypeKind

const (
	//go:fix inline
	TypeInvalid = ir.TypeInvalid
	//go:fix inline
	TypeDyn = ir.TypeDyn
	//go:fix inline
	TypeBool = ir.TypeBool
	//go:fix inline
	TypeInt = ir.TypeInt
	//go:fix inline
	TypeFloat = ir.TypeFloat
	//go:fix inline
	TypeString = ir.TypeString
	//go:fix inline
	TypeList = ir.TypeList
	//go:fix inline
	TypeOption = ir.TypeOption
	//go:fix inline
	TypeStruct = ir.TypeStruct
	//go:fix inline
	TypeEnum = ir.TypeEnum
	//go:fix inline
	TypeUnit = ir.TypeUnit
	//go:fix inline
	TypeFunc = ir.TypeFunc
	//go:fix inline
	TypeComponent = ir.TypeComponent
	//go:fix inline
	//go:fix inline
	TypeNull = ir.TypeNull
	//go:fix inline
	TypeTypeParam = ir.TypeTypeParam
	//go:fix inline
	TypeVoid = ir.TypeVoid
)

//go:fix inline
type Type = ir.Type

//go:fix inline
type FuncSig = ir.FuncSig

//go:fix inline
type Purity = ir.Purity

const (
	//go:fix inline
	PurityUnknown = ir.PurityUnknown
	//go:fix inline
	PurityPure = ir.PurityPure
	//go:fix inline
	PurityReadonly = ir.PurityReadonly
	//go:fix inline
	PurityMutates = ir.PurityMutates
)

//go:fix inline
var TypDyn = ir.TypDyn

//go:fix inline
var TypBool = ir.TypBool

//go:fix inline
var TypInt = ir.TypInt

//go:fix inline
var TypFloat = ir.TypFloat

//go:fix inline
var TypString = ir.TypString

//go:fix inline
var TypNull = ir.TypNull

//go:fix inline

//go:fix inline
var TypVoid = ir.TypVoid

//go:fix inline
var ListOf = ir.ListOf

//go:fix inline
var OptionOf = ir.OptionOf

//go:fix inline
var IterOf = ir.IterOf
