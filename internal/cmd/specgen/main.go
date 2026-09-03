// Command specgen fills the machine-generated regions of
// docs/reference/specification.md from the v2 EBNF grammar.
//
// The specification is a hand-written prose document. specgen does NOT own
// the whole file; it only rewrites the spans delimited by HTML markers:
//
//	<!-- BEGIN GENERATED: <name> -->
//	...generated content...
//	<!-- END GENERATED: <name> -->
//
// Everything outside those markers is authored by hand and left untouched.
// Recognized region names:
//
//	grammar-full              every EBNF section, in order (the appendix)
//	grammar-<section-slug>    a single grammar section (inline, per chapter)
//	precedence                operator precedence table
//	keywords                  reserved keywords + predeclared identifiers
//
// specgen runs from docs/generate.go under `go generate`; its own test fails
// when the committed regions have drifted from internal/parser/sngl.ebnf.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func main() {
	root := repoRoot()
	if err := generate(root, filepath.Join(root, "docs", "reference", "specification.md")); err != nil {
		fatalf("%v", err)
	}
}

func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// outPath is a parameter so a test can run the whole pipeline, mdox included,
// over a copy and diff the result.
func generate(root, outPath string) error {
	prods := parseEBNF(filepath.Join(root, "internal", "parser", "sngl.ebnf"))
	gen := newGenerator(prods)

	src, err := os.ReadFile(outPath)
	if err != nil {
		return err
	}
	out, err := gen.fillRegions(string(src))
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, []byte(out), 0o644); err != nil {
		return err
	}

	// mdox fmt keeps `go tool verify -dry` from flip-flopping between generate
	// and check.
	cmd := exec.Command("go", "tool", "mdox", "fmt", "--soft-wraps", outPath)
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mdox fmt %s: %v", outPath, err)
	}
	return nil
}

// RE2 lacks backreferences, so both marker names are captured and compared in
// the callback. Region bodies must not themselves contain a BEGIN marker.
var regionRE = regexp.MustCompile(`(?s)(<!-- BEGIN GENERATED: (\S+) -->\n).*?(\n<!-- END GENERATED: (\S+) -->)`)

func (g *generator) fillRegions(src string) (string, error) {
	var genErr error
	out := regionRE.ReplaceAllStringFunc(src, func(match string) string {
		m := regionRE.FindStringSubmatch(match)
		begin, name, end, endName := m[1], m[2], m[3], m[4]
		if name != endName {
			genErr = fmt.Errorf("specgen: mismatched GENERATED markers: BEGIN %q vs END %q", name, endName)
			return match
		}
		body, ok := g.region(name)
		if !ok {
			genErr = fmt.Errorf("specgen: unknown GENERATED region %q in specification.md", name)
			return match
		}
		return begin + "\n" + strings.TrimRight(body, "\n") + "\n" + end
	})
	return out, genErr
}

func (g *generator) region(name string) (string, bool) {
	switch name {
	case "grammar-full":
		return g.grammarFull(), true
	case "precedence":
		return precedenceTable(), true
	case "keywords":
		return keywordsBlock(), true
	}
	if slug, ok := strings.CutPrefix(name, "grammar-"); ok {
		if sec, ok := g.sectionBySlug[slug]; ok {
			return g.grammarSection(sec), true
		}
	}
	return "", false
}

type production struct {
	name string
	body string
}

var prodRE = regexp.MustCompile(`^(\w+)\s*=\s*(.*)`)

