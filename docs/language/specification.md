---
title: "Language Specification"
order: 3
description: "Formal grammar and semantics of the SNGL language"
---

## Grammar

SNGL uses an LL(1) grammar parseable with a single token of lookahead via recursive descent.

### Document Structure

```
Document       = Declaration*

Declaration    = "import" STRING
               | "output" IDENT IDENT ("(" KVList ")")?
               | "output" "{" OutputSpec* "}"
               | "struct" IDENT "{" StructField* "}"
               | "enum" IDENT "{" IDENT ("," IDENT)* "}"
               | "unit" IDENT "(" UnitSuffixDef ("," UnitSuffixDef)* ")"
               | "style" IDENT "{" StyleProp* "}"
               | "styles" "{" StyleDef* "}"
               | FuncDecl
               | "component" IDENT "{" ComponentMember* "}"
```

### Output Declarations

```
OutputSpec     = IDENT IDENT ("(" KVList ")")?
               | IDENT "{" PlatformList "}"

PlatformList   = IDENT ("(" KVList ")")? ("," IDENT ("(" KVList ")")?)*
```

### State Declarations

```
ConstDecl      = "const" IDENT Type? "=" Expr
               | "const" "(" ConstField ("," ConstField)* ","? ")"

ConstField     = IDENT Type? "=" Expr

VarDecl        = "var" IDENT Type? "=" Expr VarMod*
               | "var" IDENT Type VarMod*
               | "var" "(" VarField ("," VarField)* ","? ")"

VarField       = IDENT Type? "=" Expr VarMod*
               | IDENT Type VarMod*

VarMod         = "extern"
               | "trigger" ("(" STRING ")")?
```

### Type Syntax

```
Type           = IDENT ("<" Type ("," Type)* ">")?
               | "func" "(" TypeList? ")" ("->" Type)?
               | "enum" "<" IDENT ("|" IDENT)* ">"

TypeList       = Type ("," Type)*
```

### Function Declarations

```
FuncDecl       = "func" IDENT "(" FuncParamList? ")" Type? FuncBody

FuncParamList  = FuncParam ("," FuncParam)*

FuncParam      = IDENT Type

FuncBody       = Expr
               | "{" FuncStmt* ReturnStmt? "}"

FuncStmt       = LocalVar | AssignStmt | ToggleStmt | MethodCallStmt
               | CallStmt | EmitStmt

LocalVar       = "var" IDENT Type? "=" Expr

ReturnStmt     = "return" Expr?

CallStmt       = IDENT "(" ArgList? ")"
```

Expression form (`Expr`) is for pure single-expression functions; the return type is inferred from the expression. Block form allows local variables, statements, and an optional `return`. Functions with a return type must end with a `return` in block form. Void functions omit the return type and may mutate component state.

### Component Members

```
ComponentMember = "param" IDENT Type? "=" Expr
               | "param" IDENT Type "required"?
               | "prop" IDENT Type ("enum" "(" IDENT ("," IDENT)* ")")?
               | "event" IDENT IDENT
               | "children" ("none" | "one" | "many")
               | ConstDecl
               | VarDecl
               | FuncDecl
               | NodeOrControl
```

### Visual Nodes

```
NodeOrControl  = "if" Expr "{" VisualNode "}"
               | "for" IDENT ("," IDENT)? "in" Expr "{" VisualNode "}"
               | VisualNode

VisualNode     = IDENT ("(" PropList ")")? ("{" NodeBody* "}")?

NodeBody       = "@" IDENT "(" KVList ")"
               | NodeOrControl

PropList       = Prop ("," Prop)*

Prop           = "@" IDENT "=" "{" StmtList "}"
               | "style" "=" StyleLiteral
               | IDENT "=" Expr

StyleLiteral   = "{" (IDENT "=" Expr ("," IDENT "=" Expr)*)? "}"

KVList         = IDENT "=" Expr ("," IDENT "=" Expr)*
```

### Statements

