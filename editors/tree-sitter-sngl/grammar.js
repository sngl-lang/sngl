// AUTO-GENERATED from internal/v2/parser/sngl.ebnf — do not edit manually.
// Regenerate: go run ./internal/cmd/ebnf2ts internal/v2/parser/sngl.ebnf > editors/tree-sitter-sngl/grammar.js
/// <reference types="tree-sitter-cli/dsl" />
// @ts-check

const PREC = {
  TERNARY: 1,
  OR: 2,
  AND: 3,
  EQUALITY: 4,
  COMPARISON: 5,
  ADDITION: 6,
  MULTIPLICATION: 7,
  UNARY: 8,
  POSTFIX: 9,
};

module.exports = grammar({
  name: "sngl",

  externals: ($) => [
    $._automatic_semicolon,
    $._string_content,
    $._string_interpolation_start, // {
    $._string_interpolation_end, // }
  ],

  extras: ($) => [/\s/, $.line_comment, $.block_comment],

  word: ($) => $.identifier,

  conflicts: ($) => [
    [$._expression, $.qualified_name],
    [$._expression, $.struct_literal],
    [$._expression, $.visual_node],
    [$._expression, $.visual_node, $.struct_literal],
    [$.func_name, $.type_identifier],
    [$.statement_block, $.struct_literal],
    [$.statement_block, $.anon_struct_literal],
    [$.method_expression, $.field_expression],
    [$.anon_struct_field, $.spread_expression],
    [$.anon_struct_field, $._expression],
    // The property shorthand `{a}` against the statement block `{ a }`; the
    // headless-for alternative's dynamic precedence still picks the block.
    [$.anon_struct_field, $.visual_node, $._expression],
    // `struct X {}` opening a statement, versus the same tokens starting a
    // `struct { … }{ … }` value. The Go parser settles it by having no
    // StructDecl in StatementPrimary at all; here the statement wins on
    // dynamic precedence.
    [$._stmt, $.anon_struct_type_literal],
  ],

  supertypes: ($) => [$._stmt, $._expression],

  rules: {
    // ─── Generated from EBNF ──────────────────────────────────

    source_file: ($) =>
      repeat(seq(optional("/-"), $._stmt, optional($._terminator))),

    statement_block: ($) =>
      seq("{", repeat(seq(optional("/-"), $._stmt, optional($._terminator))), "}"),

    import_declaration: ($) =>
      seq("import", optional(seq(field("alias", $.identifier), "=>")), $.string_literal),

    struct_declaration: ($) =>
      seq("struct", optional(field("name", $.identifier)), "{", repeat($.struct_field), "}"),

    struct_field: ($) =>
      seq(
      field("name", $.identifier),
      field("type", $.type_identifier),
      optional(seq("=", field("default", $._expression))),
      $._terminator
    ),

    enum_declaration: ($) =>
      seq("enum", optional(field("name", $.identifier)), "{", optional($._arg_list), "}"),

    unit_declaration: ($) =>
      seq("unit", optional(field("name", $.identifier)), "{", optional($._arg_list), "}"),

    const_declaration: ($) =>
      choice(
      seq("const", $.const_spec),
      seq(
        "const",
        "(",
        repeat(seq($.const_spec, optional(choice(
        ",",
        $._terminator
      )))),
        ")"
      )
    ),

    const_spec: ($) =>
      prec.right(seq($.identifier_list, optional($.type_identifier), "=", $._expression)),

    identifier_list: ($) =>
      prec.right(seq($.identifier, repeat(seq(",", $.identifier)))),

    var_declaration: ($) =>
      choice(
      seq("var", $.var_spec),
      seq(
        "var",
        "(",
        repeat(seq($.var_spec, optional(choice(
        ",",
        $._terminator
      )))),
        ")"
      )
    ),

    var_spec: ($) =>
      prec.right(seq(
      $.identifier_list,
      optional($.type_identifier),
      optional(seq("=", $._expression)),
      repeat($.var_handler)
    )),

    var_handler: ($) =>
      seq(
      "@",
      $.identifier,
      optional(seq("(", optional($._param_list), ")")),
      $.statement_block
    ),

    func_declaration: ($) =>
      seq("func", $.func_name, optional($.type_param_list), $._func_tail),

    _func_tail: ($) =>
      choice(
      seq("(", optional($._param_list), ")", $._func_body_tail),
      $._func_body_tail
    ),

    _func_body_tail: ($) =>
      choice(
      seq("=>", $._expression),
      seq(optional($.type_identifier), $.statement_block)
    ),

    func_name: ($) =>
      choice(
      seq(field("receiver", $.identifier), ".", field("name", $.identifier)),
      field("name", $.identifier)
    ),

    type_param_list: ($) =>
      seq("<", $.identifier, repeat(seq(",", $.identifier)), ">"),

    // One list for every param-like site: a function's parameters, a lambda's,
    // a handler's, a component's props, and the names a slot population binds.
    // The `:` and `@` forms are legal only in a component declaration, which
    // the compiler says rather than the grammar.
    _param_list: ($) =>
      seq($.func_param, repeat(seq(",", $.func_param)), optional(",")),

    func_param: ($) =>
      prec.right(seq(
      optional(choice(":", "@")),
      field("name", $.identifier),
      optional(field("type", $.type_identifier)),
      optional(seq("=", field("default", $._expression)))
    )),

    component_declaration: ($) =>
      seq(
      "component",
      field("name", $.identifier),
      optional(seq("(", optional($._param_list), ")")),
      optional($.type_identifier),
      $.statement_block
    ),

    if_node: ($) =>
      seq(
      "if",
      $._expression,
      $.statement_block,
      optional(seq("else", $.statement_block))
    ),

    // The declaration is optional: without `var` the loop binds nothing and
    // the head is the iterable alone (`for seq.count(3) { }`).
    //
    // The headless `for { }` is its own alternative, at a higher dynamic
    // precedence: a `{` right after `for` could open the body or a brace
    // literal that is the head, and it is always the body. The compiler's
    // grammar says the same thing by keeping a brace literal out of a head
    // expression -- CondPrimary in internal/parser/sngl.ebnf -- which is what
    // makes that grammar LL(1) with the head optional.
    //
    // What a head that *is* there means -- an iterable to walk or a condition
    // to test -- is a matter of its type, which no grammar decides.
    for_node: ($) =>
      choice(
      prec.dynamic(1, seq(
        "for",
        $.statement_block,
        optional(seq("else", $.statement_block))
      )),
      seq(
      "for",
      optional(seq(
        "var",
        optional("&"),
        $.identifier,
        optional(seq(",", optional("&"), $.identifier)),
        "="
      )),
      $._expression,
      $.statement_block,
      optional(seq("else", $.statement_block))
    )
    ),

    assignment_operator: ($) =>
      choice(
      "=",
      "+=",
      "-=",
      "*=",
      "/=",
      "%="
    ),

    _list_body: ($) =>
      optional(seq($._list_element, repeat(seq(",", $._list_element)))),

    _list_element: ($) =>
      choice(
      seq("...", $._expression),
      $._expression
    ),

    _struct_lit_body: ($) =>
      seq(
      "{",
      optional(seq(
      $.anon_struct_field,
      repeat(seq(choice(
      ",",
      $._terminator
    ), $.anon_struct_field)),
      optional(choice(
      ",",
      $._terminator
    ))
    )),
      "}"
    ),

    anon_struct_literal: ($) =>
      seq(
      "{",
      optional(seq(
      $.anon_struct_field,
      repeat(seq(choice(
      ",",
      $._terminator
    ), $.anon_struct_field)),
      optional(choice(
      ",",
      $._terminator
    ))
    )),
      "}"
    ),

    // A bare name is the property shorthand `{a, b}`, which means `{a = a}`.
    anon_struct_field: ($) =>
      choice(
      seq("...", $._expression),
      seq(field("name", $.identifier), "=", field("value", $._expression)),
      field("name", $.identifier)
    ),

    // `struct { … }{ … }`: the value of an inline anonymous type.
    anon_struct_type_literal: ($) =>
      prec.dynamic(-1, seq(
        $.struct_declaration,
        "{",
        commaSep(choice($.anon_struct_field, $.spread_expression)),
        optional(","),
        "}",
      )),

    anon_func_expression: ($) =>
      seq("func", optional(seq("(", optional($._param_list), ")")), $._func_body_tail),

    type_identifier: ($) =>
      choice(
      seq(
        $.identifier,
        optional(choice(
        seq(".", $.identifier),
        seq("<", $.type_identifier, ">")
      ))
      ),
      // A slot's type. The parenthesised list is what the slot is invoked
      // with; the trailing type is the tree it accepts. Parens are optional on
      // both keyword forms, which is unambiguous because "(" cannot begin a
      // type.
      $.component_type,
      prec.right(seq(
        "func",
        optional(seq("(", optional($._type_list), ")")),
        optional($.type_identifier)
      )),
      $.variadic_type,
      $.enum_declaration
    ),

    component_type: ($) =>
      prec.right(seq(
      "component",
      optional(seq("(", optional($._type_list), ")")),
      optional(field("tree", $.type_identifier))
    )),

    // `...T` — a count bound written as a type prefix. On a slot it is the
    // children a caller writes bare.
    variadic_type: ($) => prec.right(seq("...", $.type_identifier)),

    _type_list: ($) =>
      seq($.type_identifier, repeat(seq(",", $.type_identifier))),

    // ─── Statements (hand-crafted for named nodes) ────────────

    _terminator: ($) => choice(";", $._automatic_semicolon),

    _stmt: ($) =>
      choice(
        $.import_declaration,
        $.struct_declaration,
        $.enum_declaration,
        $.unit_declaration,
        $.const_declaration,
        $.var_declaration,
        $.func_declaration,
        $.component_declaration,
        $.return_statement,
        $.break_statement,
        $.continue_statement,
        $.if_node,
        $.for_node,
        $.assignment_statement,
        $.toggle_statement,
        $.visual_node,
        $._expression,
      ),

    return_statement: ($) =>
      prec.right(seq("return", optional($._expression))),

    break_statement: ($) => "break",

    continue_statement: ($) => "continue",

    _visual_or_stmt: ($) =>
      choice(
        $.assignment_statement,
        $.toggle_statement,
        $.visual_node,
        $._expression,
      ),

    assignment_statement: ($) =>
      seq(
        field("target", $._expression),
        field("operator", $.assignment_operator),
        field("value", $._expression),
      ),

    toggle_statement: ($) =>
      seq(field("target", $._expression), "!!"),

    visual_node: ($) =>
      prec.right(
        seq(
          field("component", choice($.qualified_name, $.identifier)),
          optional(field("element_id", $.element_ref)),
          optional(seq("(", optional($._arg_list), ")")),
          optional($.statement_block),
        ),
      ),

    // ─── Argument list (hand-crafted, no left-factoring) ──────

    _arg_list: ($) =>
      seq(
        $._arg,
        repeat(seq(choice(",", $._terminator), $._arg)),
        optional(choice(",", $._terminator)),
      ),

    _arg: ($) =>
      choice(
        $.binding_arg,
        $.event_arg,
        $.named_arg,
        $.spread_expression,
        $._expression,
      ),

    binding_arg: ($) =>
      seq(
        ":",
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        optional(seq("=", field("default", $._expression))),
      ),

    // Same production as var_handler: the block is not optional, and `@click`
    // with nothing after it is a handler supplied without a body rather than
    // an event named in a value position.
    event_arg: ($) =>
      seq(
        "@",
        field("name", $.identifier),
        optional(seq("(", optional($._param_list), ")")),
        $.statement_block,
      ),

    named_arg: ($) =>
      seq(
        field("name", $.identifier),
        "=",
        field("value", $._expression),
      ),

    // ─── Expressions (hand-crafted, precedence-based) ─────────

    _expression: ($) =>
      choice(
        $.ternary_expression,
        $.binary_expression,
        $.unary_expression,
        $.call_expression,
        $.method_expression,
        $.field_expression,
        $.index_expression,
        $.parenthesized_expression,
        $.struct_literal,
        $.anon_struct_literal,
        $.anon_struct_type_literal,
        $.list_literal,
        $.anon_func_expression,
        $.identifier,
        $.integer_literal,
        $.float_literal,
        $.string_literal,
        $.triple_string_literal,
        $.raw_string_literal,
        $.element_ref,
        $.color_literal,
        $.unit_literal,
        $.true,
        $.false,
        $.null,
      ),

    ternary_expression: ($) =>
      prec.right(
        PREC.TERNARY,
        seq(
          field("condition", $._expression),
          "?",
          field("consequence", $._expression),
          ":",
          field("alternative", $._expression),
        ),
      ),

    binary_expression: ($) =>
      choice(
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
            seq(
              field("left", $._expression),
              // @ts-ignore
              field("operator", op),
              field("right", $._expression),
            ),
          ),
        ),
      ),

    unary_expression: ($) =>
      prec(
        PREC.UNARY,
        seq(
          field("operator", choice("!", "-")),
          field("operand", $._expression),
        ),
      ),

    call_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("function", $._expression),
          "(",
          optional($._arg_list),
          ")",
        ),
      ),

    method_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("receiver", $._expression),
          ".",
          field("method", $.identifier),
          "(",
          optional($._arg_list),
          ")",
        ),
      ),

    field_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("operand", $._expression),
          ".",
          field("field", $.identifier),
        ),
      ),

    index_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("operand", $._expression),
          "[",
          field("index", $._expression),
          "]",
        ),
      ),

    parenthesized_expression: ($) => seq("(", $._expression, ")"),

    struct_literal: ($) =>
      seq(
        field("name", choice($.qualified_name, $.identifier)),
        "{",
        commaSep(choice($.anon_struct_field, $.spread_expression)),
        optional(","),
        "}",
      ),

    list_literal: ($) =>
      seq("[", commaSep($._list_element), optional(","), "]"),

    spread_expression: ($) =>
      seq("...", $._expression),

    // ─── Literals ──────────────────────────────────────────────

    qualified_name: ($) => seq($.identifier, ".", $.identifier),

    identifier: (_$) => /[a-zA-Z_][a-zA-Z0-9_]*/,

    integer_literal: (_$) => /[0-9][0-9_]*/,

    float_literal: (_$) => /[0-9][0-9_]*\.[0-9][0-9_]*/,

    string_literal: ($) =>
      seq(
        '"',
        repeat(
          choice(
            $._string_content,
            $.string_interpolation,
          ),
        ),
        '"',
      ),

    string_interpolation: ($) =>
      seq(
        $._string_interpolation_start,
        $._expression,
        $._string_interpolation_end,
      ),

    triple_string_literal: (_$) => /"""("?"?([^"\\]|\\.))*"""/,

    raw_string_literal: (_$) => token(seq('`', /[^`]*/, '`')),

    plain_string: (_$) => token(seq('"', repeat(choice(/[^"\\]/, /\\./)), '"')),

    element_ref: (_$) => token(seq("#", /[a-zA-Z_][a-zA-Z0-9_]*/)),

    // Color literals must take precedence over element_ref when the
    // content is exactly 6 or 8 hex digits (e.g. #ff0000 also matches
    // element_ref since 'ff0000' satisfies [a-zA-Z][a-zA-Z0-9]*).
    color_literal: (_$) => token(prec(1, /#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?/)),

    unit_literal: (_$) => /[0-9][0-9_]*(\.[0-9][0-9_]*)?[a-zA-Z]+/,

    true: (_$) => "true",
    false: (_$) => "false",
    null: (_$) => "null",

    // ─── Comments ──────────────────────────────────────────────

    line_comment: (_$) => token(seq("//", /.*/)),

    block_comment: (_$) =>
      token(seq("/*", /[^*]*\*+([^/*][^*]*\*+)*/, "/")),
  },
});

/**
 * Comma-separated list (zero or more).
 * @param {RuleOrLiteral} rule
 */
function commaSep(rule) {
  return optional(commaSep1(rule));
}

/**
 * Comma-separated list (one or more).
 * @param {RuleOrLiteral} rule
 */
function commaSep1(rule) {
  return seq(rule, repeat(seq(",", rule)));
}

/**
 * Separated-by list (one or more).
 * @param {RuleOrLiteral} separator
 * @param {RuleOrLiteral} rule
 */
function sepBy1(separator, rule) {
  return seq(rule, repeat(seq(separator, rule)));
}