func parseEBNF(path string) []production {
	f, err := os.Open(path)
	if err != nil {
		fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var prods []production
	var nameBuf string
	var bodyBuf []string

	flush := func() {
		if nameBuf != "" {
			prods = append(prods, production{name: nameBuf, body: strings.Join(bodyBuf, "\n")})
		}
		nameBuf = ""
		bodyBuf = nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			flush()
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if m := prodRE.FindStringSubmatch(line); m != nil {
			flush()
			nameBuf = m[1]
			bodyBuf = []string{m[2]}
			continue
		}
		if nameBuf != "" {
			bodyBuf = append(bodyBuf, line)
		}
	}
	flush()
	return prods
}

type section struct {
	slug  string
	rules []string
}

var sections = []section{
	{"document", []string{"Document", "StmtBlock"}},
	{"statements", []string{"Stmt", "VisualOrStmt", "IfNode", "ForNode", "SlotNode", "SlotArgList", "AssignOp", "IncDecOp"}},
	{"imports", []string{"ImportDecl"}},
	{"type-declarations", []string{"StructDecl", "StructField", "EnumDecl", "UnitDecl"}},
	{"constants-variables", []string{
		"ConstDecl", "ConstSpec", "IdentList",
		"VarDecl", "VarSpec", "VarHandler",
	}},
	{"functions", []string{"FuncDecl", "FuncTail", "FuncBodyTail", "FuncName", "TypeParamList", "TypeParam", "ParamList", "Param"}},
	{"components", []string{"ComponentDecl", "CompParamList", "CompParam", "CompParamBody", "SlotParam", "CompParamTail"}},
	{"expressions", []string{
		"Expr", "TernaryExpr", "OrExpr", "AndExpr",
		"EqExpr", "CmpExpr", "AddExpr", "MulExpr",
		"EqOp", "CmpOp", "AddOp", "MulOp",
		"UnaryExpr", "PostfixExpr", "PrimaryExpr",
		"ExprPostfixOp", "StmtPostfixOp",
	}},
	{"argument-lists", []string{"ArgList", "Arg", "IdentArgCont", "ArgExprCont"}},
	{"literals", []string{
		"ListBody", "ListElem", "StructLitBody", "AnonStructLit", "AnonField", "FuncLit",
	}},
	{"string-interpolation", []string{"InterpStr", "TripleInterp", "I18nInterpStr", "I18nTriple", "I18nPlaceholder"}},
	{"types", []string{"Type", "TypeList", "FuncTypeParamList", "FuncTypeParam"}},
}

type generator struct {
	prodMap       map[string]production
	sectionBySlug map[string]section
}

func newGenerator(prods []production) *generator {
	g := &generator{
		prodMap:       map[string]production{},
		sectionBySlug: map[string]section{},
	}
	for _, p := range prods {
		g.prodMap[p.name] = p
	}
	for _, s := range sections {
		g.sectionBySlug[s.slug] = s
	}
	return g
}

func (g *generator) grammarSection(sec section) string {
	var b strings.Builder
	b.WriteString("```ebnf\n")
	for _, name := range sec.rules {
		p, ok := g.prodMap[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s = %s\n\n", name, formatBody(p.body))
	}
	b.WriteString("```")
	return b.String()
}

func (g *generator) grammarFull() string {
	var b strings.Builder
	for _, sec := range sections {
		b.WriteString(g.grammarSection(sec))
		b.WriteString("\n\n")
	}
	return b.String()
}

var tokenReplacements = map[string]string{
	"ident": "IDENT", "int_lit": "INT", "float_lit": "FLOAT",
	"str_full": `STRING`, "triple_full": `TRIPLE_STRING`, "raw_str": "RAW_STRING",
	"unit_lit": "UNIT", "hash": "HASH",
	"lparen": `"("`, "rparen": `")"`, "lbrace": `"{"`, "rbrace": `"}"`,
	"lbracket": `"["`, "rbracket": `"]"`,
	"comma": `","`, "dot": `"."`, "colon": `":"`, "assign": `"="`,
	"at": `"@"`, "amp": `"&"`, "arrow": `"->"`, "fat_arrow": `"=>"`, "ellipsis": `"..."`,
	"plus": `"+"`, "minus": `"-"`, "star": `"*"`, "slash": `"/"`, "percent": `"%"`,
	"bang": `"!"`, "bangbang": `"!!"`, "question": `"?"`,
	"plus_plus": `"++"`, "minus_minus": `"--"`,
	"eq": `"=="`, "neq": `"!="`, "lt": `"<"`, "gt": `">"`,
	"lte": `"<="`, "gte": `">="`,
	"land": `"&&"`, "lor": `"||"`,
	"plus_assign": `"+="`, "minus_assign": `"-="`, "star_assign": `"*="`,
	"slash_assign": `"/="`, "pct_assign": `"%="`,
	"kw_import": `"import"`, "kw_struct": `"struct"`, "kw_enum": `"enum"`,
	"kw_const": `"const"`, "kw_var": `"var"`, "kw_component": `"component"`,
	"kw_if": `"if"`, "kw_for": `"for"`, "kw_else": `"else"`,
	"kw_func": `"func"`, "kw_unit": `"unit"`, "kw_return": `"return"`,
	"kw_break": `"break"`, "kw_continue": `"continue"`,
	"kw_slot":   `"slot"`,
	"slashdash": `"/-"`, "semi": `";"`,
	"str_start": "STR_START", "str_end": "STR_END", "str_resume": "STR_RESUME",
	"triple_start": "TRIPLE_START", "triple_end": "TRIPLE_END",
	"i18n_str_full": "I18N_STR_FULL", "i18n_triple_full": "I18N_TRIPLE_FULL",
	"i18n_str_start": "I18N_STR_START", "i18n_str_end": "I18N_STR_END",
	"i18n_str_resume":   "I18N_STR_RESUME",
	"i18n_triple_start": "I18N_TRIPLE_START", "i18n_triple_end": "I18N_TRIPLE_END",
}

var tokenRE = regexp.MustCompile(`\b[a-z][a-z0-9_]*\b`)

func formatBody(body string) string {
	body = strings.TrimSuffix(strings.TrimSpace(body), ".")
	body = strings.TrimSpace(body)

	body = tokenRE.ReplaceAllStringFunc(body, func(s string) string {
		if r, ok := tokenReplacements[s]; ok {
			return r
		}
		return s
	})

	lines := strings.Split(body, "\n")
	if len(lines) == 1 {
		return strings.TrimSpace(lines[0])
	}
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, "    "+trimmed)
	}
	return "\n" + strings.Join(out, "\n")
}

func precedenceTable() string {
	return strings.Join([]string{
		"| Precedence | Operators | Associativity | Description |",
		"| --- | --- | --- | --- |",
		"| 1 | `? :` | right | Ternary |",
		"| 2 | <code>&#124;&#124;</code> | left | Logical OR |",
		"| 3 | `&&` | left | Logical AND |",
		"| 4 | `==`, `!=` | left | Equality |",
		"| 5 | `<`, `>`, `<=`, `>=` | left | Comparison |",
		"| 6 | `+`, `-` | left | Addition |",
		"| 7 | `*`, `/`, `%` | left | Multiplication |",
		"| 8 | `!`, `-`, `&`, `*`, `const` (unary) | right | Unary |",
		"| 9 | `.`, `[]`, `()` | left | Postfix |",
	}, "\n")
}

func keywordsBlock() string {
	return strings.Join([]string{
		"`break` `component` `const` `continue` `else` `enum` `for` `func` `if` `import` `platform` `return` `struct` `unit` `var`",
		"",
		"The following names are **predeclared identifiers**, not keywords: `true`, `false`, `null`, `output`, `timer`, `window`, `style`. They have meaning in context but may be shadowed by user declarations.",
	}, "\n")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "specgen: "+format+"\n", args...)
	os.Exit(1)
}
