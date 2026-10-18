# Built-in generic types, structs and units

The types themselves are the spec's (*Types*). A `map[K, V]` is Go's
`map[K]V`, JavaScript's `Map` and Kotlin's `Map<K,V>`.

**An anonymous struct is its fields.** `struct {}` and `{a int}` written
inline are interned per package as a synthesized declaration
(`checker.internAnonStruct`, `StructDef.Anon`), whose host name is derived
from the field signature alone. Two of them are one type when they have the
same fields at equal types, whichever package spelled them (`Type.Equal`):
`sngl:ui/nav`'s page family defaults `meta` to its own `struct {}`, and a
program declaring the prop writes the program's. Compared by declaration
instead, the two never matched.

**An anonymous struct may name a type parameter**, wherever a type is written:
`func unwrap[T any](w struct { v T }) T`. Such a struct is declared and
interned nowhere (`anonStructType`, `ir.IsUninternedAnon`, which is an
anonymous `StructDef` with no name), since its fields mean the parameters in
scope where it was written: two declarations each writing `struct { v T }`
name two `T`s, where interning by the field signature had handed the second
the first one's, constraint and all. `Type.Substitute` substitutes it field
by field into another un-interned declaration, `ir.BindTypeParams` binds `T`
from the argument's field by name (`ir.FieldsOf`, so `{v=1}`, a named struct
with a `v` and a `T` constrained to one bind alike), and `HasTypeParam` reads
its fields; `Type.Equal` needed nothing, comparing anonymous structs by
fields already.

**A type is walked by `ir.Type.Any` and rebuilt by `ir.Type.MapChildren`**
(`ir/typewalk.go`), and nothing else restates which types are inside one:
the elements, the signature's parameters and result, and an un-interned
anonymous struct's fields. `HasTypeParam`, `Substitute`, the monomorphizer's
struct rewrite and `passAnonStructs` are each a predicate or a rewrite handed
to one of the two. They were six walks, and each one written after
anonymous structs could hold a `T` had to remember the fields --
`mentionsTypeParam`, a seventh, did not, so a component's prop typed
`struct { v T }` bound nothing. The checker's and the monomorphizer's
binders are one `ir.BindTypeParams` for the same reason; the monomorphizer
treats a binding that names a type parameter as none. A constraint naming a type parameter is this same struct. A
substitution that makes one concrete names a declaration no package declares,
so the always-on `passAnonStructs` points it, after every pass that copies a
body at bound types and before any that names a struct (`NoStructSpread`
names a merge helper for one), at the package's interned struct with those fields, declaring one under the name the
checker would have given it (`ir.AnonStructBaseName`) -- giving the
instantiation a generic declaration's `Elems` instead would have made
`Box__int`-style copies, a second host type beside the literal's that Go and
Kotlin cannot assign between (`testdata/generic_anon_struct.sngl`,
`error_generic_anon_struct.sngl`, `generic_anon_struct.txtar`). The pass also
merges two *interned* declarations of equal fields into the first: a
directory-imported package interns its own `struct { v int }`, which spells
the program's host name, and the output declared both
(`testdata/anon_struct_from_package.txtar`).

