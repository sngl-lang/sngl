# TODO

## Style fields, translated in SNGL

P-1's third part, on !257's branch (`refactor/android-select-state`), which
already lets a target package's override hold state, writes android's select
as an override, takes every gtk4 setter from GIR, and has `match` merged in.

### Where it stands

Each platform's Go switches on `ui.Style` field names, and drops every field
it does not name with no diagnostic. On android, bubbletea and fyne the work
is done twice: a Style written as a literal is mapped field by field in Go,
and a Style held in a value goes through a runtime helper the Go writes out
(Kotlin `Modifier.snglStyle`, Go `__styleLipgloss`, `__styleTheme`). The two
copies disagree:

- android's helper drops flex, fontFamily, borderRadius and button colours,
  and maps textAlign center/right only.
- bubbletea's helper renders `opacity=1` faint.
- All three helpers read `.px` and ignore em and rem.

What each platform answers today:

| Platform  | Fields it maps (of 52)                                     |
|-----------|------------------------------------------------------------|
| html      | all, to the CSS property of the same name                  |
| android   | done in SNGL (style.sngl); `unstyled` lists the rest       |
| bubbletea | ~25; a `Widget` drops its style whole                      |
| fyne      | ~9, through theme overrides and the flex layout            |
| gtk4      | none; only a markup flow reads color, size, family, weight |

The places to read first: `codegen/iterate.go` (`NodeStyleFieldsOf`, which
decides literal vs held), `codegen/measurement.go`, android's finished
`lib/platform/android/style.sngl` as the pattern, bubbletea's `view_ir.go`
(`irStyleCall`) and `style_value.go`, fyne's
`spec.go`, `layout.go`, `theme.go` and `pkg/go/fynetheme`, gtk4's
`intrinsic_translator.go` (`stylePropName`) and `markup.go` (`flowTags`),
and `internal/htmlutil/style.go`.

### The design

No new language feature. Each platform package translates a Style with a
`const func` written in SNGL over the host declarations it already has --
`#[kt.native]` Modifier and TextStyle calls on android, `#[go.native]`
lipgloss methods on bubbletea, fynetheme on fyne -- and every override calls
it on its `style`. `match` reads an enum field (fontWeight, textAlign) into
the host's constant. One function serves a literal and a held Style alike, so
the two copies above become one; for html, a pure function building CSS text
folds at build time and a literal still becomes static markup.

Fields a parent consumes -- `flex`, fyne's `margin`, Compose's
`Modifier.weight`, which exists only in a Row or Column scope -- are answered
by a context: vbox and hbox provide their axis, and the child's function reads
it. On Compose the call is then emitted inside `Row { }`, where `weight`
compiles. On fyne the layout reads each child's weight and margin off the
child's widget rather than taking them as arguments, a fynelayout change.

gtk4 gets a CSS setter in gtk4rt, exposed as a prop of the GIR widget
declarations, so reactivity re-sets it when the style changes; an effect is
for what the host adds and removes with the node, such as a CssProvider.

**Silence is an error, as a repo test.** Each platform declares the fields it
cannot express, with a reason, in a `const` list. A test in `lib/` holds the
fields its style functions read, together with that list, to every field of
`ui.Style`, so adding a field fails each platform until it answers. (Still to
confirm with Jonathan: whether a program's write of a field its target lists
should also warn at the write.)

When a platform's translation is SNGL, the Go it replaces goes: the field
switches, the held-Style helpers, and the per-node routing that the overrides
now say themselves (android's `modifier=` / `colors=` / `shape=` / Text
`style=`; bubbletea's flow split).

### Order

Each is a commit series on the branch; one MR, so CI runs once.

1. **android.** Fixture first: the audit's repro, `margin` and `borderRadius`
   written on android, both dropped today. Then `modifier`, `textStyle`,
   `buttonColors` and `shape` functions in `lib/platform/android/style.sngl`,
   the axis context on vbox/hbox, the overrides calling them, and the Go
   above deleted. Done. The axis is a closure rather than an enum: vbox
   and hbox provide `flexShare`, a `func(float32) Modifier` written in the
   Column's or Row's content, so a component built at run time -- a
   composable of its own, outside that scope -- still calls `weight` through
   it. Nothing in style.sngl reads a composable: under `--lang go` a ternary
   splits a function into statements no splice takes, so the theme's text
   style and a button's default colors are read in the overrides.
2. **bubbletea**, the lipgloss chain; a `Widget`'s style answered or listed.
3. **fyne**, with the fynelayout change for flex and margin.
4. **gtk4**, with the gtk4rt CSS setter.
5. **html**, last, since it already maps every field.
6. The coverage test across all six, and CLAUDE.md's platform sections.

### Found on the way, not yet fixed

- A literal Style's Modifier is built at run time on android,
  `styleModifier(Style(…))`: the optimizer splices single-expression
  functions only, and the Modifier builder is a block.
- android's `unstyled` still lists fields Compose can express: `gap`,
  `alignItems` and `justifyContent` (Column and Row arguments), `maxLines` and
  `whiteSpace` (Text arguments). Each would put an argument on every node.
- On android a Column the Go writes rather than an override -- a window's
  content arriving through a slot, a nav page of several nodes -- provides no
  `flexShare`, so a `flex` child there fills the screen; a page under an hbox
  inherits the Row's closure. Both took a Column weight before.
- A side written `0px` beside a non-zero whole (`{padding=16px, paddingTop=0px}`) reads as unset, so it is 16 on android and 0 on html: zero
  is unset in every Style.
- On clean main, `ui.select` does not compile on bubbletea under a `for`
  (`too many arguments in call to m.widget0Copy`), nor on fyne or gtk4 under
  a reactive `if` (`undefined: o`; a missing `c.__n1`).
- passImplicitState refuses a call site that binds one two-way prop and
  leaves another unbound.
- android's select honors none of `disabled`, `multiple`, `@focus`,
  `@blur`.

## `match`, later

`match` is implemented: the rules are in CLAUDE.md's *Match* section and the
specification's *The match statement*. What is left is the longer road.

### Generated code a person would write

The checker turns a match into an if/else-if chain or nested ternaries, which
every target emits unchanged, so the first implementation needed no backend
work. The cost is the shape of the output: Go gets `if … else { if … }`,
Kotlin nested `if` expressions, JavaScript chained `?:`, where each has a
construct that says it -- Go's `switch`, Kotlin's `when`, JavaScript's
`switch` -- and an exhaustive match over an enum is exactly what a compiler
checks a `when` for.

1. Keep the match in the IR: an `ir.Match` statement and expression the
   checker produces instead of the chain, with the subject, the arms'
   patterns and whether the last arm is taken unasked.
2. Lower it to the chain in a pass every target gets unless its language
   declares `#[gen.can(match)]` -- the capability polarity `sngl:x/gen`
   already uses, so a target that has not heard of it keeps working.
3. Teach the walks that ask an `if` what it holds to ask a match the same:
   `ir.TransparentBlocks`, the family, slot, element-ref and `.empty` rules,
   the interpreter, the optimizer's folds, the reactivity pass's render slots.
   Each is a case beside the `if` it already has, and the reason the first
   implementation did not keep one.
4. Emit it natively in Go, Kotlin and JavaScript, view bodies included where
   the platform renders statements (Compose's `when` in a composable).

### Language

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
