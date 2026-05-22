# SNGL Test Deficiencies Audit

Scope: `testdata/*.sngl` fixtures, `cmd/sngl/testdata/*.txt` txtar scripts,
all `_test.go` files in the live tree (worktrees excluded). 249 testdata
fixtures, ~115 Go test files, 5 fixtures carrying `// FOLD` directives,
4 fuzz targets, 0 calls to `t.Parallel()`.

## (a) Coverage matrix

### Stdlib components vs platform fixture coverage

Counts are how many `testdata/*.sngl` files mention each component name.
Platform-specific row (`bubbletea`, `fyne`, `android`, `gtk4`) shows how
many `cmd/sngl/testdata/*.txt` scripts compile fixtures referencing it.

| Component      | fixtures  | html | bubbletea | fyne | android | gtk4 |
|----------------|----------:|-----:|----------:|-----:|--------:|-----:|
| vbox           |        17 | many |      many |  yes |     yes |    - |
| hbox           |         5 |  yes |       yes |  yes |     yes |    - |
| text           |       169 | many |      many | many |    many |    - |
| button         |        28 | many |      many | many |    many |  yes |
| input          |         5 |  yes |         - |    - |       - |    - |
| select         |         8 |  yes |         - |    - |       - |    - |
| toggle         |         3 |  yes |         - |    - |       - |    - |
| progress       |         2 |    - |         - |    - |       - |    - |
| timer          |         2 |  yes |         - |    - |       - |    - |
| tree           |         2 |    - |         - |    - |       - |    - |
| card           |         3 |    - |         - |    - |       - |    - |
| badge          |         1 |    - |         - |    - |       - |    - |
| errorBoundary  | 1 (error) |    - |         - |    - |       - |    - |
| stack          |         1 |    - |         - |    - |       - |    - |
| **image**      |         0 |    - |         - |    - |       - |    - |
| **scroll**     |         0 |    - |         - |    - |       - |    - |
| **spacer**     |         0 |    - |         - |    - |       - |    - |
| **checkbox**   |         0 |    - |         - |    - |       - |    - |
| **radio**      |         0 |    - |         - |    - |       - |    - |
| **textarea**   |       0\* |    - |         - |    - |       - |    - |
| **spinner**    |         0 |    - |         - |    - |       - |    - |
| **tabs**       |         0 |    - |         - |    - |       - |    - |
| **link**       |         0 |    - |         - |    - |       - |    - |
| **divider**    |         0 |    - |         - |    - |       - |    - |
| **modal**      |         0 |    - |         - |    - |       - |    - |
| **drawer**     |         0 |    - |         - |    - |       - |    - |
| **tooltip**    |         0 |    - |         - |    - |       - |    - |
| **popover**    |         0 |    - |         - |    - |       - |    - |
| **splitview**  |         0 |    - |         - |    - |       - |    - |
| **table**      |         0 |    - |         - |    - |       - |    - |
| **menu**       |         0 |    - |         - |    - |       - |    - |
| **menubar**    |         0 |    - |         - |    - |       - |    - |
| **toolbar**    |         0 |    - |         - |    - |       - |    - |
| **datepicker** |         0 |    - |         - |    - |       - |    - |
| **chip**       |         0 |    - |         - |    - |       - |    - |
| **avatar**     |         0 |    - |         - |    - |       - |    - |
| **slot**       |         0 |    - |         - |    - |       - |    - |

\* `textarea` referenced only in a single CLI golden
(`compile_fyne_textarea_value.txt`), no testdata fixture.

22 of 37 stdlib components have zero `.sngl` fixtures. Fixtures lean
heavily on html (27 cmd-level golden scripts) — bubbletea/fyne/android
have 5-8 each, gtk4 has exactly 1 (`compile_gtk4_button.txt`). The
`testrunner/none` interpreter test set is exclusively html-shaped widgets
(buttons, text, lists, maps); no fixture exercises the interpreter
against `errorBoundary`, `timer`, `select`, `modal`, `tabs`, etc.

### Stdlib functions