```
StmtList       = Stmt (";" Stmt)* ";"?

Stmt           = AssignStmt | ToggleStmt | MethodCallStmt | CallStmt | EmitStmt

AssignStmt     = LValue "=" Expr
               | LValue "+=" Expr
               | LValue "-=" Expr
               | LValue "*=" Expr
               | LValue "/=" Expr
               | LValue "%=" Expr

ToggleStmt     = LValue "!!"

LValue         = IDENT ("." IDENT | "[" Expr "]")*

MethodCallStmt = LValue "." IDENT "(" ArgList? ")"

EmitStmt       = "@" IDENT "(" ArgList? ")"

ArgList        = Expr ("," Expr)*
```

### Expressions

```
Expr           = Ternary
Ternary        = LogicalOr ("?" Expr ":" Expr)?
LogicalOr      = LogicalAnd ("||" LogicalAnd)*
LogicalAnd     = Equality ("&&" Equality)*
Equality       = Comparison (("==" | "!=") Comparison)*
Comparison     = Addition (("<" | ">" | "<=" | ">=") Addition)*
Addition       = Multiplication (("+" | "-") Multiplication)*
Multiplication = Unary (("*" | "/" | "%") Unary)*
Unary          = ("!" | "-") Unary | Postfix
Postfix        = Primary (Selector | Index | Call)*
Selector       = "." IDENT
Index          = "[" Expr "]"
Call           = "(" ArgList? ")"
Primary        = IDENT | Literal | "(" Expr ")" | StructLiteral | ListLiteral
StructLiteral  = IDENT "{" (IDENT ":" Expr ("," IDENT ":" Expr)* ","?)? "}"
ListLiteral    = "[" (Expr ("," Expr)* ","?)? "]"
```

## Operator Precedence

| Precedence | Operators | Associativity | Description |
| --- | --- | --- | --- |
| 1 | `? :` | right | Ternary |
| 2 | `\|\|` | left | Logical OR |
| 3 | `&&` | left | Logical AND |
| 4 | `==`, `!=` | left | Equality |
| 5 | `<`, `>`, `<=`, `>=` | left | Comparison |
| 6 | `+`, `-` | left | Addition |
| 7 | `*`, `/`, `%` | left | Multiplication |
| 8 | `!`, `-` (unary) | right | Unary |
| 9 | `.`, `[]`, `()` | left | Postfix |

## LL(1) Decision Points

| Position | Lookahead | Decision |
| --- | --- | --- |
| Top-level | keyword | Which declaration to parse |
| After `output` | IDENT vs `{` | Single output vs grouped block |
| After `var` | IDENT vs `(` | Single var vs grouped declaration |
| Inside `{}` children | `if`/`for`/`@`/IDENT | Control, attr, or node |
| Inside component | `const`/`var`/`func`/`param`/IDENT | State, func, param, or visual node |
| After `var` IDENT | `=` vs Type token | Inferred type vs explicit type |
| Inside `()` props | `@`/`style`/IDENT | Event, style literal, or prop |
| After IDENT in type | `<` or not | Generic type or plain type |
| After `for` IDENT | `,` or `in` | Index variable or iterable |
| In statement | IDENT then `!!`/`=`/`.` | Toggle, assign, or method call |
| In statement | `@` | Emit statement |

## Semantics

### Reactivity Model

SNGL uses subscription-based compile-time propagation. At compile time, the compiler:

1. Tracks every binding's dependencies (e.g., `greeting` depends on `name`)
2. Emits update closures for each dependency
3. Generates a minimal `Signal` API: `signal.Set(value)` triggers observers

No virtual DOM or runtime diffing is involved. Assignments to state trigger only the affected update handlers.

### Component Scoping

- State (`var`, `const`) and functions are scoped to the component that contains them
- `param` values are passed from parent to child at instantiation
- Components expand at compile time
- Recursive components are forbidden

### Expression Boundaries

| Context | Boundary |
| --- | --- |
| Inside `()` prop list | `,` or `)` at nesting depth 0 |
| After `if` | `{` at depth 0 (tracking `()` and `[]` only) |
| After `in` in `for` | `{` at depth 0 (tracking `()` and `[]` only) |
| After `=` in const/var | Semicolon (inserted or explicit) |
| After `=` in struct field | Semicolon |
