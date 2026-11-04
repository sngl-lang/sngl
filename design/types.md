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

**A struct literal holds the fields the program wrote, and every reader asks
`ir.StructLit.Field`** (the written value, else the declared default, else
the type's zero at the literal's type arguments: `ir.FieldZero`) or walks
`EachField`, which hands over every field in declaration order as `Field`
answers it. Nothing reads a field's presence, so an omitted field and one
written at that value are one value everywhere. Before, JavaScript wrote only
the fields named, so an omitted one read undefined, and Go left an omitted
enum field `""`, which no member is
(`cmd/sngl/testdata/struct_omitted_fields_runs.txt`); and `ui.Style` read a
field at its type's zero as unset everywhere but html, which read presence
instead, so `{padding=16px, paddingTop=0px}` was 16 on top on four targets and
0 on the fifth (`style_side_zero_runs.txt`). A field whose absence means
something is an option, or an enum leading with a member saying so:
`ui.Style`'s lengths, colors and numbers are options and each of its enums
leads with `unset`. A host's own struct holds what its host gives a field
left out, so `Field` does not answer for one.

The fields left out are not written into the IR. The checker did that for a
while, and it was what the docs site's build cost: every Style literal became
52 fields, copied by every splice and unroll. Each backend fills where it
spells a literal instead (`irwalk`, the literal spellers): JavaScript writes
every field, Go leaves out a field holding Go's own zero for it (`atGoZero`,
nil for an option) and Kotlin one holding the default its data class declares
(`atDeclaredDefault`). A literal the fold spells from a value leaves out each
field at what it would hold left out (`StructLit.HoldsOmitted`), so a folded
Style is the fields it sets.

**A spread writes each field of its operand that is not at its type's
zero**, a declared default other than the zero included, and a field written
after the spread is the literal's own. With Style's unset fields at their
zero, `{display=flex, ...style}` lays a caller's Style over a component's
look. Only the running value knows which fields those are, so the
interpreter (`Env.mergeSpread`), the merge helpers `NoStructSpread` writes
(`__merge_<Struct>`, each field compared with its zero as `==` compares it)
and the folding of a literal operand (`spreadWrites`, by `ir.ZeroKnown`) all
ask the value. A field written after a spread the build cannot read goes
through a `__set_<Struct>_<field>` rather than the merge, which would skip
it at zero (`cmd/sngl/testdata/struct_spread_zero_runs.txt`). html withholds
`structSpread` under any language, since JavaScript's own `{...s}` writes a
null over what came before it. NoStructSpread runs after `LibFuncs`, so a
library helper's spread is lowered with the program's: before, the helper
joined the package after the spreads were lowered and kept its `{...c, a=1}`
(`internal/lower/lib_funcs_test.go`).

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

