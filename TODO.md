# Strict typing: remove `TypeDyn` from the compiler

Branch `feat/strict-types`, draft MR !236. Delete this file when the MR is
done.

## Where it stands

`dyn` is gone from the language: `sngl:builtin` no longer declares it, a
program writing it names an unknown type, a var needs a type or an
initializer, a field read through an option needs a null test, and the
`go:`/`js:`/JSON importers make a host type SNGL cannot spell an unusable
declaration typed `ir.TypInvalid`. The fixture backstop
(`internal/fixtures/dyn.go`) fails any clean fixture whose IR carries a
`TypeDyn` on a declaration or an expression.

What is left is `ir.TypeDyn` inside the compiler, on things only the compiler
synthesizes. The rule for each: give it the type it actually has. A new kind
standing for "a host value" is `dyn` under another name and is not an answer.

## Steps, one commit each

1. **Namespaces are not values.** `math.pi` is a Select whose operand is a
   namespace Ident typed `dyn`, and `math.sqrt(x)` / `ui.vbox` carry one as
   `Call.Receiver`. Add `Qualifier *ir.Namespace` to `ir.Ident` and `ir.Call`:
   a member read through a namespace is `Ident{Sym: member, Qualifier: ns}`,
   a qualified call is `Call{Func, Qualifier: ns}` with no Receiver. The
   checker never builds an Ident for a namespace (`inferSelect`,
   `inferMethodCall`; `dynNoSymType` goes). Port the readers, most of which
   look the member up again from the namespace and so simplify:
   `internal/lower/lib_funcs.go` (two), `internal/optimize/consteval.go`
   (`nsConst`, `nsTarget`, the const test), `internal/interp/eval.go`
   (`namespaceMember`, `evalNamespaceCall`), and the Go, JS and Kotlin
   `evalNamespaceCall`/`snglNamespace`, which key on `Receiver != nil` today
   -- a condition that also covers non-namespace receivers, so split it.
   `ir.Convert` prints a qualified Ident or Call as `ns.name`. The lowering's
   fake `lower` namespace receiver on node ops (`lowerNSIdent`, 27 uses) goes
   too: `ir.Convert` prints a node op (`ir.IsNodeOpCall`) qualified by
   `lower.` itself.

2. **A widget handle is its component's type.** Lowering types a node's local
   by its component already (`varType` in `declarative.go`) but every
   reference to it -- `__nX` in `lower.AppendChild`, a prop target
   `__n3.text`, `parent` in a render-slot function, `__slot0`, `__entry`,
   `__root`, `__place*_prev` -- is `dyn`. Type each by the node it names
   (pass the node or its type rather than an id string where a reference is
   made); a render slot's `parent` takes the type of the container its one
   caller hands it, and the slot's list `list[<the node type its body creates>]`. Attaching a root node, `lower.AppendChild(__app, n)`, becomes
   an op of its own (`AttachRoot(n)`, replacing `ir.AppParent` /
   `IsAppParent`) so no application value is needed. Files:
   `declarative.go`, `reactivity.go`, `slot_placement.go`,
   `slot_instances.go`, `focus_order.go`, `effect.go`.

3. **A synthesized intrinsic call takes its operand's type.** `list.push`,
   `list.length`, `list.remove`, `list.indexOf`, `map.get`, `map.contains`
   built by `navigation.go`, `per_copy.go`, `slot_instances.go`,
   `slot_placement.go`, `list_lambda.go`, `reactivity.go` and `effect.go`
   declare a `list[dyn]`/`map[dyn, dyn]` receiver: use the operand's.
   `list_lambda.go`'s and `navigation.go`'s "unavailable" fallbacks become
   the type the caller has.

4. **i18n arguments stay a record.** `lower.RecordArgs` turns `{n=count}`
   into `map[string, dyn]`. Keep the typed record in the IR and have each
   language emitter (`i18n.tr`/`trInline`/`format` in
   `codegen/lang/{golang,javascript,kotlin}`) spell its runtime's map from
   the record's fields; `any` exists only in emitted host code. Keep the
   spread-once behaviour for a computed operand (passSpreadOnce).

5. **The rest of lowering and codegen.** `shape_draw.go`'s `canvasCtxParam`
   is the drawing context: a declared struct (html already has
   `DrawContext`). `codegen/modelref.go`'s Receiver is the Model: type it by
   the owner. Then each backend's remaining `TypeDyn` (html, fyne, gtk4,
   android canvas/intrinsic translators, `golang`/`kotlin` ircontext,
   `gir.girTypeToIR`) -- delete the arm where nothing produces dyn any more,
   or type the value it was standing for.

6. **Checker error recovery is `TypeInvalid`.** Every `TypDyn` in
   `internal/checker` is either recovery after a reported error or a
   "not inferred yet" placeholder (`typeFromInit`, `varSpecType`,
   `resolveType(nil)`). Recovery becomes `ir.TypInvalid` and keeps the
   poison behaviour dyn had (assignable both ways, skips operand checks), so
   no cascade is reported; a placeholder becomes nil or the inferred type.
   `internal/checker/dyn.go` (the fallback counter) goes with it.

7. **Delete `ir.TypeDyn`.** Remove the kind, `ir.TypDyn`, `BuiltinDyn`, the
   `IsAssignableTo` arm, the interpreter's dynamic fallbacks, and the
   stringer entry; the backstop in `internal/fixtures/dyn.go` then has
   nothing to find and can assert on `TypeInvalid` in a clean fixture
   instead. Sweep CLAUDE.md for `dyn` (it is mentioned throughout).

Each step: `go test ./...` green, `go tool verify --dry`, goldens refreshed
with `ANDROID_HOME=/home/claude/Android/Sdk go test . -run TestGolden -update`
where output moves (and `go test ./cmd/sngl -run TestScript/<name> -update`
for a recorded run whose digest moved).

## Also found this session, not yet fixed

- A list-literal prop handed to a component built at run time is emitted as
  `[]any` to a `[]string` constructor parameter on fyne and gtk4.
- A test cannot count the copies a loop rendered under one `#id`
  (`c.tick.length()` was never typed; `test_for_no_binding.sngl` now asserts
  by index).
- gtk4's GIR enum props are typed `string`; generating a SNGL enum per GIR
  enumeration would type them exactly.
- android's `ui.select()` by-name marker (#117) is the last
  `recordRendersByName` user.
