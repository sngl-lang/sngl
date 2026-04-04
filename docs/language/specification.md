---
title: "Language Specification"
order: 3
description: "Formal grammar and semantics of the SNGL language"
---

## Grammar

SNGL uses a recursive-descent parser with automatic semicolon insertion.
The following grammar is extracted from the tree-sitter grammar definition.

### Document Structure

```
source_file              = {Declaration_with_terminator)
declaration              = (
        ImportDeclaration,
        OutputDeclaration,
        StructDeclaration,
        EnumDeclaration,
        UnitDeclaration,
        StyleDeclaration,
        ConstDeclaration,
        VarDeclaration,
        FuncDeclaration,
        TimerDeclaration,
        ComponentDeclaration,
        TestDeclaration,
      )
```

### Imports & Outputs

```
import_declaration       = ("import", STRING)
output_declaration       = ("output", [KvList), OutputGroup)
output_group             = (
        "{",
        {(OutputGroupEntry, TERM)),
        "}",
      )
output_group_entry       = (
        field("lang", IDENT),
        "{",
        {
          (
            field("platform", IDENT),
            [KvList),
            TERM,
          ),
        ),
        "}",
      )
```

### Type Declarations

```
struct_declaration       = (
        "struct",
        field("name", IDENT),
        "{",
        {(StructField, TERM)),
        "}",
      )
struct_field             = (
        field("name", IDENT),
        field("type", TypeIdentifier),
        [("=", field("default", Expr))),
      )
enum_declaration         = (
        "enum",
        field("name", IDENT),
        "{",
        commaSep(IDENT),
        [","),
        "}",
      )
unit_declaration         = (
        "unit",
        field("name", IDENT),
        "(",
        commaSep(UnitSuffix),
        ")",
      )
style_declaration        = (
        "style",
        field("name", IDENT),
        "{",
        {(StyleProperty, TERM)),
        "}",
      )
```

### State Declarations

```
const_declaration        = (
        ("const", SingleConst),
        ("const", "(", {(SingleConst, TERM)), ")"),
      )
single_const             = (
        field("name", IDENT),
        [field("type", TypeIdentifier)),
        "=",
        field("value", Expr),
      )
var_declaration          = (
        ("var", SingleVar),
        ("var", "(", {(SingleVar, TERM)), ")"),
      )
single_var               = (
        field("name", IDENT),
        [
          (
            (
              field("type", TypeIdentifier),
              [("=", field("init", Expr))),
            ),
            ("=", field("init", Expr)),
          ),
        ),
        [VarModifiers),
      )
var_modifiers            = {
        (
          ExternModifier,
          TriggerModifier,
        ),
      )
```

### Type Syntax

```
type_identifier          = (
        SimpleType,
        GenericType,
        FuncType,
        InlineEnumType,
      )
simple_type              = (QualifiedName, IDENT)
generic_type             = (
        field("name", SimpleType),
        "<",
        commaSep1(TypeIdentifier),
        ">",
      )
func_type                = (
        "func",
        "(",
        commaSep(TypeIdentifier),
        ")",
        [("->", field("return_type", TypeIdentifier))),
      )
inline_enum_type         = ("enum", "<", sepBy1("|", IDENT), ">")
```

### Functions

```
func_declaration         = (
        "func",
        field("name", FuncName),
        FuncParams,
        (
          // Block form with optional return type
          ([field("return_type", TypeIdentifier)), FuncBlock),
          // Expression form — body is the expression, no return type, no =
          field("body", Expr),
        ),
      )
func_params              = (
        "(",
        commaSep(FuncParam),
        ")",
      )
func_param               = (
        field("name", IDENT),
        [field("type", TypeIdentifier)),
      )
func_block               = prec(1, (
        "{",
        {(FuncBodyStmt, TERM)),
        "}",
      ))
```

### Components

