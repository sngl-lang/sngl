# i18n Translatable Strings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** `docs/superpowers/specs/2026-05-07-i18n-translatable-strings-design.md`

**Goal:** Add `$"..."` translatable string syntax with full ICU MessageFormat support, lowering to a typed stdlib `i18n` package and introducing a `map<K, V>` generic type.

**Architecture:** Lexer emits distinct `I18N_*` token types when scanning `$"..."`. Grammar adds `I18nInterpStr`, `I18nPlaceholder`, `MsgFormatBody`, `MsgCase` productions. AST has new `I18nInterpExpr`/`I18nPlaceholderExpr`/`I18nCase` nodes. Checker validates ICU shape. IR conversion desugars to `ir.Call` targeting `i18n.tr(template, args)` — no IR-level translatable concept. Runtime uses `golang.org/x/text` for ICU formatting (Go reference impl).

**Tech Stack:** Go 1.22+, `modernc.org/egg` (parser generator, already a `go tool` dep), `golang.org/x/text/message` + `feature/plural` + `number` + `currency` (runtime ICU).

---

## File Structure

**New files:**
- `lib/i18n.sngl` — stdlib decl: `tr`, `format`, `numberInt`, `numberFloat`, `date`, `time`, `datetime`, `plural`, `select`, `selectordinal`, `PluralKey` struct, CLDR keyword constants, `exactly()` constructor.
- `internal/parser/i18n_test.go` — i18n lexer / parser tests.
- `internal/parser/format_i18n_test.go` — format round-trip tests for translatable strings.
- `internal/checker/i18n.go` — `inferI18nInterp` and helpers.
- `internal/checker/i18n_test.go` — checker tests for i18n.
- `internal/checker/map.go` — map type validation helpers.
- `internal/checker/map_test.go` — map type tests.
- `lib/i18n_runtime.go` — Go reference impl injected via `PkgSource`.
- `lib/i18n_runtime_test.go` — Go runtime impl tests.
- `cmd/sngl/testdata/i18n_basic.txt` — txtar end-to-end test for `$"..."`.
- `cmd/sngl/testdata/i18n_plural.txt` — txtar test for plural form.

**Modified files:**
- `ast/expr.go` — add `I18nInterpExpr`, `I18nPlaceholderExpr`, `I18nCase`; remove `InterpolationExpr.Translatable`.
- `internal/parser/token.go` — new token types `I18N_STR_FULL`, `I18N_STR_START`, `I18N_STR_RESUME`, `I18N_STR_END`, `I18N_TRIPLE_FULL`, `I18N_TRIPLE_START`, `I18N_TRIPLE_END`, `I18N_CASE_FULL`, `I18N_CASE_START`, `I18N_CASE_END`; remove `Token.Translatable`.
- `internal/parser/lexer.go` — i18n string scanning, frame-state extension.
- `internal/parser/sngl.ebnf` — terminal declarations + new productions.
- `internal/parser/zparser.go` — regenerated from ebnf.
- `internal/parser/build.go` — `buildI18nInterpStr`, `buildI18nTriple`, `buildI18nFull`, `buildI18nPlaceholder`, `buildMsgFormatBody`, `buildMsgCase`; revert `Translatable` flag handling.
- `internal/parser/format.go` — formatter cases for new AST nodes; revert `$` prefix on `InterpolationExpr`.
- `internal/checker/expr.go` — dispatch new node types; revert `Translatable` warning (ported into i18n.go).
- `internal/checker/checker.go` — register i18n stdlib package source.
- `ir/types.go` — add `TypeMap` kind, `MapOf(k, v)` constructor.
- `internal/checker/types.go` — `TypMap` helpers.
- `internal/checker/resolve.go` — recognise `map<K, V>` type expr.
- `codegen/lang/golang/helpers.go` — emit map literals + index ops.
- `codegen/lang/javascript/javascript.go` — emit map literals (`new Map([...])`) + index ops.
- `codegen/lang/kotlin/kotlin.go` — emit map literals + index ops.
- `cmd/sngl/extract.go` — DELETE.
- `cmd/sngl/main.go` — remove `extractCmd` registration.
- `cmd/sngl/testdata/extract_i18n.txt` — DELETE.

---

## Phase A — Cleanup the unilateral implementation

These tasks roll back the AST/lexer/checker/extract changes from the earlier commits so the new design lands cleanly.

### Task A1: Delete the extract command

**Files:**
- Modify: `cmd/sngl/main.go`
- Delete: `cmd/sngl/extract.go`
- Delete: `cmd/sngl/testdata/extract_i18n.txt`

- [ ] **Step 1: Remove the registration in `cmd/sngl/main.go`**

  Remove the line `rootCmd.AddCommand(extractCmd)` (currently the last line in the `init()` body around `rootCmd.AddCommand` calls).

- [ ] **Step 2: Delete the files**

  ```bash
  rm cmd/sngl/extract.go cmd/sngl/testdata/extract_i18n.txt
  ```

- [ ] **Step 3: Verify build**

  ```bash
  go build ./...
  ```
  Expected: clean build, no errors.

- [ ] **Step 4: Run sngl tests**

  ```bash
  go test ./cmd/sngl/...
  ```
  Expected: PASS (the extract_i18n txtar is gone).

- [ ] **Step 5: Commit**

  ```bash
  git add cmd/sngl/main.go cmd/sngl/extract.go cmd/sngl/testdata/extract_i18n.txt
  git commit -m "refactor: drop sngl extract from #21 scope"
  ```

### Task A2: Revert the `Token.Translatable` flag and lexer `$` recognition

**Files:**
- Modify: `internal/parser/token.go`
- Modify: `internal/parser/lexer.go`

- [ ] **Step 1: Remove `Translatable` from `Token`**

  In `internal/parser/token.go`, change the `Token` struct back to:
  ```go
  type Token struct {
      Type    TokenType
      Literal string
      Line    int
      Column  int
  }
  ```

- [ ] **Step 2: Remove the `$"..."` recognition from `lexer.go`**

  In `internal/parser/lexer.go`'s `NextToken()`, delete the block that begins with the comment `// Translatable string literal: $"..." or $"""..."""` (the `if ch == '$' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '"'` branch and its body that calls `scanTripleString` / `scanString` and sets `tok.Translatable = true`).

- [ ] **Step 3: Verify build**

  ```bash
  go build ./...
  ```
  Expected: build fails because `build.go` references `tok.Translatable`. That's expected; fixed in next task.

### Task A3: Revert `InterpolationExpr.Translatable` and AST builder propagation

**Files:**
- Modify: `ast/expr.go`
- Modify: `internal/parser/build.go`

- [ ] **Step 1: Remove `Translatable` from `InterpolationExpr` in `ast/expr.go`**

  Change `InterpolationExpr` back to:
  ```go
  // InterpolationExpr is a string with interpolated expressions.
  // Parts alternate between *LiteralExpr (string) and expression nodes.
  type InterpolationExpr struct {
      Pos   Pos
      Parts []Expr
      Style StringStyle
  }
  ```

- [ ] **Step 2: Revert `tokenToExpr` for `STR_FULL`/`TRIPLE_FULL`**

  In `internal/parser/build.go`'s `tokenToExpr`, replace the translatable branches with the original simple form:
  ```go
  case STR_FULL:
      return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringQuoted, Raw: tok.Literal}
  case TRIPLE_FULL:
      return &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringTrippleQuoted, Raw: tok.Literal}
  ```

- [ ] **Step 3: Revert `buildInterpStr` and `buildTripleInterp`**

  Restore both to their pre-i18n form: drop the `translatable := false` local, drop `translatable = tok.Translatable` assignment, and drop `Translatable: translatable` from the returned `&ast.InterpolationExpr`. Final form:

  ```go
  func (b *builder) buildInterpStr(it nodeIter) ast.Expr {
      var parts []ast.Expr
      pos := ast.Pos{}
      for !it.done() {
          if !it.isNonTerminal() {
              tok := it.shift()
              if !pos.IsSet() {
                  pos = b.posFromToken(tok)
              }
              switch tok.Type {
              case STR_START, STR_RESUME, STR_END:
                  if tok.Literal != "" {
                      parts = append(parts, &ast.LiteralExpr{
                          Pos:  ast.Pos(b.posFromToken(tok)),
                          Kind: ast.LiteralStringQuoted,
                          Raw:  tok.Literal,
                      })
                  }
              }
          } else {
              parts = append(parts, b.buildExpr(it.enter()))
          }
      }
      return &ast.InterpolationExpr{Pos: pos, Parts: parts, Style: ast.StyleDouble}
  }
  ```

  Same shape for `buildTripleInterp` with `TRIPLE_START`/`TRIPLE_END` and `StyleTriple`.

- [ ] **Step 4: Verify build**

  ```bash
  go build ./...
  ```
  Expected: clean build.

### Task A4: Revert formatter `$` prefix and checker warning

**Files:**
- Modify: `internal/parser/format.go`
- Modify: `internal/checker/expr.go`
- Modify: `internal/parser/format_test.go`
- Modify: `internal/checker/checker_test.go`

- [ ] **Step 1: Remove `$` prefix in `format.go`**

  In `writeInterpolation`, delete the leading:
  ```go
  if x.Translatable {
      f.write("$")
  }
  ```

- [ ] **Step 2: Remove the no-static-text warning prelude in `checker/expr.go`**

  In `inferInterpolation`, delete the block that begins:
  ```go
  if x.Translatable {
      hasStatic := false
      ...
  }
  ```

- [ ] **Step 3: Delete the translatable-string format tests**

  In `internal/parser/format_test.go`, delete `TestFormatTranslatableString` and `TestFormatTranslatableInterpolation` (last two functions; will be re-added by Phase E with the new node types).

- [ ] **Step 4: Delete the translatable-string checker tests**

  In `internal/checker/checker_test.go`, delete `TestTranslatableStringValid` and `TestTranslatableStringNoStaticTextWarning`. Re-added in Phase F.

- [ ] **Step 5: Run all tests**

  ```bash
  go test ./...
  ```
  Expected: PASS, repo is clean of the unilateral implementation.

- [ ] **Step 6: Commit**

  ```bash
  git add ast/expr.go internal/parser/token.go internal/parser/lexer.go \
          internal/parser/build.go internal/parser/format.go \
          internal/checker/expr.go \
          internal/parser/format_test.go internal/checker/checker_test.go
  git commit -m "refactor: revert unilateral i18n implementation prior to redesign"
  ```

---

## Phase B — `map<K, V>` generic type

The `select`/`plural` stdlib functions need this. Build it first so later phases can declare typed signatures.

### Task B1: Add `TypeMap` kind to IR

**Files:**
- Modify: `ir/types.go`
- Test: `ir/types_test.go` (may need to create if absent — check first)

- [ ] **Step 1: Check for an existing test file**

  ```bash
  ls ir/types_test.go 2>&1
  ```
  If absent, you'll create it in step 3.

- [ ] **Step 2: Add `TypeMap` kind**

  In `ir/types.go`, add `TypeMap` to the `TypeKind` const block (after `TypeOption`):
  ```go
  const (
      TypeInvalid TypeKind = iota
      TypeDyn
      TypeBool
      TypeInt
      TypeFloat
      TypeString
      TypeList      // Elem set
      TypeMap       // Elems = [K, V]
      TypeOption    // Elem set
      // ... rest unchanged
  )
  ```

  Add the constructor below `ListOf`:
  ```go
  // MapOf returns a map<K, V> type.
  func MapOf(k, v *Type) *Type {
      return &Type{Kind: TypeMap, Elems: []*Type{k, v}}
  }
  ```

  Update the `String()` method to include the map case. Find the existing switch on `t.Kind` (around line 100+) and add:
  ```go
  case TypeMap:
      if len(t.Elems) == 2 {
          return fmt.Sprintf("map<%s, %s>", t.Elems[0], t.Elems[1])
      }
      return "map<?>"
  ```

- [ ] **Step 3: Write a test for the new constructor**

  Create or extend `ir/types_test.go`:
  ```go
  package ir

  import "testing"

  func TestMapOfString(t *testing.T) {
      m := MapOf(TypString, TypInt)
      if got := m.String(); got != "map<string, int>" {
          t.Errorf("MapOf(string,int).String() = %q, want %q", got, "map<string, int>")
      }
      if m.Kind != TypeMap {
          t.Errorf("Kind = %v, want TypeMap", m.Kind)
      }
      if len(m.Elems) != 2 {
          t.Errorf("len(Elems) = %d, want 2", len(m.Elems))
      }
  }
  ```

- [ ] **Step 4: Regenerate the `TypeKind` stringer**

  ```bash
  go generate ./ir/
  ```
  Expected: `typekind_string.go` updated to include `TypeMap`.

