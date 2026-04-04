; Fold ranges for block constructs

(struct_declaration "{" @fold.start "}" @fold.end)
(enum_declaration "{" @fold.start "}" @fold.end)
(style_declaration "{" @fold.start "}" @fold.end)
(component_declaration "{" @fold.start "}" @fold.end)
(test_declaration "{" @fold.start "}" @fold.end)
(subtest_declaration "{" @fold.start "}" @fold.end)
(output_group "{" @fold.start "}" @fold.end)
(platform_block "{" @fold.start "}" @fold.end)

; Visual node bodies
(node_body "{" @fold.start "}" @fold.end)

; Control flow
(if_node "{" @fold.start "}" @fold.end)
(for_node "{" @fold.start "}" @fold.end)

; Grouped declarations
(var_declaration "(" @fold.start ")" @fold.end)
(const_declaration "(" @fold.start ")" @fold.end)

; Component params
(component_params "(" @fold.start ")" @fold.end)

; Block comments
(block_comment) @fold