**`methodsOf[S]`'s functions are methods of S**
(`constraintFuncs`): each is a bodyless `Func` whose `Receiver` is the
type parameter it constrains and whose `this` is a `RecvParam`, held in
S's `StructDef.Methods`, so `ir.ResolveMethod` on a T finds it
(`ir.ConstraintMethod`) and the checker's method call, the formatting of
`"{v}"` and the LSP need nothing else. S is declared un-interned, since two
of the same functions constrain different parameters, and `MapChildren`
carries its methods over. A type is
measured against one by the call it would take (`methodUnsatisfied`, over
`ir.MethodParams`), not by signature equality, so `int.format`, which takes
`width` and `pad` too, satisfies `const func format(base int) string`. A
constraint's parameter may default to a constant, and the method has to
default to the same one (`ir.SameConstant` through any `const` naming it);
the method's defaults are checked with its body, which may come after the
call that binds it, so the comparison waits for the end of the check
(`matchDefaults`). A call
through one is the bound type's own method once the type is bound
(`ir.BoundCall`, beside `_format`'s), which the interpreter asks with the
running frame's bindings, the optimizer once a body is spliced or specialized
at its call's types, and lowering in copies: `passBuiltinMethods` first copies
every generic function that makes such a call at its own parameters, itself or
through a generic it calls (`ir.BoundReach`), at each binding
(`monomorphizeFuncs`), because a target keeping generic functions whole would
have no type to call. `ir.GenericCallee` binds a receiver its parameters do
not list for that, and `Monomorphize` binds through it too. A
`_formatArgs[T]` written for a T holds what the constraint's format takes, and
the bound type's may take more: `ir.FormatCall` leaves a field the literal
does not hold to the format's default, and the interpreter completes such a
literal at the running bindings (`ir.FormatArgsLit`)
(`testdata/generic_constraint_method.sngl`,
`error_generic_constraint_method.sngl`, `generic_constraint_method.txtar`,
`internal/lower/testdata/builtin_methods_constraint.txtar`).

**A receiver's list may narrow a parameter its declaration leaves `any`**
(`func list[T ordered].sort()`). The parser keeps the constraint on
`FuncDef.RecvTypeParams`; `inheritReceiverConstraints` skips a parameter with
one, so `resolveTypeParams` gives the method's T the narrower constraint, and
a body reads it as that. Narrowing a parameter the declaration already
constrains would ask for both, which no constraint spells, so it is an error
and the declaration's stands. A call measures the receiver's binding against
the narrowed constraint (`checkNarrowedReceiver`) where it binds the
receiver's parameters; the declaration's own were measured where the
receiver's type was written. `ordered` is a constraint the checker answers
(`ordered` in constraints.go): a number, a single-base unit, a string, or a
type parameter constrained to `ordered`; Go and Kotlin copy a generic body per
type, so `<` on a `T ordered` needs nothing of them
(`testdata/list_sort.sngl`, `error_list_sort.sngl`,
`cmd/sngl/testdata/list_sort_runs.txt`).

**An alias is an `ir.Alias`, not a declaration of what it names**
(`internal/checker/aliases.go`): `struct Name = Type` parses as an
`ast.StructDef` whose `Alias` holds the type -- `enum` and `unit` as their own
-- so its doc comment, export and position are a declaration's, but the
checker declares it apart from `Package.Structs`, which every later phase
walks. It is replaced by what it names wherever it is written: a type where a
type is (`aliasType`, from `resolveNamedType`), a constraint where a
constraint is (`namedConstraint`), and the declaration where its name stands
for one -- a literal, a member, a conversion, a method's receiver
(`aliasedDecl`, `literalAlias`). So nothing after the check meets one, and
no target emits one. What it names is resolved once, on demand, at the
package root through its own file's imports, whichever scope first names it,
so a use may come before the declaration; `aliasing` catches one naming
itself, and reports it once. Whether it names a constraint is read off the
name written (`namesConstraint`), since a constraint resolves on the
parameter it constrains and a type does not. A function `methodsOf` names is
called on that parameter, which an alias does not know, so a constraint's
alias resolves on a `Self` of its own and each use substitutes the parameter
for it and its arguments for its parameters (`ir.ExpandAlias`), into each
function's parameters, result and receiver as well
(`ir.SubstituteConstraint`), which `Type.Substitute` leaves to a struct's
declaration (`testdata/type_alias.sngl`, `constraint_alias.sngl`,
`error_alias.sngl`, `type_alias.txtar`, `constraint_alias.txtar`).

**A method is looked up on the declaration its receiver's name finds, past
any binding of the name that holds no method** (`ir.LookupMethodIn`). A
scalar's type names no declaration, so its methods are found by name, and a
node `#int` or a variable `string` bound between the program and sngl:builtin
answered first: `"{n}"` with `n` an int was "cannot format int"
(`testdata/method_lookup_past_binding.sngl`).

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

**A prop its default types is typed before any body is checked**
(`ensurePropDefaults`). Pass 2 checked a component's prop defaults with its
body, in declaration order, so a call site above the component saw the prop
untyped: `card(style={fontWeight=bold})` above `component card(style = ui.Style{})` gave the literal no type and read `bold` as a name of its own,
and an override restating the type was held to nothing. The defaults are
checked ahead, each in the scope its body check gives it, and an override's
restatement waits for them (`afterPropDefaults`)
(`testdata/prop_default_typed_call_site.sngl`,
`error_override_restates_default_type.sngl`).