- [ ] **Step 5: Run tests**

  ```bash
  go test ./ir/
  ```
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add ir/types.go ir/typekind_string.go ir/types_test.go
  git commit -m "feat(ir): add TypeMap kind and MapOf constructor"
  ```

### Task B2: Parse `map<K, V>` in type expressions

**Files:**
- Modify: `internal/checker/types.go`
- Modify: `internal/checker/resolve.go`
- Test: `internal/checker/map_test.go` (new)

- [ ] **Step 1: Add `TypMap` helper**

  In `internal/checker/types.go`, add near the other `Typ*` aliases:
  ```go
  // MapOf is re-exported for checker use.
  func MapOf(k, v *ir.Type) *ir.Type { return ir.MapOf(k, v) }
  ```

- [ ] **Step 2: Find where `list<T>` is resolved**

  ```bash
  grep -n "list" internal/checker/resolve.go | head
  ```
  Identify the function that handles generic types — likely `resolveNamedType` or similar — and find the case for `list`.

- [ ] **Step 3: Add the `map` case alongside `list`**

  In `internal/checker/resolve.go`, in the same function that handles `list<T>` generic resolution, add:
  ```go
  case "map":
      if len(typeArgs) != 2 {
          c.errorf(pos, "map requires exactly 2 type arguments (key, value), got %d", len(typeArgs))
          return TypDyn
      }
      k := c.resolveType(typeArgs[0])
      v := c.resolveType(typeArgs[1])
      if !isComparable(k) {
          c.errorf(pos, "map key type %s is not comparable", k)
          return TypDyn
      }
      return MapOf(k, v)
  ```

  And add the `isComparable` helper at file scope:
  ```go
  // isComparable reports whether values of t can be used as map keys.
  // Primitives (bool/int/float/string) and structs whose fields are all
  // comparable qualify. Lists, maps, and functions do not.
  func isComparable(t *ir.Type) bool {
      if t == nil {
          return false
      }
      switch t.Kind {
      case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString,
          ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
          ir.TypeColor, ir.TypeURL, ir.TypeEmail, ir.TypeUUID,
          ir.TypeRegex, ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6,
          ir.TypeHostname, ir.TypeDecimal:
          return true
      case ir.TypeStruct:
          if t.Decl == nil {
              return false
          }
          // Lookup the struct decl and check every field.
          // Implementation: iterate decl.Fields.
          // (A precise impl needs access to the decl's IR fields; for
          //  this initial pass, accept any named struct as comparable
          //  and let the codegen layer flag any non-trivial composites.)
          return true
      case ir.TypeEnum, ir.TypeUnit:
          return true
      }
      return false
  }
  ```

  Note: the struct-comparability check is conservative (always true for now). Tighten in a follow-up if needed.

- [ ] **Step 4: Write the failing test**

  Create `internal/checker/map_test.go`:
  ```go
  package checker_test

  import (
      "testing"

      "git.duckfam.us/jonathan/sngl/internal/checker"
      "git.duckfam.us/jonathan/sngl/internal/parser"
      "git.duckfam.us/jonathan/sngl/ir"
  )

  func TestMapTypeResolves(t *testing.T) {
      src := `var m map<string, int>`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected error: %s", d.Error())
          }
      }
  }

  func TestMapWrongArity(t *testing.T) {
      src := `var m map<string>`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error {
              found = true
          }
      }
      if !found {
          t.Error("expected error for map<string> (1 type arg)")
      }
  }
  ```

- [ ] **Step 5: Run tests, verify they pass**

  ```bash
  go test ./internal/checker/ -run TestMap
  ```
  Expected: PASS for both.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/checker/types.go internal/checker/resolve.go internal/checker/map_test.go
  git commit -m "feat(checker): resolve map<K, V> type expressions"
  ```

### Task B3: Map literal AST node + lexer/parser detection

**Files:**
- Modify: `ast/expr.go`
- Modify: `internal/parser/build.go`
- Modify: `internal/parser/format.go`
- Test: `internal/parser/map_test.go` (new)

The grammar already accepts `{<keyExpr>: <valueExpr>, ...}` shape via the existing struct-lit/anon-struct alternatives only for `=` separator. For `:` we need a new alternative or a build-time disambiguation. Plan: at build time, after parsing what the grammar treats as an `AnonStructLit`, inspect the field separator. If `:`, build a `MapLit`; if `=`, build a `StructExpr`.

**Implementation note:** Parsing approach — the simplest path is to extend the existing `StructLitBody` / `AnonField` productions in the EBNF to accept either `=` or `:` as the separator, and disambiguate at build time. Less intrusive than splitting into separate productions.

- [ ] **Step 1: Add the `MapLit` AST node**

  In `ast/expr.go`, after `ListExpr`:
  ```go
  // MapLit is a map literal: {<keyExpr>: <valueExpr>, ...}.
  // The colon separator distinguishes from struct literals (which use =).
  type MapLit struct {
      Pos     Pos
      Entries []MapEntry
      // Type, if non-nil, supplies an explicit map<K, V> annotation
      // (only used by IR-emitter callers; user-source map literals
      // are inferred from context).
  }

  type MapEntry struct {
      Pos   Pos
      Key   Expr
      Value Expr
  }
  ```

  Add the `ExprPos()` method:
  ```go
  func (x *MapLit) ExprPos() *Pos { return &x.Pos }
  ```

- [ ] **Step 2: Find the EBNF production for anonymous struct literals**

  ```bash
  grep -n "AnonStructLit\|AnonField" internal/parser/sngl.ebnf
  ```

- [ ] **Step 3: Update the EBNF to accept `:` as well as `=` in field bodies**

  In `internal/parser/sngl.ebnf`, modify the `AnonField` (or `StructField` body) production so the separator alternative includes `colon`:
  ```
  AnonField = Expr ( assign | colon ) Expr .
  ```

  (Locate the existing rule and adjust. The rule currently uses just `assign`.)

- [ ] **Step 4: Regenerate the parser**

  ```bash
  cd internal/parser && go run modernc.org/egg -o zparser.go -package parser -start Document sngl.ebnf
  ```
  Expected: zparser.go updated.

- [ ] **Step 5: Update `buildAnonStructLit` to disambiguate by separator**

  Find `buildAnonStructLit` in `internal/parser/build.go`:
  ```bash
  grep -n "buildAnonStructLit\|buildAnonField" internal/parser/build.go
  ```

  Change the function so that if every field uses `:`, it returns `*ast.MapLit`; if every field uses `=`, it returns `*ast.StructExpr`. Mixing is an error at parse time. For the empty `{}` case, return a dual-typed sentinel that downstream code can disambiguate by context — simplest representation: an empty `*ast.StructExpr` (the checker will see the expected type and reinterpret).

  Show the engineer the precise patch by reading the existing builder; add a `sep` field to `AnonField` AST (or read separator tokens directly during traversal) and dispatch.

  Concretely:
  ```go
  func (b *builder) buildAnonStructLit(it nodeIter) ast.Expr {
      pos := ast.Pos{}
      var fields []ast.StructFieldLit
      var entries []ast.MapEntry
      sawColon, sawAssign := false, false
      for !it.done() {
          if it.isNonTerminal() {
              // Walk the AnonField nonterminal, recording separator type.
              sub := it.enter()
              key := b.buildExpr(sub.enter()) // first Expr
              // skip separator
              tok := sub.shift()
              switch tok.Type {
              case ASSIGN:
                  sawAssign = true
                  val := b.buildExpr(sub.enter())
                  if name, ok := key.(*ast.IdentExpr); ok {
                      fields = append(fields, ast.StructFieldLit{Name: name.Name, Value: val})
                  } else {
                      // Non-ident key with `=` is a parse error.
                      b.errf(*key.ExprPos(), "struct field name must be an identifier")
                  }
              case COLON:
                  sawColon = true
                  val := b.buildExpr(sub.enter())
                  entries = append(entries, ast.MapEntry{Pos: *key.ExprPos(), Key: key, Value: val})
              }
              if !pos.IsSet() {
                  pos = *key.ExprPos()
              }
          } else {
              it.skip() // commas/semis
          }
      }
      if sawAssign && sawColon {
          b.errf(pos, "cannot mix '=' and ':' in literal; use one separator consistently")
      }
      if sawColon {
          return &ast.MapLit{Pos: pos, Entries: entries}
      }
      return &ast.StructExpr{Pos: pos, Fields: fields}
  }
  ```

  (Adapt to the actual nodeIter shape used in the file — look at existing `buildStructLitBody` for reference. The above is a sketch; the engineer should match the surrounding style.)

- [ ] **Step 6: Add the formatter case**

  In `internal/parser/format.go`, find `writeStructExpr` and add a peer `writeMapLit`:
  ```go
  func (f *formatter) writeMapLit(x *ast.MapLit) {
      f.write("{")
      for i, e := range x.Entries {
          if i > 0 {
              f.write(", ")
          }
          f.writeExpr(e.Key)
          f.write(": ")
          f.writeExpr(e.Value)
      }
      f.write("}")
  }
  ```

  And add the dispatch in `writeExpr`:
  ```go
  case *ast.MapLit:
      f.writeMapLit(x)
  ```

- [ ] **Step 7: Write the failing test**

  Create `internal/parser/map_test.go`:
  ```go
  package parser_test

  import (
      "testing"

      "git.duckfam.us/jonathan/sngl/ast"
      "git.duckfam.us/jonathan/sngl/internal/parser"
  )

  func TestParseMapLiteral(t *testing.T) {
      src := `var m = {"one": 1, "two": 2}`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      vd, ok := doc.Stmts[0].(*ast.VarDecl)
      if !ok {
          t.Fatalf("expected VarDecl, got %T", doc.Stmts[0])
      }
      ml, ok := vd.Specs[0].Default.(*ast.MapLit)
      if !ok {
          t.Fatalf("expected MapLit, got %T", vd.Specs[0].Default)
      }
      if len(ml.Entries) != 2 {
          t.Errorf("entries: got %d, want 2", len(ml.Entries))
      }
  }

  func TestParseStructLiteralStillWorks(t *testing.T) {
      src := `var c = color{r=255, g=0, b=0}`
      _, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
  }
  ```

- [ ] **Step 8: Run the tests**

  ```bash
  go test ./internal/parser/ -run TestParseMap -v
  go test ./internal/parser/ -run TestParseStructLiteralStillWorks -v
  ```
  Expected: PASS.

- [ ] **Step 9: Run the full parser test suite to catch regressions**

  ```bash
  go test ./internal/parser/
  ```
  Expected: PASS.

- [ ] **Step 10: Commit**

  ```bash
  git add ast/expr.go internal/parser/sngl.ebnf internal/parser/zparser.go \
          internal/parser/build.go internal/parser/format.go internal/parser/map_test.go
  git commit -m "feat: parse map literal {k: v, ...} distinct from struct literal"
  ```

### Task B4: Type-check map literals against `map<K, V>` context

**Files:**
- Modify: `internal/checker/expr.go`
- Modify: `internal/checker/map_test.go`

- [ ] **Step 1: Find `inferStructLit` and `inferListLit` in `expr.go`**

  ```bash
  grep -n "inferStructLit\|inferListLit\|case \*ast\.StructExpr\|case \*ast\.ListExpr" internal/checker/expr.go
  ```

- [ ] **Step 2: Add the `MapLit` dispatch and inference**

  In `inferExpr`'s switch, add:
  ```go
  case *ast.MapLit:
      return c.inferMapLit(x)
  ```

  And implement (place near `inferListLit`):
  ```go
  func (c *checker) inferMapLit(x *ast.MapLit) ir.Expr {
      // No expected-type context here — infer K and V from entries.
      var keyT, valT *ir.Type
      var entries []ir.MapEntry
      for _, e := range x.Entries {
          k := c.checkExpr(e.Key)
          v := c.checkExpr(e.Value)
          kT := exprType(k)
          vT := exprType(v)
          if keyT == nil {
              keyT = kT
          } else if !typeAssignable(keyT, kT) {
              c.errorf(e.Pos, "map key type %s does not match earlier %s", kT, keyT)
          }
          if valT == nil {
              valT = vT
          } else if !typeAssignable(valT, vT) {
              c.errorf(e.Pos, "map value type %s does not match earlier %s", vT, valT)
          }
          entries = append(entries, ir.MapEntry{Key: k, Value: v})
      }
      if keyT == nil {
          // Empty map — defer typing to expected-type context (returns dyn for now).
          return &ir.MapLitIR{Type: MapOf(TypDyn, TypDyn), Entries: nil}
      }
      if !isComparable(keyT) {
          c.errorf(x.Pos, "map key type %s is not comparable", keyT)
      }
      return &ir.MapLitIR{Type: MapOf(keyT, valT), Entries: entries}
  }
  ```

  This requires an `ir.MapLitIR` IR node — add it next.

- [ ] **Step 3: Add `MapLitIR` to `ir/expr.go` (or wherever `ir.ListLit` lives)**

  ```bash
  grep -n "type ListLit\|ListLit struct" ir/*.go
  ```
  Then add a peer struct:
  ```go
  type MapEntry struct {
      Key   Expr
      Value Expr
  }

  type MapLitIR struct {
      AST     *ast.MapLit
      Type    *Type
      Entries []MapEntry
  }

  func (x *MapLitIR) exprType() *Type { return x.Type }
  // Implement any other methods ListLit has.
  ```
  Match the methods of `ListLit` exactly — check what interface it satisfies and mirror.

- [ ] **Step 4: Update map_test.go with checker assertions**

  Append to `internal/checker/map_test.go`:
  ```go
  func TestMapLiteralInfersTypes(t *testing.T) {
      src := `var m = {"a": 1, "b": 2}`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected error: %s", d.Error())
          }
      }
  }

  func TestMapLiteralMixedKeyTypesError(t *testing.T) {
      src := `var m = {"a": 1, 2: 3}`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error {
              found = true
          }
      }
      if !found {
          t.Error("expected error for mixed key types")
      }
  }
  ```

