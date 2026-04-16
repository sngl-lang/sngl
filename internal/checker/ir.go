package checker

import "git.duckfam.us/jonathan/sngl/ir"

// IR type aliases — re-export from ir package.

//go:fix inline
type Package = ir.Package

//go:fix inline
type Import = ir.Import

//go:fix inline
type NativeImport = ir.NativeImport

//go:fix inline
type Func = ir.Func

//go:fix inline
type Var = ir.Var

//go:fix inline
type Component = ir.Component

//go:fix inline
type Prop = ir.Prop

//go:fix inline
type EventDecl = ir.EventDecl

//go:fix inline
type EventHandler = ir.EventHandler

//go:fix inline
type Window = ir.Window

//go:fix inline
type Timer = ir.Timer

//go:fix inline
type Output = ir.Output

//go:fix inline
type Param = ir.Param

//go:fix inline
type StructDef = ir.StructDef

//go:fix inline
type StructField = ir.StructField

//go:fix inline
type EnumDef = ir.EnumDef

//go:fix inline
type EnumMember = ir.EnumMember

//go:fix inline
type UnitDef = ir.UnitDef

//go:fix inline
type UnitSuffix = ir.UnitSuffix

//go:fix inline
type Diagnostic = ir.Diagnostic

//go:fix inline
type Severity = ir.Severity

// Statement IR type aliases.

//go:fix inline
type Stmt = ir.Stmt

//go:fix inline
type NodeInst = ir.NodeInst

//go:fix inline
type CallStmt = ir.CallStmt

//go:fix inline
type SlotInst = ir.SlotInst

//go:fix inline
type Assign = ir.Assign

//go:fix inline
type Toggle = ir.Toggle

//go:fix inline
type Emit = ir.Emit

//go:fix inline
type LocalVar = ir.LocalVar

//go:fix inline
type Return = ir.Return

//go:fix inline
type If = ir.If

//go:fix inline
type For = ir.For

//go:fix inline
type PlatformFilter = ir.PlatformFilter

// Target/interface type aliases.

//go:fix inline
type Target = ir.Target

//go:fix inline
type Language = ir.Language

//go:fix inline
type Platform = ir.Platform

//go:fix inline
type StaticTarget = ir.StaticTarget

// Severity constants.
const (
	//go:fix inline
	Error = ir.Error
	//go:fix inline
	Warning = ir.Warning
)