| Namespace | covered in fixtures                                                                              | gaps                                                                                         |
|-----------|--------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| `int`     | `min/max/abs/clamp` partial                                                                      | `clamp` w/ inverted bounds                                                                   |
| `float`   | basic arith only                                                                                 | `floor/ceil/round/pow/sqrt/sin/cos/tan/asin/acos/atan/atan2` have no behavioural fixture     |
| `string`  | `upper/lower/trim/replace/substring/indexOf` in `test_stdlib.sngl`, plus `optimize_methods.sngl` | `split`, `length`, `contains`, `startsWith`, `endsWith` not in any `t.assert`-backed fixture |
| `list`    | `push`/`remove` only in `test_lists.sngl`; `filter`/`map` in `test_list_methods_typed.sngl`      | `indexOf`, `join`, `reverse`, `slice`, `contains`, `length` lack runtime-asserted fixtures   |
| `map`     | typed methods in `test_map_methods_typed.sngl`                                                   | `get` (with default), iteration order — no edge cases                                        |
| `color`   | `rgb`, `rgba`, `lighten`, `darken`, `opacity`, `hex`                                             | only present in `test_stdlib.sngl`; no platform-specific lowering test                       |
| `Alert`   | `test_alert.sngl` only                                                                           | platform output not asserted on android/fyne/bubbletea                                       |
| `File`    | not covered                                                                                      | `File.pick`, `File.pickFolder` have no fixture or integration test                           |
| `error`   | `test_error_runtime.sngl`, `test_error_handling.sngl`                                            | `errorBoundary` outside test-only path; no platform-specific render fixture                  |

### Per-language i18n runtime parallelism

| Runtime           | tests      | parity                                                                                                              |
|-------------------|-----------:|---------------------------------------------------------------------------------------------------------------------|
| `pkg/go/i18n`     |         37 | reference                                                                                                           |
| `pkg/js/i18n`     | ~24 (jest) | missing equivalents of `TestDefaultLocaleFromLANG`, `TestNumberCurrencyDefaults`, `TestPluralEnglish` ordinal cases |
| `pkg/kotlin/i18n` |         16 | thinnest; lacks template apostrophe escape, multi-locale date matrix, currency default tests                        |

## (b) Specific gaps (proposed fixture names)

### Components without any fixture (1–22)

1. `testdata/component_image.sngl` — exercise `image` with src/alt/ObjectFit.
2. `testdata/component_scroll.sngl` — vertical scroll container.
3. `testdata/component_spacer.sngl` — flex grow in hbox/vbox.
4. `testdata/component_checkbox.sngl` — bound to bool var, click toggles.
5. `testdata/component_radio.sngl` — group selection via shared var.
6. `testdata/component_textarea.sngl` — multiline input/value bind.
7. `testdata/component_spinner.sngl` — visible/hidden via bool.
8. `testdata/component_tabs.sngl` — selected index, tab change event.
9. `testdata/component_link.sngl` — href ref + window id.
10. `testdata/component_divider.sngl` — minimal render.
11. `testdata/component_modal.sngl` — open/close via var, `@dismiss` event.
12. `testdata/component_drawer.sngl` — side prop, dismiss handler.
13. `testdata/component_tooltip.sngl` — wrapping child, message prop.
14. `testdata/component_popover.sngl` — anchor + visible toggle.
15. `testdata/component_splitview.sngl` — two-pane resize event.
16. `testdata/component_table.sngl` — rows iterated from `list<T>`.
17. `testdata/component_tree.sngl` — recursive node, expand event (only 2 mentions today, no behavioural test).
18. `testdata/component_menu.sngl` + `_menubar` + `_toolbar` — menu event.
19. `testdata/component_datepicker.sngl` — bound to `date`, `@dateChange`.
20. `testdata/component_chip.sngl` — closable variant.
21. `testdata/component_avatar.sngl` — image fallback when src empty.
22. `testdata/component_slot.sngl` — children projection through slot
    (component appears in lib but no test exercises slot semantics).

### Platform-asymmetric gaps (23–32)