```
component_declaration    = (
        "component",
        field("name", (QualifiedName, IDENT)),
        [ComponentParams),
        [field("children_type", TypeIdentifier)),
        "{",
        {([Slashdash), ComponentMember, TERM)),
        "}",
      )
component_params         = (
        "(",
        commaSep((
          ComponentEventParam,
          ComponentBindingParam,
          ComponentParam,
        )),
        ")",
      )
component_param          = (
        field("name", IDENT),
        [field("type", TypeIdentifier)),
        [EnumConstraint),
        [("=", field("default", Expr))),
        ["required"),
      )
component_binding_param  = (
        ":",
        field("name", IDENT),
        [field("type", TypeIdentifier)),
        [("=", field("default", Expr))),
      )
component_event_param    = (
        "@",
        field("name", IDENT),
        [field("payload_type", IDENT)),
      )
enum_constraint          = (
        "enum",
        "(",
        commaSep((IDENT, INT, STRING)),
        ")",
      )
component_member         = (
        ConstDeclaration,
        VarDeclaration,
        FuncDeclaration,
        TimerDeclaration,
        PlatformBlock,
        NodeOrControl,
      )
platform_block           = (
        "platform",
        field("name", IDENT),
        "{",
        {(NodeOrControl, TERM)),
        "}",
      )
```

### Visual Nodes

```
node_or_control          = (IfNode, ForNode, VisualNode)
if_node                  = (
        "if",
        field("condition", Expr),
        "{",
        [NodeOrControl),
        "}",
      )
for_node                 = (
        "for",
        field("variable", IDENT),
        [(",", field("index", IDENT))),
        "=",
        field("iterable", Expr),
        "{",
        [NodeOrControl),
        "}",
        [field("else", ElseBlock)),
      )
else_block               = (
        "else",
        NodeBody,
      )
visual_node              = prec.right(
        (
          field("component", (QualifiedName, IDENT)),
          [field("element_id", ElementRef)),
          [PropList),
          [NodeBody),
        ),
      )
node_body                = (
        "{",
        {(NodeBodyMember, TERM)),
        "}",
      )
prop_list                = (
        "(",
        optCommaSep(PropEntry),
        ")",
      )
prop_entry               = (
        PropAssignment,
        PropBinding,
        EventHandler,
      )
prop_assignment          = (
        field("name", IDENT),
        "=",
        field("value", (AnonStructLiteral, Expr)),
      )
prop_binding             = (
        ":",
        field("name", IDENT),
        "=",
        field("value", Expr),
      )
event_handler            = (
        "@",
        field("name", IDENT),
        "=",
        "{",
        {(Stmt, TERM)),
        "}",
      )
```

### Statements

```
statement                = (
        AssignmentStatement,
        ToggleStatement,
        EmitStatement,
        Expr,
      )
assignment_statement     = (
        field("target", Expr),
        field("operator", AssignmentOperator),
        field("value", Expr),
      )
toggle_statement         = (field("target", Expr), "!!")
emit_statement           = (
        "@",
        field("name", IDENT),
        "(",
        commaSep(Expr),
        ")",
      )
return_statement         = ("return", [field("value", Expr)))
```

### Expressions