Four things follow from a struct having no one declaration. Its identity is
its fields everywhere, so the intern key spells an anonymous struct inside a
field by *its* fields rather than its address (`ir.AnonTypeKey`), or
`{o=wrap(1)}` and `{o={v=1}}` interned two Go types,
and a spread's source is measured with `Type.Equal` rather than by
declaration. A value the optimizer folds out of a generic body carries the type
its literal was checked at there, `struct { v T }`, so `irFromValue` takes the
type the call was checked at instead, and its fields' types for the fields'
values -- read off the values, `wrap[option[int]](3)` was a
`struct { v int }`. **A generic component is spliced at the types its call site bound**
(`ir.InstantiateStmts`, `ir.ComponentBindings` off `NodeInst.TypeArgs`): the
optimizer's `inlineComponentCall`, `passInlinePure` and
`passNoInlineComponents` all copy its body that way (`splicedBody`,
`cloneHoistedVar`), and the latter instantiates each method it hoists. Left
as written, its state and methods reached Go as `any` and Kotlin as an
unnamed type -- a plain `var held T` too, which is older than anonymous
structs (`generic_anon_struct.txtar`'s `tally`).

**A generic component built at run time is copied per instantiation** on a
target not holding `genericComponents` (Go and Kotlin; html holds it under any
`--lang`, its instances being the page's JavaScript), as a function is by
`passMonomorphize` and a struct by `monomorphizeStructs`: `show[int]` renders
`show__int`, whose props, events, slots, state, methods and body hold the types
its call site bound (`ir.InstantiateComponent`, which owns what the component
declares and shares the rest). The copy is made where passNoInlineComponents
elects the instantiation to be built (`instantiateRuntime`), the one place that
knows -- a spliced one is already at its types -- and everything after reads it
as an ordinary component, `passInstanceSlots`' per-content copies and android's
composables included. Emitted once with T erased, a `list[T]` prop was `[]any`
on fyne, which the call site's `[]int` is not, and `struct { v T }` had no
spelling at all, so a component naming one was refused. A recursion's copy
joins the cycle and points its own renders of the cycle at their copies; one
that renders itself at a deeper type at every level would need a copy per
level, and is refused at that render past `maxInstances` copies
(`testdata/generic_component_runtime_instance.txtar`,
`cmd/sngl/testdata/generic_component_runtime_instance_runs.txt`).

**`==` is `interp.Equal`, in the interpreter and in the optimizer's fold**: a
struct field by field by name, a list element by element, a number by value.
It says where it cannot answer (a map, two scalars of different kinds), which
the fold leaves to run and the interpreter compares by rendered form as it
always did. Two copies had drifted: the fold compared a struct's pointer and
panicked on a list.

**A unit's members are its bases, and they are registered like any other
declaration's.** A unit value is a magnitude per base (`ir/units.go`): `unit measurement { px, em, rem }` declares three bases, so a
value carries three numbers and every backend emits it as a record of them.
`ir.UnitDef.Fields` is that record's member table — `ir.UnitFields`, one
`float` per base, built at registration — and `ir.Fielded` is what a struct and
a unit answer it through, the field half of what `ir.methodTable` already does
for methods across four kinds. `checker.selectDeclaredMember` is the one lookup
both use.

A **single-base** unit has no members at all. `unit tick { tk }` is `type Tick float64` in Go, a `Double` in Kotlin and a number in JavaScript: there is
nothing to select, and `float(x)`/`int(x)` is how that magnitude is read. So
`t.tk` is an unknown member like any other, and a *reduced* suffix is one too —
in `unit width { cm, mm = 0.1cm }`, `mm` is a tenth of a cm and no value
carries a magnitude for it.

`inferSelect` looks a unit member up in that table, so a member the unit does
not declare (`m.value`) is an error, and a base (`m.px`) is a `float`, which
takes the string conversion every other numeric operand gets in an
interpolation.

**A cast reads the magnitude, and it reads it in the unit's base.** `int(x)`
and `float(x)` are the whole of how a single-base unit's value is got at, so
every target has to answer them the same. A **multi-base** unit is refused
there (`multiBaseUnitCast`): a value of one is a magnitude per base, so there
is no single number to produce, and the diagnostic points at the per-base read
that replaces the cast — `m.px`. That is the same premise the checker already
applied to ordering two of them, extended to the one other place it decides
anything. `string(m)` is untouched, being display rather than a magnitude.
`duration` is the one that did not agree: Go carries one as a `time.Duration`,
whose unit is nanoseconds, while the declared base is ms — so
`GoIRContext.durationToNumber` divides at the cast. Only at the cast, because
arithmetic stays inside the representation:
`d + 100ms` and `d * 2` are `time.Duration` arithmetic and are correct in ns
right up to the cast that divides them out. `float` divides as floats, since an
integer division converted afterwards truncates a sub-millisecond duration to
zero.

`ClassifyUnit` is what gates it, and it reads `UnitDef.Builtin` —
`ir.BuiltinDuration`, the kind `lib/time/time.sngl` marks — rather than the
name `duration`. It asked the name in four places, which made the one built-in
with a host representation the one built-in that was *not* shadowable: a
program's own `unit duration { blip }` got `time.Duration(3) * time.Millisecond`
for `3blip`, the `time` import, the `mustParseDuration` helper and a Kotlin
`Long`. `testdata/unit_duration_shadowed.txtar` pins that it no longer does.
`golang/helpers.go`'s `case "duration":` stays, switching on a scheme type
*hint* beside `"color"` and `"date"` rather than on a declaration.

**And the display, which is the same claim made without a cast.** A unit
interpolated bare renders as its magnitude per base on every target: `"{d}"`
of `500ms` is `500ms`, `"{m}"` of `3px + 2em` is `3px + 2em`. The rule is the
representation restated rather than a second decision on top of it, and
`ir.FormatUnitTerm`, `ir.UnitTermSep` and `ir.FormatUnitZero` are the one
place it is written down — the interpreter and the optimizer call them, and
each backend emits a runtime helper saying the same thing about values no
compile-time caller can see.

It was **five** answers, not one per target. A var-held `500ms` printed
`500ms` on Go, `500` on JavaScript, `500.0` on Kotlin and `500ms` on the
interpreter; a `const` one printed `500` on all four, because
`optimize.evalConversion` folded a unit to the bare float64 its magnitude is
and spelled that back with `%v`. Go's apparent agreement was a coincidence of
the value: `time.Duration.String` normalises across units, so 1100ms prints
`1.1s` for a unit whose declared base is ms. And a multi-base value had no
chosen spelling anywhere — `{3 2 0 0 0}` on Go, `[object Object]` on
JavaScript, `Measurement(px=3.0, …)` on Kotlin — because nothing had ever
asked the question; those are three host defaults leaking.

Two consequences, and each is the representation asserting itself. **The
written suffix is gone from display**: `2s` shows `2000ms`, because the record
is the whole of what a compiled target holds and a spelling to prefer was a
memory only `interp.unitValue.Suffix` had. And an **all-zero value prints in
the first declared base**, so `0em` and `0px` both print `0px` — they are the
same value under per-base equality, and equal values have to print equally.

Go needs a helper per unit (`HelperSet.UnitStrings`, emitted by
`EmitUnitStringFuncs`) because it has no expression form for the join;
JavaScript and Kotlin inline an arrow and a `let`, which is also what binds
the operand so it is not re-evaluated once per base. Each helper spells its
magnitude with whatever that target already spells a bare float with —
`fmt.Sprint`, `String`, `_snglFloatStr` — so unit display inherits the float
agreement instead of restating it and drifting from it.

**The CSS path is not this path**, which is what made unifying it cheap rather
than a trade against html. A style prop is spelled by `internal/htmlutil`:
`UnitLiteralToCSS` off an `ir.Literal`, the base name being the CSS unit. It
shares `ir.UnitMagnitude` with the above and nothing else, reaches
`interpolateStringify` nowhere, and `7px` in a stylesheet is unchanged by any
of this.

**A measurement means one length on every target.** `px` is a
device-independent pixel and `em` and `rem` are font sizes: html hands all
three to CSS, and every other target, having no cascade, reads both as its
base text size -- 16 on android, 14 on fyne (the default theme's), 11 on
gtk4 (Adwaita's point size) and 16px, two cells, on bubbletea -- as one
number, px + base*(em + rem) (`codegen.MeasurementPx` for a constant,
`MeasurementSum` and `MeasurementPxExpr` for a value read at run time). A
canvas's surface is pixels everywhere, so an em there is 16
(`codegen.CanvasEmPx`). Each target used to read a constant's magnitude
whatever its base -- `12em` was 12dp on android, `fontSize=17em` 17 on fyne --
and a run-time value's px alone. `pct`, `vw` and `vh` went for the reason a
base has to be every target's: a percentage padding exists on no host but
html, and a viewport unit would have to follow a resize on hosts that apply a
style once. `rem` was `16em`, which made `13rem` 208em on html.

The interpreter is a fourth implementation of all of this and has to be checked
with the three backends: `interp.ToInt` needs its `unitValue` case (without it
every `int(<unit>)` was 0 on `--platform=none`), `evalSelect` needs one to read
a per-base member, and `unitTable.BaseOf` is what says which base a suffix
reduces to, so `2s` counts in ms. `cmd/sngl/testdata/unit_magnitude_compiles.txt`
runs one program under both `--platform=bubbletea --language=go` and
`--platform=none` for exactly that reason.

`interp.unitValue` **is** that record — `Amounts`, a magnitude per base, plus
the suffix the value was written with for display. It was one `BaseAmount` and
a suffix, the pre-`ea7b2f84` "a unit reduces to one number" model, and the
member table is what made that visible: `1px + 2em` added to 3 and then
answered 3 for `px` and 0 for `em`, so a per-base read off a value *arithmetic
built* was quietly wrong where the same read off a literal was right. Both
fixtures had only literals, which is why neither caught it.

Four things follow, and the first three are the record restated:

- **Arithmetic is per base.** `Add`/`Sub` combine base by base and `Scale`
  scales each; a base whose magnitude is zero is absent rather than stored.
- **Equality is per base**, and **ordering is refused** for a multi-base unit
  (`orderable`): `3px` and `2em` are each the larger on their own base, so
  there is no answer to invent. The checker already refuses the pair
  (`IsSingleBaseUnit` gates `<`, `<=`, `>`, `>=`), so the guard is for IR
  the checker did not see.
- **A cast reads `magnitude()`** — the one base of a single-base unit, which is
  every cast the checker's ordering rule leaves meaningful.
- **Display sums the bases it carries**: `3px + 2em`, and a bare `3px` for a
  value that only ever names one, which is what a program that never mixes
  bases sees. In the bases, not in the spelling — `1s` displays `1000ms`.

The timer is unaffected: `durationFromMs` goes through `toFloat`, and a
duration is single-base.

**The optimizer holds a multi-base value as the interpreter's**
(`interp.UnitLiteralValue`, combined by `interp.UnitBinary`), since no number
can stand for one, and `irFromValue` spells it back one literal per base. It
used to be unevaluable, and so was any struct or list holding one: html's
static unroll of a loop over rows carrying an indent wrote one copy with its
variable bound to nothing. html spells a folded sum as `calc(…)`
(`htmlutil.UnitSumToCSS`). Two values are one unit when their tables come from
one declaration (`unitTable.Decl`): each environment builds its own tables,
and compared by pointer, `Add` across two returned its left operand
(`testdata/html_unroll_measurement.txtar`). A declaration copied for another
target is the same unit, compared by name and bases. And the interpreter
reads a literal's unit off its type (`Env.unitTableOf`) rather than its
suffix: a program declaring its own `unit length { px, … }` claimed `px` for
every body it ran, so a measurement sum met a `length` it could not be added
to (`testdata/unit_suffix_two_units.sngl`).

**Equality is per base on all four**, and the three compiled targets get there
differently: Go compares its `Measurement` struct field-wise and Kotlin its
`data class` component-wise, both without being asked, while JavaScript has no
such operator for an object. `===` there is reference identity, so `a == b` for
two equal measurements was false on every pair — `multiBaseUnitEqualJS` emits
the conjunction over the bases instead, with `!=` its negation rather than a
second walk. A single-base unit is a plain number in JS and keeps the bare
operator; routing one through the walk is what `testdata/unit_equality.txtar`
denies. `cmd/sngl/testdata/unit_equality_runs.txt` runs the comparisons under
bubbletea, the interpreter and a real Chromium, because a golden shows the
emitted text and only executing it shows the answer.

The case that separates two plausible implementations is a base that cancelled
to zero: the interpreter's map holds no key for it where the JS and Go records
hold a zero, so a comparison written over *the bases a value carries* rather
than over the unit's declared bases disagrees with the other three.

**A member a type does not have is an error on every kind** -- `o.value` off
an `option`, `xs.bogus` off a `list`, `s.huh` off a `string`
(`error_select_unknown_member.sngl`). Only an operand already typed invalid,
whose error was reported where it was made, selects silently. An element ref
naming a list or an option is refused a member read rather than reaching
whichever node a target found: index it, or test it against null first (see
`elementRef`).

Stdlib collection types support generic methods: `func list[T].filter(f func(T) bool) list[T]`, `func list[T].map[U any](f func(T) U) list[U]`, `func map[K, V].keys() list[K]`, etc. The receiver's type parameters are bound at the call site from the operand's concrete type (e.g. `xs : list[int]` binds `T=int`). Method-level type parameters (the `[U any]` after the method name) are inferred from the call's actual argument types — typically from a lambda's return type.

**A program may add a method to a library type, and may not redefine one it
has.** `func int.twice` is an addition; `func int.abs` collides with the
`sngl:builtin` declaration and is the duplicate two declarations in one
package are (`reportDuplicateMethod`,
`testdata/error_method_redefines_library.sngl`). It used to replace the
library's member, which made what `x.abs()` means depend on the program
that checked first once the library is shared between checks.

**What a program adds to a library declaration is held on the program**
(`ir.Overlay`, `internal/checker`'s `overrideMap` and `attachMethod`): an
override of a library component or function goes in `ir.Package.Overlay`, a
method on a library type in the program's root `ir.Scope.Added`, so a check
leaves the library as it found it -- `TestLibraryShared` holds every fixture to
that. The checker reads both through `overridesOf` and `ir.MethodOn`, an
import adopts what the imported package added (`adoptAdditions`), and every
phase after the check finds the overrides where a target package's are because
`ir.InstallOverlay` writes them there first: `ClonePackage(For)`, the two
`Specialize` entries, `optimize.Optimize`, `lower.Lower`, `build.Emit` and
`interp.BuildProgramEnv` each call it, on the build's copy (see *Every phase
after the check works on a copy*).
