; Keywords
[
  "import"
  "output"
  "struct"
  "enum"
  "unit"
  "style"
  "component"
  "platform"
  "const"
  "var"
  "if"
  "for"
  "func"
  "test"
  "return"
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
(enum_constraint "enum" @keyword)

; Declarations
(import_declaration (string_literal) @string.special)
(struct_declaration name: (identifier) @type.definition)
(enum_declaration name: (identifier) @type.definition)
(unit_declaration name: (identifier) @type.definition)
(style_declaration name: (identifier) @type.definition)
(component_declaration name: (identifier) @type.definition)
(component_declaration name: (qualified_name) @type.definition)
(test_declaration component: (identifier) @type)
(test_declaration description: (string_literal) @string)
(subtest_declaration description: (string_literal) @string)
(platform_block name: (identifier) @constant)

; Component params
(component_param name: (identifier) @variable.parameter)
(component_binding_param ":" @punctuation.special)
(component_binding_param name: (identifier) @variable.parameter)
(component_event_param "@" @punctuation.special)
(component_event_param name: (identifier) @property)

; Variables
(var_declaration) @keyword
(const_declaration) @keyword
(single_var name: (identifier) @variable)
(single_const name: (identifier) @variable)

; Struct fields
(struct_field name: (identifier) @property)
(struct_field_value name: (identifier) @property)
(style_property name: (identifier) @property)

; Visual nodes
(visual_node component: (identifier) @tag)
(visual_node component: (qualified_name) @tag)
(prop_assignment name: (identifier) @property)
(prop_binding ":" @punctuation.special)
(prop_binding name: (identifier) @property)
(event_handler "@" @punctuation.special)
(event_handler name: (identifier) @property)
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
(lambda_expression "=>" @operator)

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