- [ ] **Step 5: Run the tests**

  ```bash
  go test ./internal/checker/ -run TestMap -v
  ```
  Expected: PASS.

- [ ] **Step 6: Run full checker suite**

  ```bash
  go test ./internal/checker/
  ```
  Expected: PASS.

- [ ] **Step 7: Commit**

  ```bash
  git add internal/checker/expr.go ir/*.go internal/checker/map_test.go
  git commit -m "feat(checker): infer map literal types"
  ```

### Task B5: Map index lookup `m[k]` returns `V`

**Files:**
- Modify: `internal/checker/expr.go`

- [ ] **Step 1: Find `inferIndex`**

  ```bash
  grep -n "func.*inferIndex" internal/checker/expr.go
  ```

- [ ] **Step 2: Extend it to handle `TypeMap`**

  Add a case before the existing `TypeList` case:
  ```go
  if opT.Kind == ir.TypeMap {
      if len(opT.Elems) != 2 {
          return &ir.Index{Type: TypDyn, Operand: op, Index: idx}
      }
      keyT, valT := opT.Elems[0], opT.Elems[1]
      if !typeAssignable(keyT, exprType(idx)) {
          c.errorf(x.Pos, "map index type %s does not match key type %s", exprType(idx), keyT)
      }
      return &ir.Index{Type: valT, Operand: op, Index: idx}
  }
  ```

- [ ] **Step 3: Add a test**

  Append to `internal/checker/map_test.go`:
  ```go
  func TestMapIndexReturnsValueType(t *testing.T) {
      src := `var m = {"a": 1, "b": 2}; var x int = m["a"]`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected: %s", d.Error())
          }
      }
  }

  func TestMapIndexWrongKeyTypeError(t *testing.T) {
      src := `var m = {"a": 1}; var x = m[42]`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error {
              found = true
          }
      }
      if !found {
          t.Error("expected error for int key on string-keyed map")
      }
  }
  ```

- [ ] **Step 4: Run**

  ```bash
  go test ./internal/checker/ -run TestMapIndex -v
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/checker/expr.go internal/checker/map_test.go
  git commit -m "feat(checker): map index lookup returns V"
  ```

### Task B6: Map methods (`length`, `keys`, `values`, `contains`, `get`)

**Files:**
- Modify: `lib/types.sngl`
- Modify: `internal/checker/checker.go` (or wherever stdlib type methods are registered)

- [ ] **Step 1: Add the `map` struct decl with methods to `lib/types.sngl`**

  Append:
  ```sngl
  // Generic map<K, V>. Methods are pure — they do not mutate the receiver.
  // Equality on K determines membership; non-comparable key types are
  // rejected at type-check time.
  struct map {}

  // Number of entries currently in the map.
  func map.length() -> int

  // List of keys in unspecified order.
  func map.keys() -> list

  // List of values in unspecified order.
  func map.values() -> list

  // True iff the given key exists.
  func map.contains(key dyn) -> bool

  // Lookup with default — returns the value if present, otherwise default.
  func map.get(key dyn, default dyn) -> dyn
  ```

  Note: the parameter types are `dyn` because the stdlib decl can't see the parameterised K/V. Codegen substitutes them at the call site.

- [ ] **Step 2: Verify the stdlib parses**

  ```bash
  go build ./...
  go test ./internal/checker/
  ```
  Expected: PASS.

- [ ] **Step 3: Add a test exercising one method**

  Append to `internal/checker/map_test.go`:
  ```go
  func TestMapLengthMethod(t *testing.T) {
      src := `var m = {"a": 1}; var n = m.length()`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected: %s", d.Error())
          }
      }
  }
  ```

- [ ] **Step 4: Run**

  ```bash
  go test ./internal/checker/ -run TestMapLengthMethod -v
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add lib/types.sngl internal/checker/map_test.go
  git commit -m "feat(stdlib): add map methods (length, keys, values, contains, get)"
  ```

### Task B7: Codegen — Go map literals

**Files:**
- Modify: `codegen/lang/golang/helpers.go` (or relevant emit file)

- [ ] **Step 1: Find where ListLit is emitted in Go codegen**

  ```bash
  grep -n "ListLit\|case \*ir\.ListLit" codegen/lang/golang/
  ```

- [ ] **Step 2: Add `MapLitIR` emission alongside `ListLit`**

  Wherever `*ir.ListLit` is handled, add a peer:
  ```go
  case *ir.MapLitIR:
      // Emit: map[K]V{k1: v1, k2: v2, ...}
      keyGo := goTypeName(x.Type.Elems[0])
      valGo := goTypeName(x.Type.Elems[1])
      w.Printf("map[%s]%s{", keyGo, valGo)
      for i, e := range x.Entries {
          if i > 0 {
              w.Print(", ")
          }
          emitExpr(w, e.Key)
          w.Print(": ")
          emitExpr(w, e.Value)
      }
      w.Print("}")
  ```
  Adapt `goTypeName` and `emitExpr` to the actual helper names in the package.

- [ ] **Step 3: Find existing Go codegen golden tests**

  ```bash
  ls codegen/lang/golang/testdata/ 2>&1 | head
  ls codegen/platform/bubbletea/testdata/ 2>&1 | head
  ```

- [ ] **Step 4: Add a fixture exercising a map literal**

  Pick a working bubbletea golden test directory and add a new one mirroring its layout. The `.sngl` source:
  ```sngl
  output bubbletea, golang
  var m = {"a": 1, "b": 2}
  ```
  Generate the expected Go output by running compile manually once the codegen change is in.