23. `cmd/sngl/testdata/compile_bubbletea_for_map.txt` — for-map exists for go/html/kotlin (`compile_*_for_map.txt`) but not bubbletea or fyne.
24. `cmd/sngl/testdata/compile_fyne_for_map.txt` — same gap; only html/go/kotlin tested.
25. `cmd/sngl/testdata/compile_android_for_map.txt` — same.
26. `cmd/sngl/testdata/compile_gtk4_*.txt` — gtk4 has exactly one CLI script (button); no map/list/i18n/timer/event coverage at all.
27. `cmd/sngl/testdata/compile_bubbletea_i18n.txt` — i18n is tested for android (compile_android_i18n), go, html but not bubbletea or fyne.
28. `cmd/sngl/testdata/compile_fyne_i18n.txt` — same.
29. `cmd/sngl/testdata/compile_html_locale_override.txt` — locale override golden exists for android and bubbletea but not html.
30. `codegen/platform/fyne/snapshot_test.go` — fyne `snapshot.go` exists, no test. Android has `batchsnapshot_test.go` + `compiler_test.go`, fyne has only intrinsic_*.
31. `codegen/platform/bubbletea/snapshot_test.go` — same missing.
32. `codegen/platform/gtk4/intrinsic_*` tests exist, but no compiler/view-builder integration test parallel to bubbletea/fyne.

### Untested checker error paths (33–46)

Cross-referencing the 177 `c.error(...)` sites against `testdata/error_*.sngl`:

33. `error_async_in_parameterized_reactive.sngl` — "async expression not allowed in parameterized reactive context".
34. `error_unexported_ident.sngl` — "cannot refer to unexported identifier %q".
35. `error_lvalue_address.sngl` — "cannot take address of non-lvalue expression".
36. `error_deref_non_ref.sngl` — "cannot dereference non-ref type".
37. `error_const_operand_nonconst.sngl` — `const()` cast operand not constant.
38. `error_context_decl_no_default.sngl` — "context decl requires a default value".
39. `error_context_decl_bad_name.sngl` — "context decl requires #identifier".
40. `error_context_decl_multi_default.sngl` — "context decl takes exactly one default value".
41. `error_context_default_named.sngl` — "context default must be positional, not named".
42. `error_error_boundary_no_handler.sngl` — "errorBoundary requires an @error handler".
43. `error_error_handler_arity.sngl` — "@error handler accepts at most one ErrorEvent parameter".
44. `error_error_handler_wrong_type.sngl` — "@error handler parameter must be ErrorEvent".
45. `error_import_cycle.sngl` — "import cycle detected".
46. `error_duplicate_import_replace.sngl` — "duplicate import replace for".
47. `error_non_exhaustive_select.sngl` — "non-exhaustive select on enum".
48. `error_select_empty_case.sngl` — "select case has empty selector".
49. `error_event_handler_in_output.sngl` — "event handlers not permitted in output declarations".
50. `error_output_lang_wrong_block.sngl` — "output block may only contain language targets" and inverse "language block may only contain platform targets" — neither asserted.
51. `error_root_visual_node.sngl` — "unexpected root-level visual node".
52. `error_toplevel_call.sngl` — "unexpected top-level call statement".
53. `error_int_str_conversion.sngl` — `int()` / `float()` / `string()` conversion failure paths (`%s(): cannot convert %s`).
54. `error_iter_arity.sngl` — "iter requires exactly 1 type argument".
55. `error_ref_arity.sngl` — "ref requires a type argument".

### Edge cases (56–63)

56. `testdata/edge_empty_file.sngl` — zero-byte input.
57. `testdata/edge_only_comments.sngl` — file containing only comments / blank lines.
58. `testdata/edge_unicode_idents.sngl` — non-ASCII identifiers and string interpolation (NFC/NFD).
59. `testdata/edge_long_string.sngl` — multi-kB single string literal (no current fixture > a few hundred chars).
60. `testdata/edge_deep_nesting.sngl` — 100-level nested `vbox`/`if` to surface recursion-limit handling in parser/checker/lower.
61. `testdata/edge_long_function_chain.sngl` — `s.upper().lower().trim()...` long chain to stress method-chain checker.
62. `testdata/edge_huge_int_literal.sngl` — `int` literals at and over int64 boundary (already a `error_integer_overflow.sngl`, no boundary-success fixture).
63. `testdata/edge_recursive_component_depth.sngl` — explicit recursion guard; `test_recursive_component.sngl` exists but uses bounded recursion.

### Tests that don't meaningfully assert (64–69)

