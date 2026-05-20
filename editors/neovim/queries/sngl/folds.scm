; Fold ranges for block constructs.
; struct/enum/unit have their braces directly; component/if/for/etc. wrap
; their body in a statement_block, so we fold the block itself.

(struct_declaration "{" @fold.start "}" @fold.end)
(enum_declaration "{" @fold.start "}" @fold.end)
(unit_declaration "{" @fold.start "}" @fold.end)
(statement_block "{" @fold.start "}" @fold.end)

; Grouped declarations
(var_declaration "(" @fold.start ")" @fold.end)
(const_declaration "(" @fold.start ")" @fold.end)

; Block comments
(block_comment) @fold
