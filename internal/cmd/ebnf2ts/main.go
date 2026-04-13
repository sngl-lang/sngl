// Command ebnf2ts generates a tree-sitter grammar.js from the v2 EBNF grammar.
//
// Usage: go run ./internal/cmd/ebnf2ts internal/v2/parser/sngl.ebnf > editors/tree-sitter-sngl/grammar.js
package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"unicode"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ebnf2ts <file.ebnf>")
		os.Exit(1)
	}
	prods, err := parseEBNF(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ebnf2ts: %v\n", err)
		os.Exit(1)
	}

	rules := generateRules(prods)

	var buf bytes.Buffer
	tmpl := template.Must(template.New("grammar").Parse(grammarTemplate))
	if err := tmpl.Execute(&buf, rules); err != nil {
		fmt.Fprintf(os.Stderr, "ebnf2ts: template: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(buf.Bytes())
}

// ── EBNF AST ────────────────────────────────────────────────────────────────

type Production struct {
	Name string
	Body Expr
}

type Expr interface{ expr() }

type SeqExpr struct{ Terms []Expr }
type AltExpr struct{ Alts []Expr }
type RepeatExpr struct{ Body Expr }
type OptionalExpr struct{ Body Expr }
type NameExpr struct{ Name string }

func (SeqExpr) expr()      {}
func (AltExpr) expr()      {}
func (RepeatExpr) expr()   {}
func (OptionalExpr) expr() {}
func (NameExpr) expr()     {}

// ── EBNF Parser ─────────────────────────────────────────────────────────────

type parser struct {
	tokens []string
	pos    int
}

func parseEBNF(path string) ([]Production, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Strip comments, join into single string.
	var lines []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	tokens := tokenize(strings.Join(lines, " "))

	var prods []Production
	p := &parser{tokens: tokens}
	for p.pos < len(p.tokens) {
		prod, err := p.parseProduction()
		if err != nil {
			return nil, err
		}
		if prod == nil {
			continue
		}
		// Skip terminal definitions (backtick-quoted values).
		if isTerminalDef(prod.Body) {
			continue
		}
		prods = append(prods, *prod)
	}
	return prods, nil
}

func isTerminalDef(e Expr) bool {
	switch e := e.(type) {
	case NameExpr:
		return strings.HasPrefix(e.Name, "`")
	case SeqExpr:
		return len(e.Terms) == 1 && isTerminalDef(e.Terms[0])
	}
	return false
}

func tokenize(text string) []string {
	var tokens []string
	i := 0
	for i < len(text) {
		ch := text[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		if ch == '`' {
			j := i + 1
			for j < len(text) && text[j] != '`' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(text) {
				j++
			}
			tokens = append(tokens, text[i:j])
			i = j
			continue
		}
		if strings.ContainsRune("=.|()[]{}", rune(ch)) {
			tokens = append(tokens, string(ch))
			i++
			continue
		}
		j := i
		for j < len(text) && !strings.ContainsRune(" \t\r\n=.|()[]{}", rune(text[j])) {
			j++
		}
		if j > i {
			tokens = append(tokens, text[i:j])
		}
		i = j
	}
	return tokens
}

func (p *parser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() string {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) expect(s string) error {
	if p.peek() != s {
		return fmt.Errorf("expected %q, got %q at token %d", s, p.peek(), p.pos)
	}
	p.advance()
	return nil
}

func (p *parser) parseProduction() (*Production, error) {
	if p.pos >= len(p.tokens) {
		return nil, nil
	}
	name := p.advance()
	if err := p.expect("="); err != nil {
		return nil, fmt.Errorf("production %s: %w", name, err)
	}
	body, err := p.parseExpression()
	if err != nil {
		return nil, fmt.Errorf("production %s: %w", name, err)
	}
	if err := p.expect("."); err != nil {
		return nil, fmt.Errorf("production %s: %w", name, err)
	}
	return &Production{Name: name, Body: body}, nil
}

func (p *parser) parseExpression() (Expr, error) {
	first, err := p.parseSequence()
	if err != nil {
		return nil, err
	}
	if p.peek() != "|" {
		return first, nil
	}
	alts := []Expr{first}
	for p.peek() == "|" {
		p.advance()
		alt, err := p.parseSequence()
		if err != nil {
			return nil, err
		}
		alts = append(alts, alt)
	}
	return AltExpr{Alts: alts}, nil
}

func (p *parser) parseSequence() (Expr, error) {
	var terms []Expr
	for {
		t := p.peek()
		if t == "" || t == "." || t == "|" || t == ")" || t == "]" || t == "}" {
			break
		}
		term, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return SeqExpr{Terms: terms}, nil
}

func (p *parser) parseFactor() (Expr, error) {
	switch p.peek() {
	case "(":
		p.advance()
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		return e, p.expect(")")
	case "[":
		p.advance()
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		return OptionalExpr{Body: e}, p.expect("]")
	case "{":
		p.advance()
		e, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		return RepeatExpr{Body: e}, p.expect("}")
	default:
		return NameExpr{Name: p.advance()}, nil
	}
}

// ── Mappings ────────────────────────────────────────────────────────────────

// termMap maps EBNF terminal names to tree-sitter JS expressions.
var termMap = map[string]string{
	"semi": "$._terminator", "ident": "$.identifier",
	"int_lit": "$.integer_literal", "float_lit": "$.float_literal",
	"str_full": "$.string_literal", "triple_full": "$.triple_string_literal",
	"raw_str": "$.raw_string_literal", "color": "$.color_literal",
	"unit_lit": "$.unit_literal", "elem_ref": "$.element_ref",
	"lparen": `"("`, "rparen": `")"`,
	"lbrace": `"{"`, "rbrace": `"}"`,
	"lbracket": `"["`, "rbracket": `"]"`,
	"comma": `","`, "dot": `"."`, "colon": `":"`,
	"assign": `"="`, "at": `"@"`,
	"arrow": `"->"`, "fat_arrow": `"=>"`, "ellipsis": `"..."`,
	"plus": `"+"`, "minus": `"-"`, "star": `"*"`, "slash": `"/"`, "percent": `"%"`,
	"bang": `"!"`, "bangbang": `"!!"`, "question": `"?"`,
	"eq": `"=="`, "neq": `"!="`,
	"lt": `"<"`, "gt": `">"`, "lte": `"<="`, "gte": `">="`,
	"land": `"&&"`, "lor": `"||"`,
	"plus_assign": `"+="`, "minus_assign": `"-="`,
	"star_assign": `"*="`, "slash_assign": `"/="`, "pct_assign": `"%="`,
	"kw_import": `"import"`, "kw_struct": `"struct"`, "kw_enum": `"enum"`,
	"kw_const": `"const"`, "kw_var": `"var"`, "kw_component": `"component"`,
	"kw_if": `"if"`, "kw_for": `"for"`, "kw_else": `"else"`,
	"kw_func": `"func"`, "kw_unit": `"unit"`, "kw_return": `"return"`,
	"slashdash": `"/-"`,
}

// ruleNames maps EBNF production names to tree-sitter rule names.
// Prefix with "_" for hidden rules.
var ruleNames = map[string]string{
	"Document":      "source_file",
	"StmtBlock":     "statement_block",
	"ImportDecl":    "import_declaration",
	"StructDecl":    "struct_declaration",
	"StructField":   "struct_field",
	"EnumDecl":      "enum_declaration",
	"UnitDecl":      "unit_declaration",
	"ConstDecl":     "const_declaration",
	"ConstSpec":     "const_spec",
	"IdentList":     "identifier_list",
	"VarDecl":       "var_declaration",
	"VarSpec":       "var_spec",
	"VarHandler":    "var_handler",
	"FuncDecl":      "func_declaration",
	"FuncTail":      "_func_tail",
	"FuncBodyTail":  "_func_body_tail",
	"FuncName":      "func_name",
	"TypeParamList": "type_param_list",
	"ParamList":     "_param_list",
	"Param":         "func_param",
	"ComponentDecl": "component_declaration",
	"CompParamList": "_comp_param_list",
	"CompParam":     "component_param",
	"CompParamTail": "_comp_param_tail",
	"IfNode":        "if_node",
	"ForNode":       "for_node",
	"AssignOp":      "assignment_operator",
	"ListBody":      "_list_body",
	"ListElem":      "_list_element",
	"StructLitBody": "_struct_lit_body",
	"AnonStructLit": "anon_struct_literal",
	"AnonField":     "anon_struct_field",
	"FuncLit":       "anon_func_expression",
	"Type":          "type_identifier",
	"TypeList":      "_type_list",
	// Skipped but referenced — map to hand-crafted rules.
	"Expr":         "_expression",
	"CondExpr":     "_expression",
	"Stmt":         "_stmt",
	"VisualOrStmt": "_visual_or_stmt",
	"ArgList":      "_arg_list",
}

// skipSet lists productions that are NOT mechanically generated.
// They are either hand-crafted in the template or collapsed.
var skipSet = map[string]bool{
	// Statement dispatch — hand-crafted for named nodes.
	"Stmt": true,
	// Visual/statement — decomposed in template.
	"VisualOrStmt": true, "StatementPrimary": true,
	"StmtPostfixOp": true,
	// Arg list — simplified (no left-factoring) in template.
	"Arg": true, "ArgList": true,
	"ArgExprCont": true, "IdentArgCont": true, "NonIdentPrimary": true,
	// Expression precedence chain — hand-crafted with prec.left.
	"Expr": true, "TernaryExpr": true,
	"OrExpr": true, "AndExpr": true, "EqExpr": true,
	"CmpExpr": true, "AddExpr": true, "MulExpr": true,
	"EqOp": true, "CmpOp": true, "AddOp": true, "MulOp": true,
	"UnaryExpr": true, "PostfixExpr": true,
	"PrimaryExpr": true, "ExprPostfixOp": true,
	// CondExpr chain — LL(1) artifact, use _expression + conflicts.
	"CondExpr": true, "CondOrExpr": true, "CondAndExpr": true,
	"CondEqExpr": true, "CondCmpExpr": true, "CondAddExpr": true,
	"CondMulExpr": true, "CondUnaryExpr": true, "CondPostfixExpr": true,
	// String interpolation — external scanner.
	"InterpStr": true, "TripleInterp": true,
}

// precRightSet lists productions that need prec.right wrapping
// to resolve greedy-match ambiguities in tree-sitter.
var precRightSet = map[string]bool{
	"VarSpec":       true, // var x Type = ... — prefer consuming Type over ending early
	"ConstSpec":     true, // same pattern
	"Param":         true, // func param with optional type
	"IdentList":     true, // var (x, y = ...) — prefer consuming more idents
	"CompParamTail": true, // comp param Type [= Expr] — prefer consuming type
}

// ── Rule generation ─────────────────────────────────────────────────────────

type Rule struct {
	Name string
	Body string
}

func generateRules(prods []Production) []Rule {
	var rules []Rule
	for _, p := range prods {
		if skipSet[p.Name] {
			continue
		}
		tsName := tsRuleName(p.Name)
		body := emitExpr(p.Body, 2)
		if precRightSet[p.Name] {
			body = "prec.right(" + body + ")"
		}
		rules = append(rules, Rule{Name: tsName, Body: body})
	}
	return rules
}

func tsRuleName(ebnfName string) string {
	if name, ok := ruleNames[ebnfName]; ok {
		return name
	}
	return toSnake(ebnfName)
}

func emitExpr(e Expr, indent int) string {
	switch e := e.(type) {
	case NameExpr:
		return emitName(e.Name)
	case SeqExpr:
		if len(e.Terms) == 0 {
			return `""`
		}
		parts := make([]string, len(e.Terms))
		for i, t := range e.Terms {
			parts[i] = emitExpr(t, indent)
		}
		if len(parts) == 1 {
			return parts[0]
		}
		joined := strings.Join(parts, ", ")
		if len(joined) < 80 {
			return "seq(" + joined + ")"
		}
		return "seq(\n" + indentJoin(parts, indent+1, ",\n") + ")"
	case AltExpr:
		parts := make([]string, len(e.Alts))
		for i, a := range e.Alts {
			parts[i] = emitExpr(a, indent+1)
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "choice(\n" + indentJoin(parts, indent+1, ",\n") + ")"
	case RepeatExpr:
		return "repeat(" + emitExpr(e.Body, indent) + ")"
	case OptionalExpr:
		return "optional(" + emitExpr(e.Body, indent) + ")"
	default:
		return "/* unknown */"
	}
}

func indentJoin(parts []string, depth int, sep string) string {
	pad := strings.Repeat("  ", depth)
	closePad := strings.Repeat("  ", depth-1)
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(pad)
		b.WriteString(p)
		if i < len(parts)-1 {
			b.WriteString(sep)
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString(closePad)
	return b.String()
}

func emitName(name string) string {
	if ts, ok := termMap[name]; ok {
		return ts
	}
	if ts, ok := ruleNames[name]; ok {
		return "$." + ts
	}
	return "$." + toSnake(name)
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			if unicode.IsLower(rune(s[i-1])) || unicode.IsDigit(rune(s[i-1])) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// ── Template ────────────────────────────────────────────────────────────────

var grammarTemplate = `// AUTO-GENERATED from internal/v2/parser/sngl.ebnf — do not edit manually.
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
    [$.method_expression, $.field_expression],
    [$.anon_struct_field, $.spread_expression],
    [$.anon_struct_field, $._expression],
  ],

  supertypes: ($) => [$._stmt, $._expression],

  rules: {
    // ─── Generated from EBNF ──────────────────────────────────
{{range .}}
    {{.Name}}: ($) =>
      {{.Body}},
{{end}}
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
        $.if_node,
        $.for_node,
        $.assignment_statement,
        $.toggle_statement,
        $.emit_expression,
        $.visual_node,
        $._expression,
      ),

    return_statement: ($) =>
      prec.right(seq("return", optional($._expression))),

    _visual_or_stmt: ($) =>
      choice(
        $.assignment_statement,
        $.toggle_statement,
        $.emit_expression,
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

    event_arg: ($) =>
      seq(
        "@",
        field("name", $.identifier),
        optional(field("type", $.type_identifier)),
        optional(seq("(", optional($.identifier), ")")),
        optional($.statement_block),
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

    // Emit/event expression for statement context: @click, @click(1)
    emit_expression: ($) =>
      prec.right(PREC.POSTFIX, seq(
        $.event_method,
        optional(seq("(", optional($._arg_list), ")")),
      )),

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
          field("method", choice($.identifier, $.event_method, $.element_ref)),
          "(",
          optional($._arg_list),
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
          field("field", choice($.identifier, $.event_method, $.element_ref)),
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

    raw_string_literal: (_$) => token(seq(` + "'`', /[^`]*/, '`'" + `)),

    plain_string: (_$) => token(seq('"', repeat(choice(/[^"\\]/, /\\./)), '"')),

    element_ref: (_$) => token(seq("#", /[a-zA-Z_][a-zA-Z0-9_]*/)),

    color_literal: (_$) => /#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?/,

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
`