64. `testdata/test_basic.sngl`, `testdata/minimal.sngl` — declare values but no `t.assert`; they only exercise parse/check, not behaviour. Convert to typed assertions or rename to `check_*.sngl`.
65. `testdata/i18n_browser_smoke.sngl`, `testdata/i18n_android_smoke.sngl` — "smoke" naming suggests no per-output expectation; verify the runner actually compares rendered text.
66. `testdata/visual_patterns_multiline.sngl`, `testdata/visual_patterns.sngl` — pure layout dumps; no FOLD/ERROR/assert directives.
67. `testdata/style_decl.sngl`, `testdata/style_forms.sngl` — same shape; checks parse only.
68. `codegen/platform/none/testrunner/runner_test.go` — single `TestRunFixtures` driver. If a fixture lacks any `t.assert`, the test still passes; no guard against fixtures with zero assertions.
69. `internal/parser/format_test.go` — `TestFormat*` tests compare formatted output against expected strings but do not assert reparse-stability of the formatted output for each named case (only the fuzz target does); add explicit round-trip per case.

### Stale / risky golden files (70–74)

70. `cmd/sngl/testdata/compile_html_minify.txt` — minified output goldens are notoriously brittle and mask whitespace regressions; verify regen procedure.
71. `internal/lower/testdata/*.txtar` — golden tests catch IR drift but every checker change requires manual regen; no comment annotations explain intent of each golden.
72. `lib/snapshots/` — present in tree but not referenced from any `_test.go`; suspected stale. Verify or delete.
73. `public/snapshots/` and `tmp/main_site/assets/snapshots/` — outputs only, but `go:generate` line in `generate.go` writes them and there is no test that diffs against committed copies.
74. `testdata/lsp_hover/snapshots/` — covered by `internal/testutil/hover_test.go`; verify all `.sngl` files under `testdata/lsp_hover/` have a snapshot (manual inspection: every `*.sngl` does map; OK).

### Missing test areas (75–88)

75. **Optimize pass — only 5 FOLD fixtures** (`optimize_arithmetic`, `optimize_fold`, `optimize_interp`, `optimize_math`, `optimize_methods`). No fixtures for `optimize/shake.go` (dead code), `optimize/specialize.go`, `optimize/expand.go`, `optimize/inline.go`. Add `testdata/optimize_dce.sngl`, `optimize_inline.sngl`, `optimize_specialize.sngl`, `optimize_expand_list_lambda.sngl`.
76. **Lower pass — no per-feature lowering fixture asserts beyond golden txtars**. Add `testdata/lower_async_*.sngl` and `lower_context_*.sngl` with explicit IR-shape assertions instead of opaque golden files.
77. **IR nodes** — `ir/intrinsics_test.go`, `ir/async_test.go`, `ir/color_test.go`, `ir/context_test.go`, `ir/pointsto_test.go`, `ir/types_test.go` exist. **No tests for `ir/scope.go`, `ir/defaults.go`, `ir/strip.go`, `ir/expr.go`, `ir/stmt.go` independently.**
78. **Formatter round-trip** — only the FuzzDocument target enforces it; no static fixture list. Add `internal/parser/roundtrip_test.go` that walks every `testdata/*.sngl` and asserts `Format(Parse(src))` reparses to identical AST.
79. **LSP** — many `lsp_*_test.go` files but no test for `internal/lsp/preview_*` round-trip with mutation, no test for semantic tokens against unicode identifiers, no test for completion mid-string interpolation, no test for hover on stdlib `Style` fields specifically.
80. **CLI** — `cmd/sngl/preview.go`, `cmd/sngl/snapshot.go`, `cmd/sngl/snapshot_examples.go`, `cmd/sngl/build.go` have no dedicated `.txt` golden scripts. Add `cmd/sngl/testdata/preview.txt`, `snapshot.txt`, `snapshot_examples.txt`, `build.txt`.
81. **dump subcommand** — only `dump_lowered_list.txt`; no golden for `dump parsed`, `dump checked`, `dump optimized`, `dump analysis`. Add four scripts.
82. **format subcommand** — `fmt.txt` and `fmt_errors.txt` exist but no `fmt --check` mode test, no test of stdin pipe behavior.
83. **Playground/WASM** — `internal/playground/api_test.go`, `minify_test.go` cover the HTTP API + minifier. **Zero tests for the `//go:build js` codepath** (`runtests_js.go` files in bubbletea/fyne, playground wasm). No way to detect breakage of the WASM build short of `GOOS=js GOARCH=wasm go build`.
84. **Snapshot / imgdiff** — `internal/imgdiff/imgdiff.go` has no `_test.go` whatsoever. Add tests for pixel tolerance, alpha handling, size-mismatch error.
85. **docsgen** — `internal/cmd/docsgen/main.go` has no tests; `internal/docbrowser/server.go` (currently in working-tree modification) has no `_test.go`. Add `internal/docbrowser/server_test.go` for routing and frontmatter parsing.
86. **tsgen / specgen / ebnf2ts** — `internal/cmd/{tsgen,specgen,ebnf2ts}` have no tests.
87. **Schemes** — `codegen/scheme/file/file.go` and `codegen/scheme/git/git.go` have **no `_test.go`**. C, go, http, js schemes all have importer tests. Add `file_test.go`, `git_test.go` (mock-backed for git).
88. **Android scaffold / gradle template** — `codegen/platform/android/{build.go,toolchain.go,gomobile.go,directbuild.go,scaffold.go,icon.go}` have no dedicated tests; only the IR compiler is covered.

