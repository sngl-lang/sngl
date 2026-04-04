; Indent after opening braces
[
  (struct_declaration)
  (enum_declaration)
  (style_declaration)
  (component_declaration)
  (test_declaration)
  (subtest_declaration)
  (output_group)
  (node_body)
  (if_node)
  (for_node)
  (platform_block)
] @indent.begin

; Dedent at closing braces
"}" @indent.end
")" @indent.end

; Indent inside grouped declarations
(var_declaration "(" @indent.begin)
(const_declaration "(" @indent.begin)

; Keep indent for continuation lines in prop lists
(prop_list "(" @indent.begin)
(component_params "(" @indent.begin)
(kv_list "(" @indent.begin)
