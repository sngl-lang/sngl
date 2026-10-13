; Keywords
[
  "import"
  "struct"
  "enum"
  "unit"
  "component"
  "const"
  "var"
  "if"
  "else"
  "for"
  "func"
  "return"
] @keyword

; break and continue: the rule is the bare token, so tree-sitter hides the
; anonymous "break"/"continue" and only the named statement node is queryable.
(break_statement) @keyword
(continue_statement) @keyword

; Literal keywords
(true) @constant.builtin
(false) @constant.builtin
(null) @constant.builtin

; Types
(type_identifier) @type

; Imports
(import_declaration (string_literal) @string.special)
(import_declaration (identifier) @variable)

; Declarations: use name: field label for the declared name.
(struct_declaration name: (identifier) @type.definition)
(enum_declaration name: (identifier) @type.definition)
(unit_declaration name: (identifier) @type.definition)
(component_declaration name: (identifier) @type.definition)

; Struct fields: use name: field label for the field name.
(struct_field name: (identifier) @property)

; Component params (positional, named, binding, or event form).
(component_param name: (identifier) @variable.parameter)
(component_param "@" @punctuation.special)
(component_param ":" @punctuation.special)

; var / const specs
(var_spec (identifier_list (identifier) @variable))
(const_spec (identifier_list (identifier) @variable))

; var handlers: var x @set { ... }
(var_handler "@" @punctuation.special)
(var_handler . (identifier) @function)

; Visual nodes
(visual_node component: (identifier) @tag)
(visual_node component: (qualified_name) @tag)
(visual_node element_id: (element_ref) @tag)

; Arguments inside (...) on visual nodes / calls
(named_arg name: (identifier) @property)
(binding_arg name: (identifier) @property)
(binding_arg ":" @punctuation.special)
(event_arg name: (identifier) @function)
(event_arg "@" @punctuation.special)

; Anonymous struct literal fields { foo = 1 }
(anon_struct_field name: (identifier) @property)

; Statements
(assignment_statement operator: (assignment_operator) @operator)
(toggle_statement "!!" @operator)

; Emit expression: @click(...)
(emit_expression (event_method) @function)

; Functions
(func_declaration (func_name name: (identifier) @function))
(func_param name: (identifier) @variable.parameter)

; Expressions
(call_expression function: (identifier) @function)
(method_expression method: (identifier) @function.method)
(method_expression method: (event_method) @function.method)
(field_expression field: (identifier) @property)
(field_expression field: (event_method) @property)

; Operators
(binary_expression "+" @operator)
(binary_expression "-" @operator)
(binary_expression "*" @operator)
(binary_expression "/" @operator)
(binary_expression "%" @operator)
(binary_expression "==" @operator)
(binary_expression "!=" @operator)
(binary_expression "<" @operator)
(binary_expression ">" @operator)
(binary_expression "<=" @operator)
(binary_expression ">=" @operator)
(binary_expression "&&" @operator)
(binary_expression "||" @operator)
(unary_expression operator: _ @operator)
(ternary_expression "?" @operator)
(ternary_expression ":" @operator)
(spread_expression "..." @operator)
(func_declaration "=>" @operator)
(anon_func_expression "=>" @operator)

; Struct literals
(struct_literal name: (identifier) @type)
(struct_literal name: (qualified_name) @type)

; Literals
(integer_literal) @number
(float_literal) @number.float
(string_literal) @string
(triple_string_literal) @string
(raw_string_literal) @string
(string_interpolation) @string.special
(color_literal) @constant
(unit_literal) @number
(element_ref) @tag

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