### Generic methods matrix (89–94)

89. `testdata/list_filter_string.sngl` — `list<string>.filter` (existing `test_list_methods_typed.sngl` only covers `list<int>`).
90. `testdata/list_map_chain.sngl` — `list.filter().map()` chained.
91. `testdata/list_map_lambda_capture.sngl` — lambda closing over outer var.
92. `testdata/map_keys_values_int_value.sngl` — `map<string,int>.values()` (existing fixtures lean on `map<string,string>`).
93. `testdata/map_contains_missing.sngl` — `m.contains(k)` for absent key + `m.get(k, dflt)`.
94. `testdata/list_method_on_iter.sngl` — confirm/deny that `iter<T>` exposes any methods (today it should not; assert error).

### i18n parity gaps (95–99)

95. `pkg/js/i18n/locale_currency_test.js` — currency default per locale (matches `pkg/go/i18n/TestNumberCurrencyDefaultsByLocale`).
96. `pkg/js/i18n/apostrophe_test.js` — ICU apostrophe escape (matches `TestTrApostrophe`).
97. `pkg/kotlin/i18n/I18nTest.kt::testApostropheEscape` — same.
98. `pkg/kotlin/i18n/I18nTest.kt::testDefaultLocaleFromLANG` — env-driven default locale (Go has 4 variants, Kotlin has 0).
99. `pkg/kotlin/i18n/I18nTest.kt::testNumberCurrencyInTemplate` — template-driven currency formatting.

### Fuzz / race (100–104)

100. **`internal/imgdiff/fuzz_test.go`** — fuzz two random small bitmaps; verify Compare doesn't panic.
101. **`internal/parser/format_fuzz_test.go`** — fuzz formatter idempotence: `Format(Format(x)) == Format(x)`.
102. **`codegen/lang/javascript/minify_fuzz_test.go`** (or wherever minify lives) — minified output must reparse to same JS AST.
103. **`go test -race ./...` baseline** — repo has 21 sites using goroutines or sync primitives but **zero `t.Parallel()` calls** in the entire live tree. Either the test suite genuinely never exercises concurrent paths, or it's missing race-coverage. Add `t.Parallel()` to leaf-level tests where safe; add a dedicated `internal/checker/concurrent_test.go` that calls `checker.Check` from many goroutines (registry, stdlib FS access).
104. **`codegen/registry_test.go`** — `codegen/registry.go` claims thread-safe registration via mutex; no test asserts concurrent `RegisterLang`/`RegisterPlatform` is race-free.

### TestRunner (none) coverage (105–108)

105. Interpreter doesn't exercise `errorBoundary`/`error.raise`; the only error fixture is `test_error_runtime.sngl`. Add fixture that nests boundaries and asserts the innermost handler fires.
106. Interpreter coverage for `timer` is `test_timer.sngl` only — single-shot; no fixture covers `timer` cancellation, multi-tick, or `Test.wait` timeout exhaustion.
107. No `none`-platform test for `i18n.plural` runtime-side selection across multiple locales (only `test_i18n_plural.sngl`, en-only).
108. `Test.setContext` is documented as the replacement for `Test.setLocale` but only `test_context_setContext.sngl` exercises it; no negative test (non-context name, non-string value).

---

Totals: 108 specific gaps. Hot spots: 22 untested stdlib components, 23
checker error paths without `error_*.sngl` fixtures, gtk4 platform almost
entirely untested, file/git URL schemes untested, imgdiff/docbrowser/
docsgen/tsgen untested, no `t.Parallel` anywhere in the repo, WASM build
has no Go-level test coverage.
