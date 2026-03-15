; Indent after opening braces
[
  (struct_declaration)
  (enum_declaration)
  (style_declaration)
  (styles_declaration)
  (component_declaration)
  (output_group)
  (node_body)
  (if_node)
  (for_node)
] @indent.begin

; Dedent at closing braces
"}" @indent.end
")" @indent.end

; Branches (for else-like constructs if added later)
; Currently SNGL has no else, but this is future-proof

; Indent inside grouped declarations
(var_declaration "(" @indent.begin)
(const_declaration "(" @indent.begin)
(computed_declaration "(" @indent.begin)

; Keep indent for continuation lines in prop lists
(prop_list "(" @indent.begin)
(kv_list "(" @indent.begin)
