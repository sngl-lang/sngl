// The compiler's grammar is internal/parser/sngl.ebnf; this one follows it by
// hand, and sources_test.go holds it to every .sngl file in the repository.
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
    $._i18n_string_content,
  ],

  extras: ($) => [/\s/, $.line_comment, $.block_comment],

  word: ($) => $.identifier,

  conflicts: ($) => [
    [$._expression, $.qualified_name],
    [$._expression, $.struct_literal],
    [$._expression, $.visual_node],
    [$._expression, $.visual_node, $.struct_literal],
    [$.func_name, $.type_identifier],
    [$.func_name],
    // `func(` in a bracket opens a func literal or a func type, and only the
    // body after the list tells them apart.
    [$.func_param, $.type_identifier],
    // A func literal with no body is a func type, `apply[func(int) bool](fs)`.
    [$.anon_func_expression, $.func_type],
    [$._func_body_tail, $.func_type],
    [$.func_param, $._func_type_param],
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
    // `match {` opens the arms of a headless match or a brace literal that is
    // the subject; the headless alternative's dynamic precedence picks the
    // arms, as the compiler's grammar does by keeping a brace out of a head.
    [$._match_arms, $.anon_struct_literal],
    // A map literal's key, `{"a"=1}`, against an assignment in a block.
    [$.assignment_operator, $.anon_struct_field],
  ],

  supertypes: ($) => [$._stmt, $._expression],

  rules: {
    // ─── Generated from EBNF ──────────────────────────────────

    source_file: ($) =>
      repeat(seq(optional("/-"), repeat($.attribute), $._stmt, optional($._terminator))),

    statement_block: ($) =>
      seq("{", repeat(seq(optional("/-"), repeat($.attribute), $._stmt, optional($._terminator))), "}"),

    // `#[builtin("kind")]`, `#[marks.intrinsic]`: a mark on what follows.
    attribute: ($) =>
      seq(
        "#[",
        field("name", choice($.qualified_name, $.identifier)),
        optional(seq("(", optional($._arg_list), ")")),
        "]",
      ),

    // `import ui "sngl:ui"`, `import . "sngl:ui"`; a second path after `=>`
    // replaces the first.
    import_declaration: ($) =>
      seq(
        "import",
        optional(field("alias", choice($.identifier, "."))),
        field("path", $.string_literal),
        optional(seq("=>", field("replacement", $.string_literal))),
      ),

    // `import("c:stdio.h").FILE{}`: a foreign declaration named in a value.
    import_expression: ($) =>
      seq("import", "(", $.string_literal, ")", ".", field("name", $.identifier)),

    struct_declaration: ($) =>
      seq(
        "struct",
        optional(field("name", $.identifier)),
        optional($.type_param_list),
        "{",
        repeat(seq(repeat($.attribute), choice($.struct_field, $.func_declaration), optional($._terminator))),
        "}",
      ),

    struct_field: ($) =>
      seq(
      field("name", $.identifier),
      repeat(seq(",", field("name", $.identifier))),
      field("type", $.type_identifier),
      optional(seq("=", field("default", $._expression))),
    ),

    // Members, which may carry a payload type or a value, and methods,
    // separated by a comma or a line break.
    enum_declaration: ($) =>
      seq(
        "enum",
        optional(field("name", $.identifier)),
        optional($.type_param_list),
        "{",
        optional(seq(
          $._enum_item,
          repeat(seq(choice(",", $._terminator), $._enum_item)),
          optional(choice(",", $._terminator)),
        )),
        "}",
      ),

    _enum_item: ($) =>
      seq(repeat($.attribute), choice($.enum_member, $.func_declaration)),

    enum_member: ($) =>
      prec.right(seq(
        field("name", $.identifier),
        optional(field("payload", $.type_identifier)),
        optional(seq("=", field("value", $._expression))),
      )),

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
      prec.right(seq($.identifier_list, optional($.type_identifier), optional(seq("=", $._expression)))),

    identifier_list: ($) =>
      prec.right(1, seq($.identifier, repeat(seq(",", $.identifier)))),

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
      seq(optional("const"), "func", $.func_name, optional($.type_param_list), optional($.override_target), $._func_tail),

    _func_tail: ($) =>
      prec.right(choice(
      seq("(", optional($._param_list), ")", optional($._func_body_tail)),
      $._func_body_tail
    )),

    // A declaration with no body is a signature whose body comes from
    // elsewhere: an intrinsic, a foreign declaration or a target's override.
    _func_body_tail: ($) =>
      prec.right(choice(
      seq("=>", $._expression),
      seq($.type_identifier, optional($.statement_block)),
      $.statement_block
    )),

    func_name: ($) =>
      choice(
      seq(field("receiver", $.identifier), optional($.receiver_type_params), ".", field("name", $.identifier)),
      field("name", $.identifier)
    ),

    // A receiver's list restates the receiver's names, with no constraints:
    // `func list[T].map[U any]`.
    receiver_type_params: ($) =>
      seq("[", $.identifier, repeat(seq(",", $.identifier)), "]"),

    type_param_list: ($) =>
      seq("[", $.type_param, repeat(seq(",", $.type_param)), "]"),

    // The target an override implements, in the bracket after the name it
    // overrides: `component ui.vbox[platform]`, `func f[html.platform]`. A
    // type parameter names a constraint, which is what tells the two apart.
    override_target: ($) =>
      seq("[", field("target", $.identifier), optional(seq(".", field("target", $.identifier))), "]"),

    // Every declared type parameter names a constraint, `any` when it says
    // nothing, and may carry a default.
    type_param: ($) =>
      seq(
      field("name", $.identifier),
      field("constraint", $.type_identifier),
      optional(seq("=", field("default", $.type_identifier)))
    ),

    // One list for every param-like site: a function's parameters, a lambda's,
    // a handler's, a component's props, and the names a slot population binds.
    // The `:` and `@` forms are legal only in a component declaration, which
    // the compiler says rather than the grammar.
    _param_list: ($) =>
      seq($.func_param, repeat(seq(",", $.func_param)), optional(",")),

    func_param: ($) =>
      prec.right(seq(repeat($.attribute), choice(
      seq(
        optional(choice(":", "const")),
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        optional(seq("=", field("default", $._expression)))
      ),
      // An event declares the parameters its handlers receive as a function
      // declares its own: `@pick(index int, label string)`. As a function's,
      // the parens may be left off when there are none.
      seq(
        "@",
        field("name", $.identifier),
        optional(field("params", $.event_params))
      ),
      // A parameter that is a type and no name: a func type's, written as a
      // func literal with no body.
      field("type", choice($.func_type, $.component_type, $.variadic_type, $.struct_declaration, $.enum_declaration)),
    ))),

    event_params: ($) =>
      seq("(", optional($._param_list), ")"),

    // prec.right resolves the shift/reduce conflict the optional block creates
    // at `component X • {` in favour of the shift; without it
    // `tree-sitter generate` fails outright.
    component_declaration: ($) =>
      prec.right(seq(
      optional("const"),
      "component",
      optional(seq(field("namespace", $.identifier), ".")),
      field("name", $.identifier),
      optional($.type_param_list),
      optional($.override_target),
      optional(seq("(", optional($._param_list), ")")),
      optional($.type_identifier),
      optional($.statement_block)
    )),

    if_node: ($) =>
      seq(
      "if",
      $._expression,
      $.statement_block,
      optional(seq("else", choice($.if_node, $.statement_block)))
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

    // A match is a statement and an expression, as in the compiler's grammar,
    // and an arm is a block or `=> value` in either position. The headless
    // form takes a dynamic precedence over a subject for the reason the
    // headless `for` does: a `{` right after `match` opens the arms.
    // `match var v = subject` names the subject for the arms, as a `for`
    // head names its element.
    match_expression: ($) =>
      choice(
      prec.dynamic(1, seq("match", $._match_arms)),
      seq(
        "match",
        optional(seq("var", field("name", $.identifier), "=")),
        field("subject", $._expression),
        $._match_arms
      )
    ),

    // Never two terminators in a row: the automatic one is zero-width, and a
    // rule that took any number of them would take it forever.
    _match_arms: ($) =>
      seq(
      "{",
      optional(seq($.match_arm, repeat(seq($._terminator, $.match_arm)), optional($._terminator))),
      "}"
    ),

    match_arm: ($) =>
      seq(
      choice("else", seq(field("pattern", $._expression), repeat(seq(",", field("pattern", $._expression))))),
      choice($.statement_block, seq("=>", field("value", $._expression)))
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
    // A key that is not a name makes the literal a map's, `{"a"=1}`.
    anon_struct_field: ($) =>
      choice(
      seq("...", $._expression),
      seq(field("name", $._expression), "=", field("value", $._expression)),
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
      prec.right(choice(
        seq("func", "(", optional($._param_list), ")", optional($._func_body_tail)),
        seq("func", choice(seq("=>", $._expression), seq(optional($.type_identifier), $.statement_block))),
      )),

    type_identifier: ($) =>
      choice(
      // A bracket right after a type is read as its arguments, as the
      // compiler's grammar reads it.
      prec.right(seq(
        $.identifier,
        optional(choice(
        seq(".", $.identifier, optional($.type_arguments)),
        $.type_arguments
      ))
      )),
      // A slot's type. The parenthesised list is what the slot is invoked
      // with; the trailing type is the tree it accepts. Parens are optional on
      // both keyword forms, which is unambiguous because "(" cannot begin a
      // type.
      $.component_type,
      $.func_type,
      $.variadic_type,
      $.struct_declaration,
      $.enum_declaration,
      $.unit_declaration
    ),

    component_type: ($) =>
      prec.right(seq(
      "component",
      optional(seq("(", optional($._func_type_params), ")")),
      optional(field("tree", $.type_identifier))
    )),

    // `...T` — a count bound written as a type prefix. On a slot it is the
    // children a caller writes bare.
    variadic_type: ($) => prec.right(seq("...", $.type_identifier)),

    type_arguments: ($) =>
      seq("[", $._type_list, "]"),

    _type_list: ($) =>
      seq($.type_identifier, repeat(seq(",", $.type_identifier))),

    // A func or component type may name its parameters, `func(s string)`.
    _func_type_params: ($) =>
      seq($._func_type_param, repeat(seq(",", $._func_type_param))),

    _func_type_param: ($) =>
      choice($.type_identifier, seq(field("name", $.identifier), $.type_identifier)),

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
        $.increment_statement,
        $.visual_node,
        $._expression,
      ),

    increment_statement: ($) =>
      seq(field("target", $._expression), field("operator", choice("++", "--"))),

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
          // A component's type arguments, `context[option[User]] #who(null)`.
          optional(seq("[", field("type_argument", $._index_element), repeat(seq(",", field("type_argument", $._index_element))), "]")),
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
        $.match_expression,
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
        $.import_expression,
        $.identifier,
        $.integer_literal,
        $.float_literal,
        $.string_literal,
        $.triple_string_literal,
        $.i18n_string_literal,
        $.i18n_triple_string_literal,
        $.raw_string_literal,
        $.element_ref,
        $.color_literal,
        $.unit_literal,
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
          field("operator", choice("!", "-", "&", "*", "const")),
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
          // An index, or a generic declaration's type arguments,
          // `first[int]`, `pair[int, string]`: the compiler decides which.
          field("index", $._index_element),
          repeat(seq(",", field("index", $._index_element))),
          "]",
        ),
      ),

    parenthesized_expression: ($) => seq("(", $._expression, ")"),

    // What a bracket holds: an index, or a type argument, which may be any
    // type -- `func(int) bool`, `struct {}` -- as well as the name-led ones an
    // expression already spells.
    _index_element: ($) =>
      choice(
        $._expression,
        $.func_type,
        $.struct_declaration,
        $.component_type,
        $.variadic_type,
        $.enum_declaration,
      ),

    // A func literal with no body is this, which dynamic precedence says.
    func_type: ($) =>
      prec.dynamic(1, prec.right(seq(
        "func",
        optional(seq("(", optional($._func_type_params), ")")),
        optional($.type_identifier)
      ))),

    struct_literal: ($) =>
      seq(
        field("name", choice($.qualified_name, $.identifier)),
        optional(seq("[", field("type_argument", $._index_element), repeat(seq(",", field("type_argument", $._index_element))), "]")),
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

    // `$"You have {n, plural, one{# item} other{# items}}"`: a translatable
    // string, whose placeholders take an ICU format and its cases.
    i18n_string_literal: ($) =>
      seq(
        '$"',
        repeat(choice($._i18n_string_content, $.i18n_placeholder)),
        '"',
      ),

    i18n_placeholder: ($) =>
      seq(
        $._string_interpolation_start,
        field("value", $._expression),
        optional(seq(
          ",",
          field("format", $.identifier),
          optional(seq(",", choice(field("style", $.identifier), repeat1($.i18n_case)))),
        )),
        $._string_interpolation_end,
      ),

    i18n_case: ($) =>
      seq(
        field("selector", choice($.identifier, seq("=", $.integer_literal))),
        field("body", $.i18n_case_body),
      ),

    // A case's text, whose placeholders nest one level deep.
    i18n_case_body: (_$) => token(/\{([^{}]|\{[^{}]*\})*\}/),

    i18n_triple_string_literal: (_$) => token(seq('$"""', /("?"?([^"\\]|\\.))*/, '"""')),

    triple_string_literal: (_$) => /"""("?"?([^"\\]|\\.))*"""/,

    raw_string_literal: (_$) => token(seq('`', /[^`]*/, '`')),

    plain_string: (_$) => token(seq('"', repeat(choice(/[^"\\]/, /\\./)), '"')),

    element_ref: (_$) => token(seq("#", /[a-zA-Z_][a-zA-Z0-9_]*/)),

    // Color literals must take precedence over element_ref when the
    // content is exactly 6 or 8 hex digits (e.g. #ff0000 also matches
    // element_ref since 'ff0000' satisfies [a-zA-Z][a-zA-Z0-9]*).
    color_literal: (_$) => token(prec(1, /#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?/)),

    unit_literal: (_$) => /[0-9][0-9_]*(\.[0-9][0-9_]*)?[a-zA-Z]+/,

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