```
expression               = (
        TernaryExpression,
        BinaryExpression,
        UnaryExpression,
        CallExpression,
        MethodExpression,
        FieldExpression,
        IndexExpression,
        ParenthesizedExpression,
        StructLiteral,
        AnonStructLiteral,
        ListLiteral,
        LambdaExpression,
        IDENT,
        INT,
        FLOAT,
        STRING,
        ElementRef,
        COLOR,
        UNIT,
        True,
        False,
        Null,
      )
ternary_expression       = prec.right(
        PREC.TERNARY,
        (
          field("condition", Expr),
          "?",
          field("consequence", Expr),
          ":",
          field("alternative", Expr),
        ),
      )
binary_expression        = (
        ...[
          ["+", PREC.ADDITION],
          ["-", PREC.ADDITION],
          ["*", PREC.MULTIPLICATION],
          ["/", PREC.MULTIPLICATION],
          ["%", PREC.MULTIPLICATION],
          ["==", PREC.EQUALITY],
          ["!=", PREC.EQUALITY],
          ["<", PREC.COMPARISON],
          [">", PREC.COMPARISON],
          ["<=", PREC.COMPARISON],
          [">=", PREC.COMPARISON],
          ["&&", PREC.AND],
          ["||", PREC.OR],
        ].map(([op, prec_val]) =>
          prec.left(
            /** @type {number} */ (prec_val),
            (
              field("left", Expr),
              // @ts-ignore
              field("operator", op),
              field("right", Expr),
            ),
          ),
        ),
      )
unary_expression         = prec(
        PREC.UNARY,
        (
          field("operator", ("!", "-")),
          field("operand", Expr),
        ),
      )
call_expression          = prec(
        PREC.POSTFIX,
        (
          field("function", IDENT),
          "(",
          commaSep(Expr),
          ")",
        ),
      )
method_expression        = prec(
        PREC.POSTFIX,
        (
          field("receiver", Expr),
          ".",
          field("method", (IDENT, EventMethod)),
          "(",
          commaSep(Expr),
          ")",
        ),
      )
field_expression         = prec(
        PREC.POSTFIX,
        (
          field("operand", Expr),
          ".",
          field("field", (IDENT, EventMethod)),
        ),
      )
index_expression         = prec(
        PREC.POSTFIX,
        (
          field("operand", Expr),
          "[",
          field("index", Expr),
          "]",
        ),
      )
parenthesized_expression = ("(", Expr, ")")
lambda_expression        = (
        "func",
        "(",
        commaSep(FuncParam),
        ")",
        field("body", Expr),
      )
```

### Literals

```
struct_literal           = (
        field("name", StructName),
        "{",
        commaSep((StructFieldValue, SpreadExpression)),
        [","),
        "}",
      )
anon_struct_literal      = (
        "{",
        commaSep((AnonStructField, SpreadExpression)),
        [","),
        "}",
      )
list_literal             = ("[", commaSep(ListElement), [","), "]")
qualified_name           = (IDENT, ".", IDENT)
string_literal           = (
        '"',
        {
          (
            StringContent,
            StringInterpolation,
          ),
        ),
        '"',
      )
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

## Keywords

`component` `const` `else` `enum` `extern` `for` `func` `if` `import` `output` `platform` `return` `struct` `style` `test` `timer` `unit` `var`

Additionally, `true`, `false`, `null`, `in`, `else`, `return`, `required`, `extern`, `platform` are contextual keywords recognized by the parser.

## Semantics

### Reactivity Model

SNGL uses compile-time dependency tracking. The compiler:

1. Tracks every binding's data dependencies
2. Emits update closures for each dependency
3. Generates platform-specific reactive code (e.g., JS Signal API, Go state fields)

No virtual DOM or runtime diffing is involved. Assignments to state trigger only the affected update handlers.

### Component Model

- Components are declared with `component Name(params...) [ChildrenType] { ... }`
- Parameters in `()` define the component's public API
- Props are passed at instantiation: `Name(prop=value)`
- Events use `@` prefix: `@click ClickEvent`
- Bidirectional bindings use `:` prefix: `:value string`
- Children type (after params): `list<component>`, `component`, `option<component>`, or omitted (no children)
- `slot` projects caller's children into the component body
- `platform Name { ... }` blocks provide platform-conditional implementations

### Abstract Components

- Stdlib components in the `sngl` package define abstract APIs (params/events/children)
- Platforms provide implementations via `.sngl` package files
- `component sngl.X()` in a platform package overrides stdlib component X (body-only, inherits API)
- Components with default bodies work on all platforms; pure-abstract components require platform support

### Scoping

- State (`var`, `const`) and functions are scoped to their component
- Component parameters become local variables in the component body
- `for` loop variables are scoped to the loop body
- Imported components are namespaced: `import "widgets"` → `widgets.Button`
- Platform/language packages are namespaced: `html.div`, `android.Card`

### Type System

Built-in types: `int`, `float`, `bool`, `string`, `color`, `date`, `measurement`, `dyn`

Generic types: `list<T>`, `option<T>`

User-defined types: `struct`, `enum`, `unit`
