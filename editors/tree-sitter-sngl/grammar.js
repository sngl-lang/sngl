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
    [$._expression, $._struct_name],
    [$._expression, $._struct_name, $.qualified_name],
    [$._expression, $.anon_struct_field],
    [$._simple_type, $._struct_name],
    [$._simple_type, $._expression],
    [$.func_param, $._simple_type],
    [$._struct_name, $.qualified_name],
    [$.func_type, $.lambda_expression],
    [$.prop_assignment, $._expression],
  ],

  supertypes: ($) => [$._declaration, $._expression, $._statement],

  rules: {
    source_file: ($) => repeat($._declaration_with_terminator),

    _declaration_with_terminator: ($) =>
      seq(optional($.slashdash), $._declaration, $._terminator),

    _terminator: ($) => choice(";", $._automatic_semicolon),

    // ─── Top-level declarations ──────────────────────────────

    slashdash: (_$) => "/-",

    _declaration: ($) =>
      choice(
        $.import_declaration,
        $.output_declaration,
        $.struct_declaration,
        $.enum_declaration,
        $.unit_declaration,
        $.style_declaration,
        $.const_declaration,
        $.var_declaration,
        $.func_declaration,
        $.timer_declaration,
        $.component_declaration,
        $.test_declaration,
      ),

    import_declaration: ($) => seq("import", $.string_literal),

    output_declaration: ($) =>
      seq("output", optional($.kv_list), $.output_group),

    output_group: ($) =>
      seq(
        "{",
        repeat(seq($.output_group_entry, $._terminator)),
        "}",
      ),

    output_group_entry: ($) =>
      seq(
        field("lang", $.identifier),
        "{",
        repeat(
          seq(
            field("platform", $.identifier),
            optional($.kv_list),
            $._terminator,
          ),
        ),
        "}",
      ),

    kv_list: ($) =>
      seq(
        "(",
        commaSep($.kv_pair),
        ")",
      ),

    kv_pair: ($) =>
      seq(
        field("key", $.identifier),
        "=",
        field("value", $.string_literal),
      ),

    struct_declaration: ($) =>
      seq(
        "struct",
        field("name", $.identifier),
        "{",
        repeat(seq($.struct_field, $._terminator)),
        "}",
      ),

    struct_field: ($) =>
      seq(
        field("name", $.identifier),
        field("type", $.type_identifier),
        optional(seq("=", field("default", $._expression))),
      ),

    enum_declaration: ($) =>
      seq(
        "enum",
        field("name", $.identifier),
        "{",
        commaSep($.identifier),
        optional(","),
        "}",
      ),

    unit_declaration: ($) =>
      seq(
        "unit",
        field("name", $.identifier),
        "(",
        commaSep($.unit_suffix),
        ")",
      ),

    unit_suffix: ($) =>
      seq(
        field("name", $.identifier),
        optional(seq("=", field("factor", $._expression))),
      ),

    style_declaration: ($) =>
      seq(
        "style",
        field("name", $.identifier),
        "{",
        repeat(seq($.style_property, $._terminator)),
        "}",
      ),

    style_property: ($) =>
      seq(
        field("name", $.identifier),
        "=",
        field("value", $._expression),
      ),

    // ─── Component ───────────────────────────────────────────

    component_declaration: ($) =>
      seq(
        "component",
        field("name", choice($.qualified_name, $.identifier)),
        optional($.component_params),
        optional(field("children_type", $.type_identifier)),
        "{",
        repeat(seq(optional($.slashdash), $._component_member, $._terminator)),
        "}",
      ),

    component_params: ($) =>
      seq(
        "(",
        commaSep(choice(
          $.component_event_param,
          $.component_binding_param,
          $.component_param,
        )),
        ")",
      ),

    component_param: ($) =>
      seq(
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        optional($.enum_constraint),
        optional(seq("=", field("default", $._expression))),
        optional("required"),
      ),

    enum_constraint: ($) =>
      seq(
        "enum",
        "(",
        commaSep(choice($.identifier, $.integer_literal, $.string_literal)),
        ")",
      ),

    component_binding_param: ($) =>
      seq(
        ":",
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        optional(seq("=", field("default", $._expression))),
      ),

    component_event_param: ($) =>
      seq(
        "@",
        field("name", $.identifier),
        optional(field("payload_type", $.identifier)),
      ),

    // ─── Test declarations ─────────────────────────────────

    test_declaration: ($) =>
      seq(
        "test",
        field("component", $.identifier),
        optional(field("description", $.string_literal)),
        "{",
        repeat(seq($._test_body_member, $._terminator)),
        "}",
      ),

    subtest_declaration: ($) =>
      seq(
        "test",
        optional(field("description", $.string_literal)),
        "{",
        repeat(seq($._test_body_member, $._terminator)),
        "}",
      ),

    _test_body_member: ($) =>
      choice(
        $.subtest_declaration,
        $._statement,
      ),

    _component_member: ($) =>
      choice(
        $.const_declaration,
        $.var_declaration,
        $.func_declaration,
        $.timer_declaration,
        $.platform_block,
        $._node_or_control,
      ),

    platform_block: ($) =>
      seq(
        "platform",
        field("name", $.identifier),
        "{",
        repeat(seq($._node_or_control, $._terminator)),
        "}",
      ),

    const_declaration: ($) =>
      choice(
        seq("const", $.single_const),
        seq("const", "(", repeat(seq($.single_const, $._terminator)), ")"),
      ),

    single_const: ($) =>
      seq(
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        "=",
        field("value", $._expression),
      ),

    var_declaration: ($) =>
      choice(
        seq("var", $.single_var),
        seq("var", "(", repeat(seq($.single_var, $._terminator)), ")"),
      ),

    single_var: ($) =>
      seq(
        field("name", $.identifier),
        optional(
          choice(
            seq(
              field("type", $.type_identifier),
              optional(seq("=", field("init", $._expression))),
            ),
            seq("=", field("init", $._expression)),
          ),
        ),
        optional($.var_modifiers),
      ),

    var_modifiers: ($) =>
      repeat1(
        choice(
          $.extern_modifier,
          $.trigger_modifier,
        ),
      ),

    extern_modifier: (_$) => "extern",

    trigger_modifier: ($) =>
      seq("@", optional($.identifier)),

    // ─── Timers ────────────────────────────────────────────────

    timer_declaration: ($) =>
      seq(
        "timer",
        field("interval", $._expression),
        field("active", $.identifier),
        "{",
        repeat(seq($._statement, $._terminator)),
        "}",
      ),

    // ─── Functions ────────────────────────────────────────────
    // Expression form: func name(params) expr
    // Block form: func name(params) ReturnType { stmts; return expr }

    func_declaration: ($) =>
      seq(
        "func",
        field("name", $.func_name),
        $.func_params,
        choice(
          // Block form with optional return type
          seq(optional(field("return_type", $.type_identifier)), $.func_block),
          // Expression form — body is the expression, no return type, no =
          field("body", $._expression),
        ),
      ),

    func_name: ($) =>
      seq(
        $.identifier,
        optional(seq(".", $.identifier)),
        optional(field("type_params", $.type_param_list)),
      ),

    type_param_list: ($) =>
      seq("<", commaSep1($.identifier), ">"),

    func_params: ($) =>
      seq(
        "(",
        commaSep($.func_param),
        ")",
      ),

    func_param: ($) =>
      seq(
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
      ),

    func_block: ($) =>
      prec(1, seq(
        "{",
        repeat(seq($._func_body_stmt, $._terminator)),
        "}",
      )),

    _func_body_stmt: ($) =>
      choice(
        $.local_var,
        $.return_statement,
        $._statement,
      ),

    local_var: ($) =>
      seq(
        "var",
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        "=",
        field("init", $._expression),
      ),

    return_statement: ($) =>
      seq("return", optional(field("value", $._expression))),

    // ─── Types ───────────────────────────────────────────────

    type_identifier: ($) =>
      choice(
        $._simple_type,
        $.generic_type,
        $.func_type,
        $.inline_enum_type,
      ),

    _simple_type: ($) => choice($.qualified_name, $.identifier),

    generic_type: ($) =>
      seq(
        field("name", $._simple_type),
        "<",
        commaSep1($.type_identifier),
        ">",
      ),

    func_type: ($) =>
      seq(
        "func",
        "(",
        commaSep($.type_identifier),
        ")",
        optional(seq("->", field("return_type", $.type_identifier))),
      ),

    inline_enum_type: ($) =>
      seq("enum", "<", sepBy1("|", $.identifier), ">"),

    // ─── Visual nodes and control flow ───────────────────────

    _node_or_control: ($) =>
      choice($.if_node, $.for_node, $.visual_node),

    if_node: ($) =>
      seq(
        "if",
        field("condition", $._expression),
        "{",
        optional($._node_or_control),
        "}",
      ),

    for_node: ($) =>
      seq(
        "for",
        field("variable", $.identifier),
        optional(seq(",", field("index", $.identifier))),
        "=",
        field("iterable", $._expression),
        "{",
        optional($._node_or_control),
        "}",
        optional(field("else", $.else_block)),
      ),

    else_block: ($) =>
      seq(
        "else",
        $.node_body,
      ),

    visual_node: ($) =>
      prec.right(
        seq(
          field("component", choice($.qualified_name, $.identifier)),
          optional(field("element_id", $.element_ref)),
          optional($.prop_list),
          optional($.node_body),
        ),
      ),

    prop_list: ($) =>
      seq(
        "(",
        optCommaSep($._prop_entry),
        ")",
      ),

    _prop_entry: ($) =>
      choice(
        $.prop_assignment,
        $.prop_binding,
        $.event_handler,
      ),

    prop_binding: ($) =>
      seq(
        ":",
        field("name", $.identifier),
        "=",
        field("value", $._expression),
      ),

    prop_assignment: ($) =>
      seq(
        field("name", $.identifier),
        "=",
        field("value", choice($.anon_struct_literal, $._expression)),
      ),

    event_handler: ($) =>
      seq(
        "@",
        field("name", $.identifier),
        "=",
        "{",
        repeat(seq($._statement, $._terminator)),
        "}",
      ),

    node_body: ($) =>
      seq(
        "{",
        repeat(seq($._node_body_member, $._terminator)),
        "}",
      ),

    _node_body_member: ($) =>
      $._node_or_control,

    // ─── Statements (event handlers) ─────────────────────────

    _statement: ($) =>
      choice(
        $.assignment_statement,
        $.toggle_statement,
        $.emit_statement,
        $._expression,
      ),

    assignment_statement: ($) =>
      seq(
        field("target", $._expression),
        field("operator", $.assignment_operator),
        field("value", $._expression),
      ),

    assignment_operator: (_$) =>
      choice("=", "+=", "-=", "*=", "/=", "%="),

    toggle_statement: ($) =>
      seq(field("target", $._expression), "!!"),

    emit_statement: ($) =>
      seq(
        "@",
        field("name", $.identifier),
        "(",
        commaSep($._expression),
        ")",
      ),

    // ─── Expressions ─────────────────────────────────────────

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
        $.list_literal,
        $.lambda_expression,
        $.identifier,
        $.integer_literal,
        $.float_literal,
        $.string_literal,
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
          field("function", $.identifier),
          "(",
          commaSep($._expression),
          ")",
        ),
      ),

    method_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("receiver", $._expression),
          ".",
          field("method", choice($.identifier, $.event_method)),
          "(",
          commaSep($._expression),
          ")",
        ),
      ),

    event_method: (_$) => token(seq("@", /[a-zA-Z_]\w*/)),

    field_expression: ($) =>
      prec(
        PREC.POSTFIX,
        seq(
          field("operand", $._expression),
          ".",
          field("field", choice($.identifier, $.event_method)),
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
      prec(-1, seq(
        field("name", $._struct_name),
        "{",
        commaSep(choice($.struct_field_value, $.spread_expression)),
        optional(","),
        "}",
      )),

    _struct_name: ($) =>
      seq($.identifier, optional(seq(".", $.identifier))),

    // Anonymous struct literal (type inferred from context): {field=val, ...}
    anon_struct_literal: ($) =>
      prec(-1, seq(
        "{",
        commaSep(choice($.anon_struct_field, $.spread_expression)),
        optional(","),
        "}",
      )),

    struct_field_value: ($) =>
      seq(
        field("name", $.identifier),
        ":",
        field("value", $._expression),
      ),

    anon_struct_field: ($) =>
      seq(
        field("name", $.identifier),
        "=",
        field("value", $._expression),
      ),

    list_literal: ($) =>
      seq("[", commaSep($._list_element), optional(","), "]"),

    _list_element: ($) =>
      choice($.spread_expression, $._expression),

    // Lambda expression: func(params) expr
    lambda_expression: ($) =>
      seq(
        "func",
        "(",
        commaSep($.func_param),
        ")",
        field("body", $._expression),
      ),

    // Spread expression: ...expr
    spread_expression: ($) =>
      seq("...", $._expression),

    // ─── Literals ────────────────────────────────────────────

    qualified_name: ($) => seq($.identifier, ".", $.identifier),

    identifier: (_$) => /[a-zA-Z_][a-zA-Z0-9_]*/,

    integer_literal: (_$) => /[0-9]+/,

    float_literal: (_$) => /[0-9]+\.[0-9]+/,

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

    element_ref: (_$) => token(seq("#", /[a-zA-Z_][a-zA-Z0-9_]*/)),

    color_literal: (_$) => /#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?/,

    unit_literal: (_$) => /[0-9]+(\.[0-9]+)?[a-zA-Z]+/,

    true: (_$) => "true",
    false: (_$) => "false",
    null: (_$) => "null",

    // ─── Comments ────────────────────────────────────────────

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
 * List with optional comma separators (zero or more).
 * @param {RuleOrLiteral} rule
 */
function optCommaSep(rule) {
  return repeat(seq(rule, optional(",")));
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
