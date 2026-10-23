; Written for Neovim, where a later pattern overrides an earlier one on the
; same text: general captures come first and the specific ones after them.
; A node's children are highlighted after it, so a capture meant to colour a
; name inside a larger node -- a type, a qualified name -- names the identifier.

; Literals. Strings come before identifiers so an interpolation's code is not
; painted as string.
(string_literal) @string
(triple_string_literal) @string
(raw_string_literal) @string
(i18n_string_literal) @string
(i18n_triple_string_literal) @string
(i18n_case_body) @string
(integer_literal) @number
(float_literal) @number.float
(unit_literal) @number
(color_literal) @constant

; Identifiers
(identifier) @variable

; true, false and null are declared names, which a program may shadow.
((identifier) @boolean
  (#any-of? @boolean "true" "false"))
((identifier) @constant.builtin
  (#eq? @constant.builtin "null"))

; Types
(type_identifier (identifier) @type)
(type_identifier (identifier) @module . "." (identifier))
(func_name receiver: (identifier) @type)
(receiver_type_params (identifier) @type.parameter)
(type_param name: (identifier) @type.parameter)
(struct_literal name: (identifier) @type)
(struct_literal name: (qualified_name (identifier) @type))
(struct_literal name: (qualified_name . (identifier) @module))

; Declarations
(struct_declaration name: (identifier) @type.definition)
(enum_declaration name: (identifier) @type.definition)
(unit_declaration name: (identifier) @type.definition)
(component_declaration name: (identifier) @type.definition)
(component_declaration namespace: (identifier) @module)
(override_target target: (identifier) @module)
(struct_field name: (identifier) @property)
(enum_member name: (identifier) @constant)
(var_spec (identifier_list (identifier) @variable))
(const_spec (identifier_list (identifier) @constant))

; Functions
(func_declaration (func_name name: (identifier) @function))
(func_param name: (identifier) @variable.parameter)
(call_expression function: (identifier) @function.call)
(call_expression function: (field_expression field: (identifier) @function.method.call))
(method_expression method: (identifier) @function.method.call)
(var_handler . (identifier) @function)
(event_arg name: (identifier) @function)
(func_param "@" . (identifier) @function)

; Imports
(import_declaration alias: (identifier) @module)
(import_declaration path: (string_literal) @string.special.path)
(import_declaration replacement: (string_literal) @string.special.path)
(import_expression name: (identifier) @type)

; Marks
(attribute name: (identifier) @attribute)
(attribute name: (qualified_name (identifier) @attribute))

; Visual nodes
(visual_node component: (identifier) @tag)
(visual_node component: (qualified_name (identifier) @tag))
(visual_node component: (qualified_name . (identifier) @module))
(visual_node element_id: (element_ref) @tag.attribute)
(element_ref) @tag.attribute

; Arguments and fields
(named_arg name: (identifier) @property)
(binding_arg name: (identifier) @property)
(anon_struct_field name: (identifier) @property)
(field_expression field: (identifier) @property)

; Translatable strings: a placeholder's format, style and case selectors.
(i18n_placeholder format: (identifier) @keyword)
(i18n_placeholder style: (identifier) @constant)
(i18n_case selector: (identifier) @constant)

; Keywords
[
  "struct"
  "enum"
  "unit"
  "component"
  "const"
  "var"
] @keyword

"import" @keyword.import
"func" @keyword.function
"return" @keyword.return
"for" @keyword.repeat
["if" "else" "match"] @keyword.conditional
(break_statement) @keyword.repeat
(continue_statement) @keyword.repeat

; Operators
[
  "+" "-" "*" "/" "%"
  "==" "!=" "<" ">" "<=" ">="
  "&&" "||" "!" "&"
  "=" "+=" "-=" "*=" "/=" "%="
  "++" "--" "!!"
  "?" "=>" "..."
] @operator
(ternary_expression ":" @operator)

; Punctuation
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," "." ";"] @punctuation.delimiter
["@" ":" "#[" "/-"] @punctuation.special

; Comments
(line_comment) @comment
(block_comment) @comment
