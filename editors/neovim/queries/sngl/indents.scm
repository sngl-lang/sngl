; Indent after opening braces
[
  (struct_declaration)
  (enum_declaration)
  (unit_declaration)
  (component_declaration)
  (statement_block)
  (if_node)
  (for_node)
] @indent.begin

; Dedent at closing braces
"}" @indent.end
")" @indent.end

; Indent inside grouped declarations
(var_declaration "(" @indent.begin)
(const_declaration "(" @indent.begin)
