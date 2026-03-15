; Keywords
[
  "import"
  "output"
  "struct"
  "enum"
  "unit"
  "style"
  "styles"
  "component"
  "param"
  "prop"
  "event"
  "children"
  "const"
  "var"
  "computed"
  "if"
  "for"
  "in"
  "func"
] @keyword

(extern_modifier) @keyword
(trigger_modifier) @keyword

; Literal keywords
(true) @constant.builtin
(false) @constant.builtin
(null) @constant.builtin

; Types
(type_identifier) @type
(generic_type name: (_) @type)
(func_type "func" @keyword)
(inline_enum_type "enum" @keyword)

; Declarations
(import_declaration (string_literal) @string.special)
(struct_declaration name: (identifier) @type.definition)
(enum_declaration name: (identifier) @type.definition)
(unit_declaration name: (identifier) @type.definition)
(style_declaration name: (identifier) @type.definition)
(component_declaration name: (identifier) @type.definition)

; Component members
(param_declaration name: (identifier) @variable.parameter)
(prop_declaration name: (identifier) @property)
(event_declaration name: (identifier) @property)
(children_declaration policy: (identifier) @constant)

; Variables
(var_declaration) @keyword
(const_declaration) @keyword
(computed_declaration) @keyword
(single_var name: (identifier) @variable)
(single_const name: (identifier) @variable)
(single_computed name: (identifier) @variable)

; Struct fields
(struct_field name: (identifier) @property)
(struct_field_value name: (identifier) @property)
(style_property name: (identifier) @property)
(style_prop_def name: (identifier) @property)

; Visual nodes
(visual_node component: (identifier) @tag)
(prop_assignment name: (identifier) @property)
(event_handler "@" @punctuation.special)
(event_handler name: (identifier) @property)
(attr_node "@" @punctuation.special)
(attr_node name: (identifier) @property)
(style_block "style" @keyword)

; Statements
(assignment_statement operator: (_) @operator)
(toggle_statement "!!" @operator)
(emit_statement "@" @punctuation.special)
(emit_statement name: (identifier) @function)

; Expressions
(call_expression function: (identifier) @function)
(method_expression method: (identifier) @function.method)
(field_expression field: (identifier) @property)

; Operators
(binary_expression operator: _ @operator)
(unary_expression operator: _ @operator)
(ternary_expression "?" @operator)
(ternary_expression ":" @operator)

; Literals
(integer_literal) @number
(float_literal) @number.float
(string_literal) @string
(string_interpolation) @string.special
(color_literal) @constant
(unit_literal) @number

; Identifiers (fallback)
(identifier) @variable

; Punctuation
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," "." ":" ";"] @punctuation.delimiter
["@"] @punctuation.special
["=" "+=" "-=" "*=" "/=" "%="] @operator

; Comments
(line_comment) @comment
(block_comment) @comment