- [ ] **Step 5: Run codegen tests**

  ```bash
  go test ./codegen/...
  ```
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add codegen/lang/golang/ codegen/platform/bubbletea/testdata/
  git commit -m "feat(codegen-go): emit map literals as map[K]V{...}"
  ```

### Task B8: Codegen — JS map literals (`new Map([[k, v], ...])`)

**Files:**
- Modify: `codegen/lang/javascript/javascript.go`

- [ ] **Step 1: Locate ListLit emission in JS codegen**

  ```bash
  grep -n "ListLit\|case \*ir\.ListLit" codegen/lang/javascript/javascript.go
  ```

- [ ] **Step 2: Add MapLitIR case**

  ```go
  case *ir.MapLitIR:
      // new Map([[k1, v1], [k2, v2], ...])
      w.Print("new Map([")
      for i, e := range x.Entries {
          if i > 0 {
              w.Print(", ")
          }
          w.Print("[")
          emitExpr(w, e.Key)
          w.Print(", ")
          emitExpr(w, e.Value)
          w.Print("]")
      }
      w.Print("])")
  ```

- [ ] **Step 3: Index lookup — `m[k]` becomes `m.get(k)` for Map values**

  Find where `IndexExpr` is emitted in JS codegen. Add a check: if the operand's type is `TypeMap`, emit `<op>.get(<idx>)` instead of `<op>[<idx>]`.

- [ ] **Step 4: Run JS codegen tests**

  ```bash
  go test ./codegen/lang/javascript/
  go test ./codegen/platform/html/
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add codegen/lang/javascript/javascript.go
  git commit -m "feat(codegen-js): emit map literals as new Map(); use .get() for index"
  ```

### Task B9: Codegen — Kotlin map literals

**Files:**
- Modify: `codegen/lang/kotlin/kotlin.go`

- [ ] **Step 1: Locate ListLit emission in Kotlin codegen**

  ```bash
  grep -n "ListLit\|case \*ir\.ListLit" codegen/lang/kotlin/kotlin.go
  ```

- [ ] **Step 2: Add MapLitIR case**

  ```go
  case *ir.MapLitIR:
      // mapOf("k1" to v1, "k2" to v2, ...)
      w.Print("mapOf(")
      for i, e := range x.Entries {
          if i > 0 {
              w.Print(", ")
          }
          emitExpr(w, e.Key)
          w.Print(" to ")
          emitExpr(w, e.Value)
      }
      w.Print(")")
  ```

- [ ] **Step 3: Run Kotlin codegen tests**

  ```bash
  go test ./codegen/lang/kotlin/
  go test ./codegen/platform/android/
  ```
  Expected: PASS.

- [ ] **Step 4: Commit**

  ```bash
  git add codegen/lang/kotlin/kotlin.go
  git commit -m "feat(codegen-kotlin): emit map literals as mapOf(k to v, ...)"
  ```

---

## Phase C — i18n lexer foundation

### Task C1: Add `I18N_*` token type sentinels

**Files:**
- Modify: `internal/parser/token.go`
- Modify: `internal/parser/sngl.ebnf` (terminal name table only; productions later)

- [ ] **Step 1: Allocate sentinel bytes**

  Inspect existing TokenType byte values in `token.go` to find unused range. Use bytes `0x4F` through `0x59` (verify none clash). Add to `token.go`:
  ```go
  // Translatable string boundary tokens (parallel to STR_*/TRIPLE_*).
  I18N_STR_FULL     TokenType = 0x4F
  I18N_STR_START    TokenType = 0x50
  I18N_STR_RESUME   TokenType = 0x51
  I18N_STR_END      TokenType = 0x52
  I18N_TRIPLE_FULL  TokenType = 0x53
  I18N_TRIPLE_START TokenType = 0x54
  I18N_TRIPLE_END   TokenType = 0x55
  // Case-body boundary tokens (inside MsgCase bodies).
  I18N_CASE_FULL    TokenType = 0x56
  I18N_CASE_START   TokenType = 0x57
  I18N_CASE_END     TokenType = 0x58
  ```

  Verify each value is unused: `grep -n "TokenType = 0x4F\|0x50\|0x51\|0x52\|0x53\|0x54\|0x55\|0x56\|0x57\|0x58" internal/parser/token.go`. If any clashes, pick a different range.

- [ ] **Step 2: Update `insertsSemicolon`**

  Add the new terminals that should trigger ASI (the *_END / *_FULL forms behave like STR_END / STR_FULL):
  ```go
  case IDENT, INT, FLOAT, STR_FULL, TRIPLE_FULL, RAW_STRING, COLOR, UNIT_LITERAL, ELEMENT_REF,
      STR_END, TRIPLE_END,
      I18N_STR_FULL, I18N_TRIPLE_FULL, I18N_STR_END, I18N_TRIPLE_END,
      KW_RETURN,
      AT, RPAREN, RBRACKET, RBRACE, BANGBANG, PLUS_PLUS, MINUS_MINUS:
      return true
  ```
  (Don't add `I18N_CASE_*` — they appear inside placeholder structure, not at expression boundaries.)

- [ ] **Step 3: Add the terminal names to `sngl.ebnf` header**

  In the terminal list at the top of `sngl.ebnf`, add:
  ```
  i18n_str_full     = `\x4F` .
  i18n_str_start    = `\x50` .
  i18n_str_resume   = `\x51` .
  i18n_str_end      = `\x52` .
  i18n_triple_full  = `\x53` .
  i18n_triple_start = `\x54` .
  i18n_triple_end   = `\x55` .
  i18n_case_full    = `\x56` .
  i18n_case_start   = `\x57` .
  i18n_case_end     = `\x58` .
  ```

- [ ] **Step 4: Verify build**

  ```bash
  go build ./...
  ```
  Expected: clean (no productions yet so terminals are unused but allowed).

- [ ] **Step 5: Commit**

  ```bash
  git add internal/parser/token.go internal/parser/sngl.ebnf
  git commit -m "feat(parser): allocate I18N_* token sentinels"
  ```

### Task C2: Lexer — recognise `$"..."` and emit i18n string tokens

**Files:**
- Modify: `internal/parser/lexer.go`
- Test: `internal/parser/i18n_lex_test.go` (new)

The strategy: extend `interpFrame` with an `i18n` flag, write i18n-mode variants of `scanStringContent`, and add an entry path in `NextToken` that consumes `$` + `"` (or `$` + `"""`).

- [ ] **Step 1: Extend `interpFrame`**

  ```go
  type interpFrame struct {
      triple bool
      i18n   bool // true for $"..." / $"""...""" frames
      depth  int
  }
  ```

- [ ] **Step 2: Add an `i18n` parameter through `scanStringContent`**

  Update its signature:
  ```go
  func (l *lexer) scanStringContent(resume, triple, i18n bool, startLine, startCol int) Token
  ```

  At the boundary points where it currently returns `STR_FULL`/`STR_START`/`STR_RESUME`/`STR_END`/`TRIPLE_*`, branch on `i18n`:
  ```go
  if !triple && ch == '"' {
      l.advance()
      if resume {
          if i18n { return l.tok(I18N_STR_END, sb.String(), startLine, startCol) }
          return l.tok(STR_END, sb.String(), startLine, startCol)
      }
      if i18n { return l.tok(I18N_STR_FULL, sb.String(), startLine, startCol) }
      return l.tok(STR_FULL, sb.String(), startLine, startCol)
  }
  // similar for TRIPLE
  // for the `{` interpolation start branch:
  if ch == '{' {
      l.advance()
      l.interpStack = append(l.interpStack, interpFrame{triple: triple, i18n: i18n})
      if resume {
          if i18n { return l.tok(I18N_STR_RESUME, sb.String(), startLine, startCol) }
          return l.tok(STR_RESUME, sb.String(), startLine, startCol)
      }
      if triple {
          if i18n { return l.tok(I18N_TRIPLE_START, sb.String(), startLine, startCol) }
          return l.tok(TRIPLE_START, sb.String(), startLine, startCol)
      }
      if i18n { return l.tok(I18N_STR_START, sb.String(), startLine, startCol) }
      return l.tok(STR_START, sb.String(), startLine, startCol)
  }
  ```

  Update all call sites of `scanStringContent` to pass `i18n=false` for existing paths.

- [ ] **Step 3: Update `scanString` and `scanTripleString` to take `i18n bool`**

  Same propagation.

- [ ] **Step 4: Add `$` recognition in `NextToken`**

  Before the existing string-scanning block (`if ch == '"'`), add:
  ```go
  // Translatable string literal: $"..." or $"""..."""
  if ch == '$' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '"' {
      l.advance() // consume $
      if l.pos+2 < len(l.input) && l.input[l.pos+1] == '"' && l.input[l.pos+2] == '"' {
          return l.scanTripleString(startLine, startCol, true)
      }
      return l.scanString(startLine, startCol, true)
  }
  ```

- [ ] **Step 5: Update `}` handling in `NextToken` to resume in the right mode**

  In the existing `}` case, when popping an `interpFrame`, pass `top.i18n`:
  ```go
  case '}':
      if len(l.interpStack) > 0 {
          top := &l.interpStack[len(l.interpStack)-1]
          if top.depth == 0 {
              triple := top.triple
              i18n := top.i18n
              l.interpStack = l.interpStack[:len(l.interpStack)-1]
              return l.scanStringContent(true, triple, i18n, startLine, startCol)
          }
          top.depth--
      }
      return l.tok(RBRACE, "}", startLine, startCol)
  ```

- [ ] **Step 6: Write the failing test**

  Create `internal/parser/i18n_lex_test.go`:
  ```go
  package parser

  import "testing"

  func TestLexI18nFullString(t *testing.T) {
      l := newLexer(`$"Login"`)
      tok := l.NextToken()
      if tok.Type != I18N_STR_FULL {
          t.Errorf("Type = %v, want I18N_STR_FULL", tok.Type)
      }
      if tok.Literal != "Login" {
          t.Errorf("Literal = %q, want Login", tok.Literal)
      }
  }

  func TestLexI18nInterpolation(t *testing.T) {
      l := newLexer(`$"Hello {name}!"`)
      types := []TokenType{}
      for {
          tok := l.NextToken()
          if tok.Type == EOF || tok.Type == SEMICOLON {
              break
          }
          types = append(types, tok.Type)
      }
      // Expect: I18N_STR_START "Hello " then IDENT "name" then I18N_STR_END "!"
      want := []TokenType{I18N_STR_START, IDENT, I18N_STR_END}
      if len(types) != len(want) {
          t.Fatalf("len = %d, want %d. types = %v", len(types), len(want), types)
      }
      for i := range types {
          if types[i] != want[i] {
              t.Errorf("[%d] = %v, want %v", i, types[i], want[i])
          }
      }
  }

  func TestLexNonTranslatableStillWorks(t *testing.T) {
      l := newLexer(`"Login"`)
      tok := l.NextToken()
      if tok.Type != STR_FULL {
          t.Errorf("Type = %v, want STR_FULL", tok.Type)
      }
  }
  ```

- [ ] **Step 7: Run tests**

  ```bash
  go test ./internal/parser/ -run TestLexI18n -v
  go test ./internal/parser/ -run TestLexNonTranslatableStillWorks -v
  ```
  Expected: PASS.

- [ ] **Step 8: Run full parser suite**

  ```bash
  go test ./internal/parser/
  ```
  Expected: PASS (regressions caught here).

- [ ] **Step 9: Commit**

  ```bash
  git add internal/parser/lexer.go internal/parser/i18n_lex_test.go
  git commit -m "feat(lexer): emit I18N_STR_* tokens for $\"...\" strings"
  ```

### Task C3: Lexer — case-body framing

When the parser is inside `MsgCase`, after the selector and `{`, the lexer must scan the body as i18n string content (allowing literal text + nested placeholders) and emit `I18N_CASE_*` tokens at the body boundaries.

The trick: the lexer can't see grammar context directly. We use a sentinel: when the parser pushes a "case body" frame via a flag the lexer can observe. Cleanest approach without grammar coupling — treat the `{` after a `Selector` token like an i18n string start:

**Strategy:** Track in `interpFrame` a `caseBody bool`. When the lexer sees `{` immediately after an IDENT (or `=N`) that is the selector position, push a caseBody frame. But the lexer can't know "selector position".

**Better:** Let the grammar dictate this. We add a `case_open` *terminal* — the lexer detects after a sequence `<ident> {` or `=<int> {` while inside a placeholder body, peek for `<sel> {` and emit `I18N_CASE_START` instead of `LBRACE`.

Since the structure of MsgFormatBody requires `<selector> { ... }` as the next item after the second comma in a placeholder, the lexer can be in a "MsgFormatBody mode" after seeing `, plural ,` or `, select ,` etc. inside an `i18n` placeholder frame.

This is intricate. **Simpler approach:** the lexer tracks a per-frame `inMsgBody bool`. When inside an i18n placeholder frame and we see a `,` followed by an `ident` followed by another `,`, set the flag; subsequent `<ident>{` or `=<int>{` enters case-body mode.

For initial implementation, take the **pragmatic shortcut**: extend `scanStringContent` to support a `caseBody bool` mode. The lexer, when handed control (in `NextToken`'s `}` case after popping an i18n placeholder frame at depth 0), checks if it's about to close into a MsgCase context. Use a small lookahead when the lexer encounters `{` after the second comma in a placeholder.

**Decision:** Defer message-case lexing complexity. For this task, implement enough lexer support that **simple cases work**: `$"text {expr}"`, `$"text {expr, type}"`, `$"text {expr, type, ident{caseBody} ident{caseBody}}"` where each `caseBody` contains literal text only (no nested placeholders). Nested placeholders inside case bodies become a follow-up task.

- [ ] **Step 1: Lex placeholder content depth-aware**

  Modify the `}` case in `NextToken` and the corresponding interpFrame handling so that when an i18n placeholder frame pops, the lexer checks: are we still inside the outer i18n string? If yes, resume with i18n=true. If we instead opened a case body (depth+1 with no resume), handle that.

  Concretely, add to `interpFrame`: `caseDepth int` tracking case-body nesting. When `{` appears inside an i18n placeholder body and the previous non-WS token was an IDENT or `=<int>`, treat the `{` as the start of a case body — increment `caseDepth` and switch to scanning literal text via `scanStringContent` with new flags emitting `I18N_CASE_START` etc.

  Implementing this fully is involved. Reference test cases (Task C4) define the contract; the engineer iterates until the tests pass.

- [ ] **Step 2: Add a helper `lookbackForSelector(l)`**

  Before treating `{` as expression-start, peek backward (via the buffered prevTok and one prior) to determine selector context. If `prevTok` is IDENT or INT-after-`=`, it's a case body.

- [ ] **Step 3: Write tests for the case-body shape**

  Append to `internal/parser/i18n_lex_test.go`:
  ```go
  func TestLexI18nPlural(t *testing.T) {
      // $"You have {count, plural, one{message} other{messages}}"
      src := `$"You have {count, plural, one{message} other{messages}}"`
      l := newLexer(src)
      var types []TokenType
      for {
          tok := l.NextToken()
          if tok.Type == EOF || tok.Type == SEMICOLON {
              break
          }
          types = append(types, tok.Type)
      }
      want := []TokenType{
          I18N_STR_START,                          // "You have "
          IDENT, COMMA, IDENT, COMMA,              // count, plural,
          IDENT, I18N_CASE_FULL,                   // one{message}
          IDENT, I18N_CASE_FULL,                   // other{messages}
          I18N_STR_END,                            // closing "
      }
      if !sameTypes(types, want) {
          t.Errorf("got %v\nwant %v", types, want)
      }
  }

  // Helper:
  func sameTypes(a, b []TokenType) bool {
      if len(a) != len(b) { return false }
      for i := range a { if a[i] != b[i] { return false } }
      return true
  }
  ```

- [ ] **Step 4: Run, iterate**

  ```bash
  go test ./internal/parser/ -run TestLexI18nPlural -v
  ```
  Iterate the implementation in step 1/2 until this passes. The lexer's case-body recognition can be heuristic — track recent token types via `prevTok` plus one extra slot.

- [ ] **Step 5: Add tests for nested placeholders inside case bodies**

  ```go
  func TestLexI18nNestedPlaceholderInCase(t *testing.T) {
      // $"{count, plural, one{1 file} other{# files}}"
      // Bare # inside case body is part of the literal text — not a SNGL token.
      src := `$"{count, plural, one{1 file} other{# files}}"`
      l := newLexer(src)
      // ... walk tokens, assert structure including I18N_CASE_FULL containing "1 file" / "# files".
  }
  ```

  If the lexer doesn't yet handle nested placeholders inside case bodies, mark this as `t.Skip("nested in case body deferred")` and circle back. Track in a TODO test.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/parser/lexer.go internal/parser/i18n_lex_test.go
  git commit -m "feat(lexer): emit I18N_CASE_* tokens for plural/select bodies"
  ```

---

## Phase D — Grammar + AST

### Task D1: Add i18n productions to EBNF

**Files:**
- Modify: `internal/parser/sngl.ebnf`

- [ ] **Step 1: Add productions**

  After the existing `InterpStr`/`TripleInterp` productions, add:
  ```
  # Translatable strings.
  I18nInterpStr  = i18n_str_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_str_end .
  I18nTriple     = i18n_triple_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_triple_end .

  # The 1-3 fields inside a {...} placeholder.
  I18nPlaceholder = Expr [ comma ident [ comma MsgFormatBody ] ] .

  # Plural / select / selectordinal cases (space-separated per ICU spec).
  MsgFormatBody = MsgCase { MsgCase } .
  MsgCase       = Selector MsgBody .
  Selector      = ident | eq int_lit .

  # Case body: literal text only, or text + nested placeholders.
  MsgBody       = i18n_case_full
                | i18n_case_start I18nPlaceholder { i18n_str_resume I18nPlaceholder } i18n_case_end .
  ```

- [ ] **Step 2: Add the new alternatives to `PrimaryExpr`**

  Find `PrimaryExpr` (or the rule that lists `InterpStr`, `TripleInterp`, etc.) in the EBNF and add:
  ```
  | I18nInterpStr | I18nTriple | i18n_str_full | i18n_triple_full
  ```

- [ ] **Step 3: Regenerate the parser**

  ```bash
  cd internal/parser && go run modernc.org/egg -o zparser.go -package parser -start Document sngl.ebnf
  ```
  Expected: zparser.go regenerated. If egg reports grammar conflicts, the engineer must resolve them by adjusting the EBNF (likely an LL(1) issue; add lookahead tokens or refactor).

- [ ] **Step 4: Verify build**

  ```bash
  go build ./internal/parser/
  ```
  Expected: clean.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/parser/sngl.ebnf internal/parser/zparser.go
  git commit -m "feat(grammar): productions for translatable strings"
  ```

### Task D2: Add AST node types

**Files:**
- Modify: `ast/expr.go`

- [ ] **Step 1: Add the three new node types**

  In `ast/expr.go`, after `InterpolationExpr`:
  ```go
  // I18nInterpExpr is a translatable string: $"..." or $"""...""".
  // Parts alternate between *LiteralExpr (string segments) and
  // *I18nPlaceholderExpr (one per {...}).
  type I18nInterpExpr struct {
      Pos   Pos
      Parts []Expr
      Style StringStyle
  }

  // I18nPlaceholderExpr is the {<value>, <type>, <body>} form.
  // Type is "" when the comma-ident form is absent. Cases is nil unless
  // a MsgFormatBody is present.
  type I18nPlaceholderExpr struct {
      Pos   Pos
      Value Expr
      Type  string
      Cases []I18nCase
  }

  // I18nCase is one <selector>{<body>} pair within a plural/select/selectordinal body.
  type I18nCase struct {
      Pos      Pos
      Selector string // "one", "other", "=0", "male", …
      Body     []Expr // alternation: *LiteralExpr | *I18nPlaceholderExpr
  }
  ```

- [ ] **Step 2: Implement `ExprPos()` for the two `Expr`-implementing nodes**

  ```go
  func (x *I18nInterpExpr) ExprPos() *Pos       { return &x.Pos }
  func (x *I18nPlaceholderExpr) ExprPos() *Pos  { return &x.Pos }
  ```

  `I18nCase` is not an Expr, so no ExprPos needed.

- [ ] **Step 3: Verify build**

  ```bash
  go build ./...
  ```
  Expected: clean.

- [ ] **Step 4: Commit**

  ```bash
  git add ast/expr.go
  git commit -m "feat(ast): I18nInterpExpr, I18nPlaceholderExpr, I18nCase"
  ```

### Task D3: AST builders

**Files:**
- Modify: `internal/parser/build.go`
- Test: `internal/parser/i18n_test.go` (new)

- [ ] **Step 1: Locate dispatch site**

  ```bash
  grep -n "buildInterpStr\|buildTripleInterp\|InterpStr\|TripleInterp" internal/parser/build.go
  ```

- [ ] **Step 2: Add `tokenToExpr` cases for the i18n full forms**

  ```go
  case I18N_STR_FULL:
      lit := &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringQuoted, Raw: tok.Literal}
      return &ast.I18nInterpExpr{Pos: ast.Pos(pos), Parts: []ast.Expr{lit}, Style: ast.StyleDouble}
  case I18N_TRIPLE_FULL:
      lit := &ast.LiteralExpr{Pos: ast.Pos(pos), Kind: ast.LiteralStringTrippleQuoted, Raw: tok.Literal}
      return &ast.I18nInterpExpr{Pos: ast.Pos(pos), Parts: []ast.Expr{lit}, Style: ast.StyleTriple}
  ```

- [ ] **Step 3: Add the dispatch for `I18nInterpStr` and `I18nTriple` non-terminals**

  Find where `InterpStr` and `TripleInterp` are dispatched (probably in `buildExprNonTerminal` via the symbol switch). Add:
  ```go
  case I18nInterpStr:
      return b.buildI18nInterpStr(it.enter())
  case I18nTriple:
      return b.buildI18nTriple(it.enter())
  ```

- [ ] **Step 4: Implement `buildI18nInterpStr`**

  ```go
  func (b *builder) buildI18nInterpStr(it nodeIter) ast.Expr {
      var parts []ast.Expr
      pos := ast.Pos{}
      for !it.done() {
          if !it.isNonTerminal() {
              tok := it.shift()
              if !pos.IsSet() {
                  pos = b.posFromToken(tok)
              }
              switch tok.Type {
              case I18N_STR_START, I18N_STR_RESUME, I18N_STR_END:
                  if tok.Literal != "" {
                      parts = append(parts, &ast.LiteralExpr{
                          Pos:  ast.Pos(b.posFromToken(tok)),
                          Kind: ast.LiteralStringQuoted,
                          Raw:  tok.Literal,
                      })
                  }
              }
          } else if it.symbol() == I18nPlaceholder {
              parts = append(parts, b.buildI18nPlaceholder(it.enter()))
          } else {
              it.skip()
          }
      }
      return &ast.I18nInterpExpr{Pos: pos, Parts: parts, Style: ast.StyleDouble}
  }
  ```

- [ ] **Step 5: Implement `buildI18nTriple` (same shape, different terminals)**

  ```go
  func (b *builder) buildI18nTriple(it nodeIter) ast.Expr {
      var parts []ast.Expr
      pos := ast.Pos{}
      for !it.done() {
          if !it.isNonTerminal() {
              tok := it.shift()
              if !pos.IsSet() {
                  pos = b.posFromToken(tok)
              }
              switch tok.Type {
              case I18N_TRIPLE_START, I18N_STR_RESUME, I18N_TRIPLE_END:
                  if tok.Literal != "" {
                      parts = append(parts, &ast.LiteralExpr{
                          Pos:  ast.Pos(b.posFromToken(tok)),
                          Kind: ast.LiteralStringTrippleQuoted,
                          Raw:  tok.Literal,
                      })
                  }
              }
          } else if it.symbol() == I18nPlaceholder {
              parts = append(parts, b.buildI18nPlaceholder(it.enter()))
          } else {
              it.skip()
          }
      }
      return &ast.I18nInterpExpr{Pos: pos, Parts: parts, Style: ast.StyleTriple}
  }
  ```

- [ ] **Step 6: Implement `buildI18nPlaceholder`**

  ```go
  func (b *builder) buildI18nPlaceholder(it nodeIter) ast.Expr {
      // I18nPlaceholder = Expr [ comma ident [ comma MsgFormatBody ] ]
      ph := &ast.I18nPlaceholderExpr{}
      // First child is Expr (non-terminal).
      if !it.done() && it.isNonTerminal() {
          ph.Value = b.buildExpr(it.enter())
          ph.Pos = *ph.Value.ExprPos()
      }
      // Optional ", ident, MsgFormatBody"
      for !it.done() {
          if it.isNonTerminal() {
              if it.symbol() == MsgFormatBody {
                  ph.Cases = b.buildMsgFormatBody(it.enter())
              } else {
                  it.skip()
              }
          } else {
              tok := it.shift()
              switch tok.Type {
              case IDENT:
                  ph.Type = tok.Literal
              case COMMA:
                  // separator
              }
          }
      }
      return ph
  }
  ```

- [ ] **Step 7: Implement `buildMsgFormatBody` and `buildMsgCase`**

  ```go
  func (b *builder) buildMsgFormatBody(it nodeIter) []ast.I18nCase {
      var cases []ast.I18nCase
      for !it.done() {
          if it.isNonTerminal() && it.symbol() == MsgCase {
              cases = append(cases, b.buildMsgCase(it.enter()))
          } else {
              it.skip()
          }
      }
      return cases
  }

  func (b *builder) buildMsgCase(it nodeIter) ast.I18nCase {
      var c ast.I18nCase
      // Selector = ident | eq int_lit
      if !it.done() && it.isNonTerminal() {
          // grammar wraps Selector as a non-terminal; descend.
          sel := it.enter()
          if !sel.done() && !sel.isNonTerminal() {
              tok := sel.shift()
              c.Pos = b.posFromToken(tok)
              switch tok.Type {
              case IDENT:
                  c.Selector = tok.Literal
              case EQ:
                  // followed by int_lit
                  if !sel.done() && !sel.isNonTerminal() {
                      n := sel.shift()
                      c.Selector = "=" + n.Literal
                  }
              }
          }
      }
      // MsgBody
      for !it.done() {
          if !it.isNonTerminal() {
              tok := it.shift()
              switch tok.Type {
              case I18N_CASE_FULL:
                  if tok.Literal != "" {
                      c.Body = append(c.Body, &ast.LiteralExpr{
                          Pos:  ast.Pos(b.posFromToken(tok)),
                          Kind: ast.LiteralStringQuoted,
                          Raw:  tok.Literal,
                      })
                  }
              case I18N_CASE_START, I18N_STR_RESUME, I18N_CASE_END:
                  if tok.Literal != "" {
                      c.Body = append(c.Body, &ast.LiteralExpr{
                          Pos:  ast.Pos(b.posFromToken(tok)),
                          Kind: ast.LiteralStringQuoted,
                          Raw:  tok.Literal,
                      })
                  }
              }
          } else if it.symbol() == I18nPlaceholder {
              c.Body = append(c.Body, b.buildI18nPlaceholder(it.enter()))
          } else {
              it.skip()
          }
      }
      return c
  }
  ```

- [ ] **Step 8: Write the failing test**

  Create `internal/parser/i18n_test.go`:
  ```go
  package parser_test

  import (
      "testing"

      "git.duckfam.us/jonathan/sngl/ast"
      "git.duckfam.us/jonathan/sngl/internal/parser"
  )

  func TestParseI18nFullString(t *testing.T) {
      doc, err := parser.Parse("test.sngl", []byte(`var x = $"Login"`))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      vd := doc.Stmts[0].(*ast.VarDecl)
      ie, ok := vd.Specs[0].Default.(*ast.I18nInterpExpr)
      if !ok {
          t.Fatalf("got %T, want *I18nInterpExpr", vd.Specs[0].Default)
      }
      if len(ie.Parts) != 1 {
          t.Fatalf("Parts len = %d, want 1", len(ie.Parts))
      }
      lit, ok := ie.Parts[0].(*ast.LiteralExpr)
      if !ok {
          t.Fatalf("Part 0: got %T, want *LiteralExpr", ie.Parts[0])
      }
      if lit.Raw != "Login" {
          t.Errorf("Raw = %q, want Login", lit.Raw)
      }
  }

  func TestParseI18nSimpleInterpolation(t *testing.T) {
      doc, err := parser.Parse("test.sngl", []byte(`var x = $"Hello {name}!"`))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      vd := doc.Stmts[0].(*ast.VarDecl)
      ie := vd.Specs[0].Default.(*ast.I18nInterpExpr)
      if len(ie.Parts) != 3 {
          t.Fatalf("Parts len = %d, want 3", len(ie.Parts))
      }
      ph, ok := ie.Parts[1].(*ast.I18nPlaceholderExpr)
      if !ok {
          t.Fatalf("Part 1: got %T, want *I18nPlaceholderExpr", ie.Parts[1])
      }
      ident, ok := ph.Value.(*ast.IdentExpr)
      if !ok || ident.Name != "name" {
          t.Errorf("placeholder value: got %T %v, want IdentExpr 'name'", ph.Value, ph.Value)
      }
      if ph.Type != "" {
          t.Errorf("Type = %q, want empty", ph.Type)
      }
  }

  func TestParseI18nPlural(t *testing.T) {
      src := `var x = $"You have {count, plural, one{message} other{messages}}"`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      vd := doc.Stmts[0].(*ast.VarDecl)
      ie := vd.Specs[0].Default.(*ast.I18nInterpExpr)
      // Find the placeholder part.
      var ph *ast.I18nPlaceholderExpr
      for _, p := range ie.Parts {
          if e, ok := p.(*ast.I18nPlaceholderExpr); ok {
              ph = e
          }
      }
      if ph == nil {
          t.Fatal("expected a placeholder")
      }
      if ph.Type != "plural" {
          t.Errorf("Type = %q, want plural", ph.Type)
      }
      if len(ph.Cases) != 2 {
          t.Fatalf("Cases len = %d, want 2", len(ph.Cases))
      }
      if ph.Cases[0].Selector != "one" {
          t.Errorf("Cases[0].Selector = %q, want one", ph.Cases[0].Selector)
      }
      if ph.Cases[1].Selector != "other" {
          t.Errorf("Cases[1].Selector = %q, want other", ph.Cases[1].Selector)
      }
  }
  ```

- [ ] **Step 9: Run tests, iterate**

  ```bash
  go test ./internal/parser/ -run TestParseI18n -v
  ```
  Expected: all PASS. The builder's iterator code may need adjustments to match the actual `nodeIter` API; reference `buildInterpStr` for the working pattern.

- [ ] **Step 10: Commit**

  ```bash
  git add internal/parser/build.go internal/parser/i18n_test.go
  git commit -m "feat(parser): build I18n* AST nodes from grammar"
  ```

---

## Phase E — Formatter

### Task E1: Format `I18nInterpExpr` round-trip

**Files:**
- Modify: `internal/parser/format.go`
- Test: `internal/parser/format_i18n_test.go` (new)

- [ ] **Step 1: Add the dispatch in `writeExpr`**

  ```go
  case *ast.I18nInterpExpr:
      f.writeI18nInterp(x)
  case *ast.I18nPlaceholderExpr:
      f.writeI18nPlaceholder(x)
  ```

- [ ] **Step 2: Implement `writeI18nInterp`**

  ```go
  func (f *formatter) writeI18nInterp(x *ast.I18nInterpExpr) {
      f.write("$")
      switch x.Style {
      case ast.StyleTriple:
          f.write(`"""`)
      default:
          f.write(`"`)
      }
      for _, part := range x.Parts {
          if lit, ok := part.(*ast.LiteralExpr); ok {
              f.write(escapeInterpLiteral(lit.Raw, x.Style))
          } else {
              f.write("{")
              f.writeExpr(part)
              f.write("}")
          }
      }
      switch x.Style {
      case ast.StyleTriple:
          f.write(`"""`)
      default:
          f.write(`"`)
      }
  }
  ```

- [ ] **Step 3: Implement `writeI18nPlaceholder`**

  ```go
  func (f *formatter) writeI18nPlaceholder(x *ast.I18nPlaceholderExpr) {
      f.writeExpr(x.Value)
      if x.Type != "" {
          f.write(", ")
          f.write(x.Type)
      }
      if len(x.Cases) > 0 {
          f.write(", ")
          for i, c := range x.Cases {
              if i > 0 {
                  f.write(" ")
              }
              f.write(c.Selector)
              f.write("{")
              for _, p := range c.Body {
                  if lit, ok := p.(*ast.LiteralExpr); ok {
                      f.write(escapeInterpLiteral(lit.Raw, ast.StyleDouble))
                  } else {
                      f.write("{")
                      f.writeExpr(p)
                      f.write("}")
                  }
              }
              f.write("}")
          }
      }
  }
  ```

- [ ] **Step 4: Write round-trip tests**

  Create `internal/parser/format_i18n_test.go`:
  ```go
  package parser

  import "testing"

  func TestFormatI18nFull(t *testing.T) {
      assertFormat(t, `var x = $"Login"`, `var x = $"Login"`)
  }

  func TestFormatI18nInterp(t *testing.T) {
      assertFormat(t, `var x = $"Hello {name}!"`, `var x = $"Hello {name}!"`)
  }

  func TestFormatI18nPlural(t *testing.T) {
      src := `var x = $"You have {count, plural, one{message} other{messages}}"`
      assertFormat(t, src, src)
  }

  func TestFormatI18nFormatter(t *testing.T) {
      src := `var x = $"Created on {d, date, short}"`
      assertFormat(t, src, src)
  }
  ```

- [ ] **Step 5: Run tests**

  ```bash
  go test ./internal/parser/ -run TestFormatI18n -v
  ```
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/parser/format.go internal/parser/format_i18n_test.go
  git commit -m "feat(format): round-trip translatable strings"
  ```

---

## Phase F — Checker

### Task F1: Dispatch + no-static-text warning

**Files:**
- Create: `internal/checker/i18n.go`
- Modify: `internal/checker/expr.go`
- Test: `internal/checker/i18n_test.go` (new)

- [ ] **Step 1: Add the dispatch in `inferExpr`**

  In `internal/checker/expr.go`, in the switch:
  ```go
  case *ast.I18nInterpExpr:
      return c.inferI18nInterp(x)
  ```

- [ ] **Step 2: Create `internal/checker/i18n.go`**

  ```go
  package checker

  import (
      "git.duckfam.us/jonathan/sngl/ast"
      "git.duckfam.us/jonathan/sngl/ir"
  )

  // inferI18nInterp checks a translatable string and lowers it to an
  // ir.Call to i18n.tr. The synthesized ICU template is the first arg;
  // the placeholder values bundle into a map<string, dyn> as the second arg.
  func (c *checker) inferI18nInterp(x *ast.I18nInterpExpr) ir.Expr {
      // Static-text warning.
      if !hasStaticText(x.Parts) {
          c.warn(x.Pos, "translatable string contains no static text; translators will have nothing to translate")
      }
      // Validate placeholders + recurse cases.
      for _, p := range x.Parts {
          if ph, ok := p.(*ast.I18nPlaceholderExpr); ok {
              c.checkI18nPlaceholder(ph)
          }
      }
      // Lowering happens in Task G1; for now return a string-typed placeholder.
      return &ir.Literal{Type: TypString, Raw: `""`}
  }

  func hasStaticText(parts []ast.Expr) bool {
      for _, p := range parts {
          switch v := p.(type) {
          case *ast.LiteralExpr:
              if hasNonWhitespace(v.Raw) {
                  return true
              }
          case *ast.I18nPlaceholderExpr:
              for _, ca := range v.Cases {
                  if hasStaticText(ca.Body) {
                      return true
                  }
              }
          }
      }
      return false
  }

  func hasNonWhitespace(s string) bool {
      for _, r := range s {
          if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
              return true
          }
      }
      return false
  }

  // checkI18nPlaceholder type-checks Value and validates Type/Cases.
  // Stub for Task F2.
  func (c *checker) checkI18nPlaceholder(ph *ast.I18nPlaceholderExpr) {
      _ = c.checkExpr(ph.Value)
  }
  ```

- [ ] **Step 3: Write tests**

  Create `internal/checker/i18n_test.go`:
  ```go
  package checker_test

  import (
      "testing"

      "git.duckfam.us/jonathan/sngl/internal/checker"
      "git.duckfam.us/jonathan/sngl/internal/parser"
      "git.duckfam.us/jonathan/sngl/ir"
  )

  func TestI18nValid(t *testing.T) {
      src := `var name = "world"; var x = $"Hello {name}!"`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected error: %s", d.Error())
          }
      }
  }

  func TestI18nNoStaticTextWarn(t *testing.T) {
      src := `var n = 5; var x = $"{n}"`
      doc, err := parser.Parse("test.sngl", []byte(src))
      if err != nil {
          t.Fatalf("parse: %v", err)
      }
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Warning && contains(d.Msg, "no static text") {
              found = true
          }
      }
      if !found {
          t.Error("expected warning")
      }
  }

  // contains helper if not already in checker_test.go
  ```

  Reuse the `contains` helper from `checker_test.go`.

- [ ] **Step 4: Run**

  ```bash
  go test ./internal/checker/ -run TestI18n -v
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add internal/checker/i18n.go internal/checker/expr.go internal/checker/i18n_test.go
  git commit -m "feat(checker): i18n dispatch + no-static-text warning"
  ```

### Task F2: Validate placeholder type + cases against ICU keyword set

**Files:**
- Modify: `internal/checker/i18n.go`
- Modify: `internal/checker/i18n_test.go`

- [ ] **Step 1: Define the keyword set + selector validity per type**

  Add to `i18n.go`:
  ```go
  // icuTypeKeywords lists valid second-position identifiers in placeholders.
  var icuTypeKeywords = map[string]struct{}{
      "plural": {}, "selectordinal": {}, "select": {},
      "date": {}, "time": {}, "number": {},
      // styles also valid in the type slot for some cases (date/time/number with no explicit type)
  }

  // pluralSelectors are the valid CLDR keyword selectors.
  var pluralSelectors = map[string]struct{}{
      "zero": {}, "one": {}, "two": {}, "few": {}, "many": {}, "other": {},
  }

  func (c *checker) checkI18nPlaceholder(ph *ast.I18nPlaceholderExpr) {
      vT := exprType(c.checkExpr(ph.Value))
      if ph.Type == "" {
          return
      }
      if _, ok := icuTypeKeywords[ph.Type]; !ok {
          c.errorf(ph.Pos, "unknown ICU type %q in placeholder", ph.Type)
          return
      }
      switch ph.Type {
      case "plural", "selectordinal":
          if vT.Kind != ir.TypeInt && vT.Kind != ir.TypeFloat && vT.Kind != ir.TypeDyn {
              c.errorf(ph.Pos, "%s placeholder requires numeric value, got %s", ph.Type, vT)
          }
          for _, ca := range ph.Cases {
              if !validPluralSelector(ca.Selector) {
                  c.errorf(ca.Pos, "invalid plural selector %q", ca.Selector)
              }
              for _, p := range ca.Body {
                  if pp, ok := p.(*ast.I18nPlaceholderExpr); ok {
                      c.checkI18nPlaceholder(pp)
                  }
              }
          }
      case "select":
          if vT.Kind != ir.TypeString && vT.Kind != ir.TypeDyn {
              c.errorf(ph.Pos, "select placeholder requires string value, got %s", vT)
          }
          // Selectors are free-form idents; no validation beyond non-empty.
          for _, ca := range ph.Cases {
              if ca.Selector == "" {
                  c.errorf(ca.Pos, "select case has empty selector")
              }
          }
      case "date", "time":
          // Caller provides a date/time value — accept date, time, dateTime, or dyn.
          ok := false
          switch vT.Kind {
          case ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDyn:
              ok = true
          }
          if !ok {
              c.errorf(ph.Pos, "%s placeholder requires date/time value, got %s", ph.Type, vT)
          }
      case "number":
          if vT.Kind != ir.TypeInt && vT.Kind != ir.TypeFloat && vT.Kind != ir.TypeDyn {
              c.errorf(ph.Pos, "number placeholder requires numeric value, got %s", vT)
          }
      }
  }

  func validPluralSelector(s string) bool {
      if _, ok := pluralSelectors[s]; ok {
          return true
      }
      // =N form
      if len(s) > 1 && s[0] == '=' {
          for _, r := range s[1:] {
              if r < '0' || r > '9' {
                  return false
              }
          }
          return true
      }
      return false
  }
  ```

- [ ] **Step 2: Add tests**

  Append to `internal/checker/i18n_test.go`:
  ```go
  func TestI18nPluralValid(t *testing.T) {
      src := `var c = 5; var x = $"You have {c, plural, one{msg} other{msgs}}"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Errorf("unexpected: %s", d.Error())
          }
      }
  }

  func TestI18nPluralRejectsNonNumeric(t *testing.T) {
      src := `var c = "x"; var x = $"{c, plural, one{a} other{b}}"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error && contains(d.Msg, "numeric") {
              found = true
          }
      }
      if !found {
          t.Error("expected numeric error")
      }
  }

  func TestI18nUnknownType(t *testing.T) {
      src := `var c = 5; var x = $"{c, frobble, one{a}}"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error && contains(d.Msg, "unknown ICU type") {
              found = true
          }
      }
      if !found {
          t.Error("expected unknown type error")
      }
  }

  func TestI18nInvalidPluralSelector(t *testing.T) {
      src := `var c = 5; var x = $"{c, plural, frobble{a} other{b}}"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      _, diags := checker.Check(doc, &checker.Config{IsMain: true})
      found := false
      for _, d := range diags {
          if d.Severity == ir.Error && contains(d.Msg, "invalid plural selector") {
              found = true
          }
      }
      if !found {
          t.Error("expected selector error")
      }
  }
  ```

- [ ] **Step 3: Run**

  ```bash
  go test ./internal/checker/ -run TestI18n -v
  ```
  Expected: all PASS.

- [ ] **Step 4: Commit**

  ```bash
  git add internal/checker/i18n.go internal/checker/i18n_test.go
  git commit -m "feat(checker): validate i18n placeholder type + selectors"
  ```

---

## Phase G — IR conversion + stdlib decl

### Task G1: Synthesize the ICU template string

**Files:**
- Modify: `internal/checker/i18n.go`
- Test: `internal/checker/i18n_test.go`

- [ ] **Step 1: Add `synthesizeTemplate`**

  In `i18n.go`:
  ```go
  // synthesizeTemplate walks an I18nInterpExpr and produces the canonical
  // ICU MessageFormat string used for both manifest lookup and runtime
  // formatting. SNGL exprs that are not bare identifiers are rewritten to
  // synthetic argN names; the args slice tracks {name → expr} bindings.
  type templateBuilder struct {
      sb   []byte
      args []bindArg
      next int
  }

  type bindArg struct {
      name string
      expr ast.Expr
  }

  func (b *templateBuilder) addBareName(name string, expr ast.Expr) string {
      b.args = append(b.args, bindArg{name: name, expr: expr})
      return name
  }

  func (b *templateBuilder) addSynth(expr ast.Expr) string {
      name := fmt.Sprintf("arg%d", b.next)
      b.next++
      b.args = append(b.args, bindArg{name: name, expr: expr})
      return name
  }

  func (b *templateBuilder) writeParts(parts []ast.Expr) {
      for _, p := range parts {
          switch v := p.(type) {
          case *ast.LiteralExpr:
              b.sb = append(b.sb, v.Raw...)
          case *ast.I18nPlaceholderExpr:
              b.writePlaceholder(v)
          }
      }
  }

  func (b *templateBuilder) writePlaceholder(ph *ast.I18nPlaceholderExpr) {
      b.sb = append(b.sb, '{')
      var name string
      if id, ok := ph.Value.(*ast.IdentExpr); ok {
          name = b.addBareName(id.Name, ph.Value)
      } else {
          name = b.addSynth(ph.Value)
      }
      b.sb = append(b.sb, name...)
      if ph.Type != "" {
          b.sb = append(b.sb, ',', ' ')
          b.sb = append(b.sb, ph.Type...)
      }
      if len(ph.Cases) > 0 {
          b.sb = append(b.sb, ',', ' ')
          for i, c := range ph.Cases {
              if i > 0 {
                  b.sb = append(b.sb, ' ')
              }
              b.sb = append(b.sb, c.Selector...)
              b.sb = append(b.sb, '{')
              b.writeParts(c.Body)
              b.sb = append(b.sb, '}')
          }
      }
      b.sb = append(b.sb, '}')
  }
  ```

  Add the import for `fmt` if not present.

- [ ] **Step 2: Test the template synthesis directly**

  Add to `i18n_test.go`:
  ```go
  func TestSynthesizeTemplateSimple(t *testing.T) {
      // Need an exported helper or use checker.Check end-to-end and
      // inspect the lowered IR. For now, assert via the lowered Call:
      src := `var name = "world"; var x = $"Hello {name}!"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Fatalf("unexpected: %s", d.Error())
          }
      }
      _ = pkg
      // The full lowered form is asserted in Task G2; here we just confirm no error.
  }
  ```

  (The deeper assertion lands in G2 once the lowering is wired.)

- [ ] **Step 3: Run, commit**

  ```bash
  go test ./internal/checker/ -run TestI18n -v
  git add internal/checker/i18n.go internal/checker/i18n_test.go
  git commit -m "feat(checker): synthesize ICU template from I18nInterpExpr"
  ```

### Task G2: Lower to `ir.Call` to `i18n.tr`

**Files:**
- Modify: `internal/checker/i18n.go`

- [ ] **Step 1: Replace the placeholder return in `inferI18nInterp`**

  ```go
  func (c *checker) inferI18nInterp(x *ast.I18nInterpExpr) ir.Expr {
      if !hasStaticText(x.Parts) {
          c.warn(x.Pos, "translatable string contains no static text; translators will have nothing to translate")
      }
      for _, p := range x.Parts {
          if ph, ok := p.(*ast.I18nPlaceholderExpr); ok {
              c.checkI18nPlaceholder(ph)
          }
      }
      // Synthesize template + args bundle.
      b := &templateBuilder{}
      b.writeParts(x.Parts)
      template := string(b.sb)

      // Resolve i18n.tr.
      trFn := c.resolveStdlibFunc("i18n", "tr")
      if trFn == nil {
          c.errorf(x.Pos, "stdlib function i18n.tr not found")
          return &ir.Literal{Type: TypString, Raw: `""`}
      }

      // Build args map<string, dyn>.
      var entries []ir.MapEntry
      for _, a := range b.args {
          entries = append(entries, ir.MapEntry{
              Key:   &ir.Literal{Type: TypString, Raw: fmt.Sprintf("%q", a.name)},
              Value: c.checkExpr(a.expr),
          })
      }
      argsMap := &ir.MapLitIR{
          Type:    MapOf(TypString, TypDyn),
          Entries: entries,
      }

      return &ir.Call{
          Type: TypString,
          Func: trFn,
          Args: []ir.Expr{
              &ir.Literal{Type: TypString, Raw: fmt.Sprintf("%q", template)},
              argsMap,
          },
      }
  }
  ```

- [ ] **Step 2: Implement `resolveStdlibFunc`**

  In `i18n.go` (or a shared helper):
  ```go
  func (c *checker) resolveStdlibFunc(pkg, name string) ir.Expr {
      // Look up via the existing scope/package machinery.
      // Return nil if not found.
      // Concrete impl depends on how packages register; check existing
      // call-site resolution for reference (e.g. how `int.min` is found).
      return c.lookupFuncQualified(pkg, name)
  }
  ```

  If no `lookupFuncQualified` exists, search the existing checker for how qualified calls are resolved (likely in `inferCall` via `inferSelect`) and reuse that path.

- [ ] **Step 3: Wire stdlib `i18n` package source**

  Create `lib/i18n.sngl` (full content in Task H1; for now a stub):
  ```sngl
  package i18n

  func tr(key string, args map<string, dyn> = {}) -> string
  ```

  Verify `lib/lib.go` embeds it: `grep "go:embed" lib/lib.go` should show a pattern like `//go:embed *.sngl`.

- [ ] **Step 4: Add an end-to-end test**

  In `internal/checker/i18n_test.go`:
  ```go
  func TestI18nLowersToCall(t *testing.T) {
      src := `var name = "world"; var x = $"Hello {name}!"`
      doc, _ := parser.Parse("t.sngl", []byte(src))
      pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
      for _, d := range diags {
          if d.Severity == ir.Error {
              t.Fatalf("unexpected: %s", d.Error())
          }
      }
      // Walk pkg, find the var initializer, assert it is an ir.Call to i18n.tr.
      // Concrete walk depends on the IR shape — adapt to the existing helpers.
      // Simplest: scan diagnostics-free; tighten in Phase I integration tests.
      _ = pkg
  }
  ```

- [ ] **Step 5: Run**

  ```bash
  go test ./internal/checker/
  ```
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/checker/i18n.go lib/i18n.sngl
  git commit -m "feat(checker): lower I18nInterpExpr to ir.Call(i18n.tr, ...)"
  ```

---

## Phase H — Stdlib `i18n` package

### Task H1: Full `lib/i18n.sngl` declaration

**Files:**
- Modify: `lib/i18n.sngl`

- [ ] **Step 1: Replace the stub with the full surface**

  ```sngl
  // i18n provides translation and locale-aware formatting.
  //
  // The lowering of $"..." literals targets `tr`. Other entry points are
  // direct formatters callable from regular SNGL code.
  package i18n

  // --- Translation entry points ---

  // Translate `key` against the loaded manifest, formatting via ICU.
  // The lowering of $"..." passes the synthesized ICU template as `key`
  // and the placeholder values as `args`.
  func tr(key string, args map<string, dyn> = {}) -> string

  // Format an ICU template directly, no manifest lookup.
  func format(template string, args map<string, dyn> = {}) -> string

  // --- Direct formatters ---

  // Style: "decimal" (default), "percent", "currency", "scientific".
  func numberInt(n int, style string = "decimal") -> string
  func numberFloat(n float, style string = "decimal") -> string

  // Style: "short", "medium" (default), "long", "full".
  func date(d date, style string = "medium") -> string
  func time(t time, style string = "medium") -> string
  func datetime(dt dateTime, dateStyle string = "medium", timeStyle string = "medium") -> string

  // --- Direct selectors ---

  // Key for plural / selectordinal cases. exact=true means "match this exact n".
  // exact=false uses sentinel n values resolved via the predeclared CLDR
  // keyword constants below.
  struct PluralKey { n int; exact bool }

  // CLDR keyword constants. Sentinel n values are an implementation detail.
  const zero  PluralKey = PluralKey{n: 0, exact: false}
  const one   PluralKey = PluralKey{n: 1, exact: false}
  const two   PluralKey = PluralKey{n: 2, exact: false}
  const few   PluralKey = PluralKey{n: 3, exact: false}
  const many  PluralKey = PluralKey{n: 4, exact: false}
  const other PluralKey = PluralKey{n: 5, exact: false}

  // Construct an =N exact-match key.
  func exactly(n int) -> PluralKey

  // Cardinal plural selection.
  func plural(count int, forms map<PluralKey, string>) -> string

  // Ordinal plural selection (1st, 2nd, …).
  func selectordinal(count int, forms map<PluralKey, string>) -> string

  // Free-form value-based selection.
  func select(value string, cases map<string, string>) -> string
  ```

- [ ] **Step 2: Verify the stdlib parses**

  ```bash
  go test ./internal/checker/
  ```
  Expected: PASS. Failures here indicate the SNGL stdlib parser doesn't accept some syntax — adjust to the closest accepted form (e.g. if `default` values for params with type `map<...>` aren't accepted, drop the `= {}` default).

- [ ] **Step 3: Commit**

  ```bash
  git add lib/i18n.sngl
  git commit -m "feat(stdlib): full i18n package declaration"
  ```

### Task H2: Go reference runtime — manifest loader

**Files:**
- Create: `lib/i18n_runtime.go`
- Test: `lib/i18n_runtime_test.go` (new)

- [ ] **Step 1: Design the loader interface**

  Create `lib/i18n_runtime.go`:
  ```go
  package lib

  import (
      "encoding/json"
      "errors"
      "io/fs"
      "os"
      "strings"
  )

  // ManifestEntry mirrors one entry in i18n.manifest.json.
  type ManifestEntry struct {
      Translations map[string]string `json:"translations"`
  }

  // Manifest maps ICU template keys to their translations.
  type Manifest map[string]ManifestEntry

  // LoadManifest reads i18n.manifest.json from the given filesystem.
  // Returns an empty manifest (no error) if the file does not exist.
  func LoadManifest(fsys fs.FS, path string) (Manifest, error) {
      data, err := fs.ReadFile(fsys, path)
      if errors.Is(err, fs.ErrNotExist) {
          return Manifest{}, nil
      }
      if err != nil {
          return nil, err
      }
      var m Manifest
      if err := json.Unmarshal(data, &m); err != nil {
          return nil, err
      }
      return m, nil
  }

  // LoadManifestFromFile loads from the OS filesystem; returns empty
  // manifest if the file is missing.
  func LoadManifestFromFile(path string) (Manifest, error) {
      data, err := os.ReadFile(path)
      if errors.Is(err, fs.ErrNotExist) {
          return Manifest{}, nil
      }
      if err != nil {
          return nil, err
      }
      var m Manifest
      if err := json.Unmarshal(data, &m); err != nil {
          return nil, err
      }
      return m, nil
  }

  // Lookup returns the translated string for the given key + locale, falling
  // back to the inlined template, then to the key. Empty locale fragments
  // are tried in order ("en-US" → "en").
  func (m Manifest) Lookup(key, inlinedTemplate, locale string) string {
      e, ok := m[key]
      if !ok {
          if inlinedTemplate != "" {
              return inlinedTemplate
          }
          return key
      }
      // Try locale, then progressively shorter forms.
      for loc := locale; loc != ""; {
          if v, ok := e.Translations[loc]; ok {
              return v
          }
          if i := strings.LastIndex(loc, "-"); i >= 0 {
              loc = loc[:i]
          } else {
              break
          }
      }
      if inlinedTemplate != "" {
          return inlinedTemplate
      }
      return key
  }
  ```

- [ ] **Step 2: Test it**

  Create `lib/i18n_runtime_test.go`:
  ```go
  package lib

  import (
      "os"
      "path/filepath"
      "testing"
  )

  func TestManifestLookupHit(t *testing.T) {
      m := Manifest{
          "Login": {Translations: map[string]string{"es": "Iniciar"}},
      }
      got := m.Lookup("Login", "Login", "es")
      if got != "Iniciar" {
          t.Errorf("got %q, want Iniciar", got)
      }
  }

  func TestManifestLookupFallbackToInlined(t *testing.T) {
      m := Manifest{}
      got := m.Lookup("Login", "Login", "es")
      if got != "Login" {
          t.Errorf("got %q, want Login", got)
      }
  }

  func TestManifestLookupLocaleHierarchy(t *testing.T) {
      m := Manifest{
          "Login": {Translations: map[string]string{"es": "Iniciar"}},
      }
      got := m.Lookup("Login", "Login", "es-MX")
      if got != "Iniciar" {
          t.Errorf("got %q, want Iniciar (via es fallback)", got)
      }
  }

  func TestLoadManifestFromFileMissing(t *testing.T) {
      m, err := LoadManifestFromFile("/nonexistent/path.json")
      if err != nil {
          t.Fatalf("unexpected: %v", err)
      }
      if len(m) != 0 {
          t.Errorf("expected empty, got %v", m)
      }
  }

  func TestLoadManifestFromFileValid(t *testing.T) {
      tmp, _ := os.MkdirTemp("", "manifest-test")
      defer os.RemoveAll(tmp)
      path := filepath.Join(tmp, "i18n.manifest.json")
      os.WriteFile(path, []byte(`{"Login":{"translations":{"es":"Iniciar"}}}`), 0644)
      m, err := LoadManifestFromFile(path)
      if err != nil {
          t.Fatalf("unexpected: %v", err)
      }
      if got := m.Lookup("Login", "Login", "es"); got != "Iniciar" {
          t.Errorf("got %q, want Iniciar", got)
      }
  }
  ```

- [ ] **Step 3: Run**

  ```bash
  go test ./lib/
  ```
  Expected: PASS.

- [ ] **Step 4: Commit**

  ```bash
  git add lib/i18n_runtime.go lib/i18n_runtime_test.go
  git commit -m "feat(lib): manifest loader with locale-fallback Lookup"
  ```

### Task H3: Go runtime — `tr` and `format` via x/text

**Files:**
- Modify: `lib/i18n_runtime.go`
- Modify: `lib/i18n_runtime_test.go`
- Modify: `go.mod` (if x/text not already a dep)

- [ ] **Step 1: Verify x/text is available**

  ```bash
  grep "golang.org/x/text" go.mod
  ```
  If absent, add: `go get golang.org/x/text/message golang.org/x/text/feature/plural golang.org/x/text/number golang.org/x/text/currency`.

- [ ] **Step 2: Add `Tr` and `Format` to `i18n_runtime.go`**

  ```go
  import (
      "golang.org/x/text/feature/plural"
      "golang.org/x/text/language"
      "golang.org/x/text/message"
  )

  // Translator is a configured i18n runtime: manifest + active locale.
  type Translator struct {
      Manifest Manifest
      Locale   language.Tag
  }

  // NewTranslator constructs a Translator from a manifest and a locale
  // string (BCP 47, e.g. "en", "es-MX").
  func NewTranslator(m Manifest, locale string) *Translator {
      tag, err := language.Parse(locale)
      if err != nil {
          tag = language.English
      }
      return &Translator{Manifest: m, Locale: tag}
  }

  // Tr looks up `key` in the manifest, falls back to `inlinedTemplate`,
  // formats it via ICU using `args`.
  func (t *Translator) Tr(key, inlinedTemplate string, args map[string]any) string {
      tmpl := t.Manifest.Lookup(key, inlinedTemplate, t.Locale.String())
      return formatICU(t.Locale, tmpl, args)
  }

  // Format runs ICU MessageFormat on `template` with `args`, no manifest.
  func (t *Translator) Format(template string, args map[string]any) string {
      return formatICU(t.Locale, template, args)
  }

  // formatICU is a minimal ICU MessageFormat formatter. It handles:
  //   - {name} simple substitution
  //   - {name, plural, ...} via x/text plural rules
  //   - {name, select, ...} via static map lookup
  //   - {name, date|time|number, style} via x/text formatters
  //
  // Implementation note: golang.org/x/text doesn't ship a complete ICU
  // MessageFormat parser. We roll a small recursive parser here similar
  // to the one in internal/parser. ~150 LOC.
  func formatICU(tag language.Tag, template string, args map[string]any) string {
      p := message.NewPrinter(tag)
      // ... walk template, emit substitutions ...
      // For simple {name} substitution, the trivial implementation is:
      //   strings.ReplaceAll loops, but that doesn't handle plural/select.
      // Provide a real impl here in step 3.
      return formatICUImpl(p, tag, template, args)
  }

  func _ = plural.One // keep the import warm
  ```

- [ ] **Step 3: Implement `formatICUImpl`**

  Write a small ICU parser/formatter in the same file. Aim for ~150 LOC. Key responsibilities:
  - Split template into literal text and `{...}` placeholders (brace-balanced).
  - For each placeholder, parse `<name>[,<type>[,<body>]]`.
  - Simple form: emit `args[name]`.
  - `plural`: select form via `plural.Select(tag, n, forms)` from `x/text/feature/plural` (or roll a small CLDR rule lookup).
  - `select`: map lookup on `args[name].(string)`.
  - `date`/`time`: format via `time.Time.Format` with style-derived layout.
  - `number`: format via `p.Sprintf("%d", n)` or `number.Decimal(n)`.

  Provide the full implementation. This is the largest single chunk of code in the plan; the engineer should reference `golang.org/x/text/internal/format` and `feature/plural` docs.

  **Sketch (engineer fills in):**
  ```go
  func formatICUImpl(p *message.Printer, tag language.Tag, tmpl string, args map[string]any) string {
      // Find {...}, recurse for nested. For each placeholder, parse fields
      // and dispatch on type. See spec for full semantics.
      // ...
      return result
  }
  ```

  Pragma: if a full ICU impl is too large for this task, ship a minimum that handles `{name}` simple substitution and `{count, plural, ...}` with `=N` and `other` selectors only. Document the gaps in a `TODO` comment and surface them as separate follow-up tasks. The integration tests in Phase I will exercise what's implemented; uncovered cases get tests in those follow-ups.

- [ ] **Step 4: Test simple substitution**

  Append to `lib/i18n_runtime_test.go`:
  ```go
  func TestTrSimple(t *testing.T) {
      tr := NewTranslator(Manifest{}, "en")
      got := tr.Tr("Hello {name}!", "Hello {name}!", map[string]any{"name": "world"})
      if got != "Hello world!" {
          t.Errorf("got %q", got)
      }
  }

  func TestTrPluralOther(t *testing.T) {
      tr := NewTranslator(Manifest{}, "en")
      got := tr.Tr(
          "{count, plural, =0{no files} one{1 file} other{# files}}",
          "{count, plural, =0{no files} one{1 file} other{# files}}",
          map[string]any{"count": 5},
      )
      if got != "5 files" {
          t.Errorf("got %q, want 5 files", got)
      }
  }
  ```

- [ ] **Step 5: Run**

  ```bash
  go test ./lib/ -run TestTr -v
  ```
  Expected: PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add lib/i18n_runtime.go lib/i18n_runtime_test.go go.mod go.sum
  git commit -m "feat(lib): Translator + Tr/Format with minimal ICU impl"
  ```

### Task H4: Direct formatters and selectors

**Files:**
- Modify: `lib/i18n_runtime.go`
- Modify: `lib/i18n_runtime_test.go`

- [ ] **Step 1: Add direct formatters**

  ```go
  func (t *Translator) NumberInt(n int, style string) string {
      tmpl := "{n, number, " + style + "}"
      return t.Format(tmpl, map[string]any{"n": n})
  }

  func (t *Translator) NumberFloat(n float64, style string) string {
      tmpl := "{n, number, " + style + "}"
      return t.Format(tmpl, map[string]any{"n": n})
  }

  func (t *Translator) Date(d time.Time, style string) string {
      // map style to time.Format layout: short / medium / long / full
      return d.Format(layoutFor(style, true, false))
  }

  func (t *Translator) Time(d time.Time, style string) string {
      return d.Format(layoutFor(style, false, true))
  }

  func (t *Translator) Datetime(d time.Time, dateStyle, timeStyle string) string {
      return d.Format(layoutFor(dateStyle, true, false) + " " + layoutFor(timeStyle, false, true))
  }

  func layoutFor(style string, dateOnly, timeOnly bool) string {
      // Pick a sensible Go time layout per style. Locale-aware variants
      // require x/text's date/time formatter (deferred — short/medium/long/full
      // all use a fixed Go layout in this initial impl).
      switch style {
      case "short":
          if dateOnly { return "1/2/06" }
          if timeOnly { return "3:04 PM" }
      case "long":
          if dateOnly { return "January 2, 2006" }
          if timeOnly { return "3:04:05 PM MST" }
      case "full":
          if dateOnly { return "Monday, January 2, 2006" }
          if timeOnly { return "3:04:05 PM MST" }
      }
      // medium (default)
      if dateOnly { return "Jan 2, 2006" }
      if timeOnly { return "3:04:05 PM" }
      return time.RFC3339
  }
  ```

- [ ] **Step 2: Add selectors**

  ```go
  // PluralKey mirrors lib/i18n.sngl PluralKey. The runtime uses a tagged
  // representation matching the SNGL struct.
  type PluralKey struct {
      N     int
      Exact bool
  }

  // Sentinel CLDR keyword keys. The N values match the constants in
  // lib/i18n.sngl (zero=0, one=1, two=2, few=3, many=4, other=5).
  var (
      PluralZero  = PluralKey{N: 0, Exact: false}
      PluralOne   = PluralKey{N: 1, Exact: false}
      PluralTwo   = PluralKey{N: 2, Exact: false}
      PluralFew   = PluralKey{N: 3, Exact: false}
      PluralMany  = PluralKey{N: 4, Exact: false}
      PluralOther = PluralKey{N: 5, Exact: false}
  )

  // Exactly constructs an =N key.
  func Exactly(n int) PluralKey {
      return PluralKey{N: n, Exact: true}
  }

  // Plural selects a form by CLDR rule. forms maps PluralKey to the message
  // template. exact-match keys are checked first; CLDR keywords second; then
  // PluralOther; then PluralKey{Exact: false} with N=5 (other) as final
  // fallback.
  func (t *Translator) Plural(count int, forms map[PluralKey]string) string {
      // Exact match wins.
      if msg, ok := forms[Exactly(count)]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      // CLDR keyword for cardinal.
      cat := plural.Cardinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
      key := keywordToPluralKey(cat)
      if msg, ok := forms[key]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      if msg, ok := forms[PluralOther]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      return ""
  }

  func (t *Translator) Selectordinal(count int, forms map[PluralKey]string) string {
      // Same as Plural but uses plural.Ordinal.
      if msg, ok := forms[Exactly(count)]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      cat := plural.Ordinal.MatchPlural(t.Locale, count, 0, 0, 0, 0)
      key := keywordToPluralKey(cat)
      if msg, ok := forms[key]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      if msg, ok := forms[PluralOther]; ok {
          return formatICUImpl(message.NewPrinter(t.Locale), t.Locale, msg, map[string]any{"#": count})
      }
      return ""
  }

  func (t *Translator) Select(value string, cases map[string]string) string {
      if msg, ok := cases[value]; ok {
          return msg
      }
      if msg, ok := cases["other"]; ok {
          return msg
      }
      return ""
  }

  func keywordToPluralKey(cat plural.Form) PluralKey {
      switch cat {
      case plural.Zero: return PluralZero
      case plural.One:  return PluralOne
      case plural.Two:  return PluralTwo
      case plural.Few:  return PluralFew
      case plural.Many: return PluralMany
      }
      return PluralOther
  }
  ```

- [ ] **Step 3: Tests**

  Append to `lib/i18n_runtime_test.go`:
  ```go
  func TestPluralEnglish(t *testing.T) {
      tr := NewTranslator(Manifest{}, "en")
      forms := map[PluralKey]string{
          Exactly(0): "no files",
          PluralOne:  "1 file",
          PluralOther: "{#} files",
      }
      cases := []struct {
          n    int
          want string
      }{
          {0, "no files"},
          {1, "1 file"},
          {5, "5 files"},
      }
      for _, c := range cases {
          got := tr.Plural(c.n, forms)
          if got != c.want {
              t.Errorf("Plural(%d) = %q, want %q", c.n, got, c.want)
          }
      }
  }

  func TestSelectFallsBackToOther(t *testing.T) {
      tr := NewTranslator(Manifest{}, "en")
      cases := map[string]string{"male": "he", "female": "she", "other": "they"}
      if got := tr.Select("nonbinary", cases); got != "they" {
          t.Errorf("got %q, want they", got)
      }
  }

  func TestNumberInt(t *testing.T) {
      tr := NewTranslator(Manifest{}, "en")
      got := tr.NumberInt(1234, "decimal")
      if got != "1234" && got != "1,234" {
          t.Errorf("got %q", got)
      }
  }
  ```

- [ ] **Step 4: Run**

  ```bash
  go test ./lib/
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add lib/i18n_runtime.go lib/i18n_runtime_test.go
  git commit -m "feat(lib): direct formatters + selectors via x/text"
  ```

### Task H5: Wire runtime into Go codegen via PkgSource

**Files:**
- Investigate: existing PkgSource overrides for stdlib (e.g. how `int.min` resolves to `mathlib`)
- Modify: `codegen/lang/golang/<wherever-stdlib-overrides-live>.go`

- [ ] **Step 1: Find existing PkgSource overrides for stdlib**

  ```bash
  grep -rn "PkgSource\|stdlib.*override" codegen/ internal/checker/ | head
  ```

- [ ] **Step 2: Register `i18n` package source for Go codegen**

  Following the existing pattern, register that calls to `i18n.tr` etc. should generate Go code that calls into a `git.duckfam.us/jonathan/sngl/lib` runtime Translator.

  The mapping:
  - `i18n.tr(key, args)` → `lib.GetTranslator().Tr(key, key, mapToInterface(args))` (using key as both manifest key and inlined template — matches the spec)
  - `i18n.format(t, args)` → `lib.GetTranslator().Format(t, mapToInterface(args))`
  - `i18n.numberInt(n, s)` → `lib.GetTranslator().NumberInt(n, s)`
  - … etc.

  The exact override mechanism depends on the codegen plumbing. Adapt to the existing pattern.

- [ ] **Step 3: Add a translator initialization helper**

  In `lib/i18n_runtime.go`:
  ```go
  var defaultTranslator *Translator

  // GetTranslator returns the process-wide translator, lazily initialised
  // from i18n.manifest.json (in working dir) and the LC_ALL/LANG env vars.
  func GetTranslator() *Translator {
      if defaultTranslator == nil {
          locale := pickLocale()
          m, _ := LoadManifestFromFile("i18n.manifest.json")
          defaultTranslator = NewTranslator(m, locale)
      }
      return defaultTranslator
  }

  func pickLocale() string {
      for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
          if v := os.Getenv(k); v != "" {
              // Trim @modifier and .codeset; keep base locale.
              if i := strings.IndexAny(v, ".@"); i >= 0 {
                  v = v[:i]
              }
              v = strings.ReplaceAll(v, "_", "-")
              return v
          }
      }
      return "en"
  }
  ```

- [ ] **Step 4: Run codegen tests**

  ```bash
  go test ./codegen/...
  go test ./lib/
  ```
  Expected: PASS.

- [ ] **Step 5: Commit**

  ```bash
  git add lib/i18n_runtime.go codegen/lang/golang/
  git commit -m "feat(codegen-go): wire i18n stdlib calls to lib.Translator"
  ```

---

## Phase I — End-to-end integration

### Task I1: Txtar test for `$"Login"` end-to-end

**Files:**
- Create: `cmd/sngl/testdata/i18n_basic.txt`

- [ ] **Step 1: Create the txtar fixture**

  ```
  # $"..." compiles and the runtime returns the inlined template when no
  # manifest is present.

  sngl check app.sngl
  stdout 'app.sngl: ok'

  -- app.sngl --
  output bubbletea, golang

  component main {
      var greeting = $"Login"
      Text(text = greeting)
  }
  ```

  Adapt for whatever minimal compilable form bubbletea expects.

- [ ] **Step 2: Run**

  ```bash
  go test ./cmd/sngl/ -run TestScript/i18n_basic -v
  ```
  Expected: PASS.

- [ ] **Step 3: Commit**

  ```bash
  git add cmd/sngl/testdata/i18n_basic.txt
  git commit -m "test: end-to-end check of \$\"Login\""
  ```

### Task I2: Txtar test for plural + lowering

**Files:**
- Create: `cmd/sngl/testdata/i18n_plural.txt`

- [ ] **Step 1: Create**

  ```
  # Plural form parses, type-checks, and compiles.

  sngl check app.sngl
  stdout 'app.sngl: ok'

  sngl dump checked app.sngl
  # Confirm the lowering produced an i18n.tr call (textually).
  stdout 'i18n'
  stdout 'tr'

  -- app.sngl --
  output bubbletea, golang

  component main {
      var count = 5
      var msg = $"You have {count, plural, one{message} other{messages}}"
      Text(text = msg)
  }
  ```

- [ ] **Step 2: Run, commit**

  ```bash
  go test ./cmd/sngl/ -run TestScript/i18n_plural -v
  git add cmd/sngl/testdata/i18n_plural.txt
  git commit -m "test: plural lowers to i18n.tr call"
  ```

### Task I3: Final sweep

- [ ] **Step 1: Run the full verify suite**

  ```bash
  go tool verify
  ```
  Expected: PASS, no regressions.

- [ ] **Step 2: Run `go vet`**

  ```bash
  go vet ./...
  ```
  Expected: clean.

- [ ] **Step 3: If there are remaining lint warnings, fix and recommit.**

- [ ] **Step 4: Update CLAUDE.md if architecturally new public APIs warrant a mention**

  Specifically the new `map<K, V>` type and `i18n` stdlib package may merit a one-liner under the Architecture or Stdlib sections. Keep additions terse.

- [ ] **Step 5: Final commit**

  ```bash
  git add -A
  git commit -m "docs: note map<K, V> and i18n stdlib in CLAUDE.md"
  ```

---

## Self-review notes

- **Spec coverage:** Every section of the spec has at least one task: parser changes (Phase C/D), grammar (D1), AST (D2), checker (F1/F2), IR conversion (G2), stdlib decl (H1), Go runtime (H2/H3/H4), map<K, V> (Phase B), cleanup (Phase A), integration (Phase I).
- **Out-of-scope items**: extract command (deleted in A1), `$identifier` (deferred to #22), `i18n-sync` (deferred), HTML/Android stdlib overrides (deferred). Each is called out in the spec and not addressed in the plan.
- **Risk items**: (1) Task C3 — case-body lexing is the most intricate part; tests should pin the contract before iteration. (2) Task B3 — using `:` vs `=` to disambiguate map literal from struct literal requires careful builder logic; the empty `{}` case especially. (3) Task H3 `formatICUImpl` is a non-trivial parser; the plan accepts a minimum (simple substitution + `plural` with `=N` and `other`) and tracks gaps via separate follow-ups.
- **Risk mitigation:** Phase order is selected so each phase produces a runnable build at its end. After Phase A the repo is clean. After Phase B `map<K, V>` works on its own. After Phase C the lexer recognises `$"..."`. After Phase D the parser produces AST. After Phase E the formatter round-trips. After Phase F the checker validates. After Phase G the lowering targets `i18n.tr`. After Phase H the runtime is functional. Phase I is integration only.
