# Primitives, natives, element refs and handles

**`#[intrinsic]` on a component is a platform primitive.** On a function the
mark names a signature in `ir.Intrinsics` that every language backend must
implement. On a component there is no signature to register — the declaration
*is* the contract for props and events, and one platform's codegen emits the
widget from it (android's `Screen`, `Link` and `SnglDateField` in
`lib/platform/android/android.sngl`). The id is that codegen's dispatch key, namespaced by
the emitting platform (`android:Screen`) so it can never collide with a stdlib
intrinsic or with another platform's. `lib/internal_intrinsics_test.go` holds
the two forms to opposite rules: a function id must be in the registry, a
component id must not be, and must carry its namespace.

An `#[intrinsic]` component may **not** have a body, which is the same claim
from the other side: the mark says where the render comes from, so a body
beside one is emitted by nobody and read by nobody -- `isPrimitiveComponent`
exempts the declaration from inlining precisely so the platform can render it
from the declaration itself. `{}` is refused with the rest, because it says the
component renders nothing, which is the one thing an intrinsic never does. The
rule is at registration, so gtk4's GIR-synthesized declarations are held to it
too.

The mark's other job is to stop the inliner. A platform's extension override
inlines into its caller (`passInlinePure`), and every platform-package
component must inline or the build fails — so the primitives those overrides
lower down to have to be exempt.
`isPrimitiveComponent` reads `Component.Primitive()` for that -- `Intrinsic`,
`Wildcard` (html's raw element) and a native mark -- alongside `Builtin` (a node kind) — the marks are the
whole list, and each says in its own vocabulary that the declaration is
rendered rather than composed away. A **tree kind** is deliberately not on it:
belonging to a segmented tree says which family a declaration joins, not that a
codegen renders it, so a shape composed out of other shapes is a wrapper like
any other. `isPlatformStdlibComponent` is a different question: whether a component came
from a `sngl:platform/` package the program imports.

A **native** mark — `#[go.native(path, name, flags)]`, `#[js.native(name, module, flags)]` — is the other half: the declaration *is* that host
identifier, so a call becomes a call to it and nothing is emitted for the
declaration itself. Two things about the name that are easy to get wrong:

- **It carries its own qualifier.** `#[go.native("strings", "strings.ToUpper")]`,
  not `("strings", "ToUpper")` — a Go package's name is not a function of its
  import path (`gopkg.in/yaml.v3` is package `yaml`), so the path cannot supply
  it. The path is what `RequireImport` adds.
- **Every language has one.** `go.native` and `js.native` were joined by
  `kotlin.native`, which android needs to describe Compose, and by
  **`#[cnative]` in `sngl:macro`** — C is an ABI rather than a target, so a
  platform built on a C library (gtk4 on cairo) names C identifiers and no
  language at all. The Go backend renders those as cgo and supplies the
  `C.double`/`C.int` conversions from the declared parameter types. Which
  header declares one is the program's `c.link` (`sngl:x/c`), and every Go target writes the links the program
  reaches (`ir.ReachedCLinks`) into its cgo preamble: fyne and bubbletea as a
  preamble of their own, gtk4 inside the one inline mode already has, which a
  `C.` call puts the build in (`golang.CPreambleLines`,
  `testdata/c_link.txtar`). A call of a program's `#[cnative]` function from a package
  no c.link reaches is a checker error at the call
  (`reportUnlinkedCNativeCalls`, `testdata/error_cnative_unlinked.sngl`):
  it used to reach the Go toolchain as `undefined: C`. A library's is exempt
  (`ir.Package.Library`, set for a `sngl:` package and what a library plugin
  writes), since gtk4's preamble includes the headers its cairo and `gir:`
  declarations name.
- **A component may be one, on Kotlin.** `#[kt.native("Card", "androidx.compose.material3.Card")]` on a bodyless component names a
  composable: android calls it with the props as arguments by name and the rest
  slot as the trailing lambda (`renderIntrinsic`), and adds the import unless a
  star import of its package already reaches it. android declares its plain
  composables this way rather than as `#[intrinsic]`, so a program describing a
  widget the platform never heard of writes what the platform writes
  (`testdata/android_native_composable.txtar`). It is `Component.IsNative`, a
  `Primitive` like an intrinsic, and the native rule below applies:
  `codegen.componentsFor` skips one. Any other platform refuses it in
  `passForeignPrimitive` unless its generator claims the scheme
  (`codegen.NativeWidgets`); a body or a call flag beside the mark is a checker
  error (`error_native_component.sngl`).
- **A native is never emitted.** Every call became a call to the host
  identifier, so a declaration would be read by nobody — and it is written as
  a stub over the zero value, which reads exactly like a real implementation.
  JavaScript got that rule first; Kotlin had the bug until a `deny` caught
  `fun hyp(a: Double, b: Double): Double = 0.0` beside a working call; Go was
  the third and is `testdata/native_decl_not_emitted.txtar`. The test is
  `Foreign.Name != "" && !Foreign.Marked` and it is applied **once**, in
  `CodegenCtx.AllFuncs` — not at the emitter, because fyne and bubbletea
  rebuild a component's func into a fresh `ir.Func` to give it a Model
  receiver and the copy carries no `Foreign`. Only a native with a *return
  type* ever showed: the checker synthesizes `return <zero>` for a bodyless
  func, and a void one got an empty block that every Go emitter's
  `len(fn.Block) == 0` guard already skipped. Nor is one overridden: no target
  emits its declaration, so `func ToUpper[bubbletea.platform]` over
  `go:strings` is refused where it is written rather than checked and never
  run.
- **`method` says the identifier is invoked *on* its first argument** rather
  than handed it: `c.Circle(1, 2)` where the default is
  `gfx.Context.Circle(c, 1, 2)`. Both are valid Go for the same method, and
  which one a host API wants is the API's to say — cairo takes its context
  first and wants the default. JavaScript has no receiver-first spelling at
  all, so describing a DOM API needs the flag: `ctx.arc(x, y, r)` is the only
  thing that runs. Only the last dotted segment is emitted, because the
  receiver supplies the package and type.
- **`named` (Kotlin) passes the arguments by the declaration's own parameter
  names**, which is what reaching a host parameter after one with a default
  requires: `drawCircle(color, radius, center, alpha, style, …)` cannot be
  reached past `alpha` positionally.
- **A native mark names a *type* as readily as a function**, which is what
  makes a host handle spellable: `#[go.native("time", "*time.Ticker")] struct Schedule { C go.chan[time.datetime] }` with `#[go.native("time", "time.Ticker.Stop", method)] func Schedule.stop()` beside it -- and a *field* of one is how a host API hands a channel back. gtk4 has done the same for C all along —
  `#[cnative("*C.cairo_t")] struct CairoContext {}` — so the form predates the
  need for it by a platform.

  **Reach for it before working around a signature.** fyne's and gtk4's timer
  runtimes each kept a process-wide mutex-guarded `map[int]…` and handed SNGL
  an integer index into it, because the override was written as
  `var handle = 0` and `int` looked like the only thing the declaration could
  spell. It was not: the schedule itself is a name, and the registries were
  bought for nothing. Nothing new had to be built to delete them.

  **A runtime package is a list of missing language features written in Go**,
  and `pkg/go/fynert` was two of them in turn: a registry while a handle was
  unspellable, then a goroutine and a `select` around a `time.Ticker`, because
  `Ticker.Stop` does not close `C` and a bare `for range` over it leaks. The
  registry went when the handle became spellable. The second was the language
  gap itself, and it is closed rather than stepped around: fyne's timer is a
  `*time.Ticker` consumed on a goroutine, and `fyne.sngl` holds all of it.

  **`go.chan[T]` is an intrinsic struct declared by `sngl:language/go`**
  (`#[marks.intrinsic("go:chan")] struct chan[T any] {}`, `StructDef.Intrinsic`),
  not a type kind: to the checker it is an ordinary generic struct, and only
  the Go emitter, which answers the id, gives it a host spelling. It is
  declared there rather than in `lib/` because only a language with channels
  can answer one. It exists
  because a host API hands channels *out*: `*time.Ticker`'s `C` is a field, and
  without a channel type there was nothing to declare it as. `IRTypeToGo`
  spells it `chan T`, `lazyIter` ranges it one variable at a time like the pull
  sequence it is, and its zero is nil in both places a zero is written --
  `ZeroValueGo`, whose `TypeHintToGo` would otherwise title-case it into
  `Chan bool`, and the struct-literal path, which resolves the declaration the
  way `structLitTypeName` does because a synthesized zero leaves `Def` nil.

  **Direction is not spelled**, and the declaration says why: a receive-only
  channel is reachable because nothing writes a Go type for an expression that
  is only selected on, while binding one to a var would emit the bidirectional
  `chan T` and not compile against it.

  **`go.select` takes a `list[Case]`**, written before SNGL had variadic
  parameters, and each arm is `go.recv(ch, func(v T) { … })`. It is
  the one thing here that cannot be an `IntrinsicEmitter`: that renders a single
  *expression*, and a select's arms are statement lists. So it is answered from
  `CallStmtLines`, the statement-level seam that already existed, and the arms
  are **inlined rather than called** -- an arm may `return`, and that has to
  leave the goroutine, which a closure wrapper would not do. A case variable is
  bound only where the body reads it, Go rejecting an unused one.

  `go.makechan` takes a witness value rather than a type argument --
  `makeChan(false)` is a `chan[bool]` -- because it was declared before a call
  site could write one, and the emitter reads the element type off the
  argument.
  `make` and `close` are Go *builtins*, carrying no import path, so a
  `#[go.native]` cannot name them at all and both are intrinsics for that
  reason.

  **A statement-level answer has to say so**, which is what
  `DeclareLangImplements` is: the language-axis counterpart of
  `DeclarePlatformImplements`, and it exists for the same reason stated there.
  `lib/internal_intrinsics_test.go` asks whether *some* target can emit each id,
  and three of these are answered outside the emitter registry, so without it
  the check reads them as ids a build would emit a call to nothing for. It names
  the ids rather than the package, the opposite of the platform side and
  deliberately: `sngl:language/go` holds ordinary emitter-answered intrinsics
  too.

  **A list literal is a fourth place a lambda hides.** `WalkLowered` reached a
  lambda that *was* an expression and one handed to a call, and not one inside a
  list -- which is what a select's arms are, two levels down. So the widget
  writes in a tick reached the emitter untranslated: a bare `__n0.Text =`, which
  is neither a field any Fyne widget has nor a name in scope. The same blind
  spot design/language.md records for the lowering passes, one layer out.

  **The hand-over it also held is `async.do`**, which a platform package may
  name: `#[intrinsic("async.do")] func do(f func())` dispatches through
  `LookupPlatformIntrinsic` exactly as a blocking call's posted tail does, and
  on fyne emits `__snglDo`, the model's helper calling `fyne.Do` -- or, in a
  test build, queueing the closure for `t.wait` to run on the test's own
  goroutine (`agentPosts`): fyne's test driver runs `fyne.Do` on the goroutine
  that calls it, so a request's answer rebuilt a list's widgets while the test
  clicked others, and fyne crashed laying them out
  (`examples/pokedex`'s tests, in `cmd/sngl/testdata/examples_runs.txt`). So a callback that must reach the drawing
  thread asks for that in one word instead of a package re-spelling it. The id
  is also the second thing `nativeCallbackFuncs` treats as scheduling, for the
  reason the `schedules` flag exists: a post runs its closure from the loop the
  platform owns, so a blocking call written inside one is on the drawing thread
  unless this pass takes the closure as an entry point. There is no *one*
  declaration to put the flag on -- the pass synthesizes calls to the id
  itself, and a platform package may declare its own -- so the pass names it.
  fyne, gtk4 and bubbletea answer the id; inside a literal bubbletea runs off
  its loop, the Go emitter spells the post itself (`GoIRContext.evalPost`),
  since the Model the post writes is the one the literal was made from.

  **Two shapes make Go's select behave unexpectedly, and both are written on the
  declaration.** A *closed* channel is always ready and yields the zero value
  forever -- which is the idiom here rather than the hazard, since closing
  `done` is how the goroutine is told to stop and its arm is taken on the very
  next pass. A *nil* channel is never ready, so its arm is never chosen, and nil
  is exactly what `chan[T]`'s zero value is.

  What such a type may *not* do is be constructed: a program holds one and
  calls methods on it. So the only literal of one that reaches a backend is the
  empty zero the checker synthesizes for an uninitialised `var t Ticker`, and
  where the host spelling is a pointer that zero is `nil` rather than a
  composite literal — `*time.Ticker{}` does not parse
  (`testdata/native_pointer_zero.txtar`).

  An **index** is still right where the host's own ABI is an index: a GLib
  callback carries an `int` user_data and cannot hold a Go pointer at all,
  which is what `pkg/go/cbind` is. The test is whether the host asked for it.

**`c.<id>` names the nodes one instance's own body wrote** (`elementRef`,
Jonathan's call). A node in a component the instance renders is that
component's ref and is reached through the id of the instance holding it —
`badge #inbox(…)`, then `c.inbox.lbl` — so two instances never share a ref and
a recursive component's `c.step` is this level's alone. Children written bare
are the writer's wherever the callee inserts them, which the rest-slot rule in
design/slots.md makes a single rendering. A slot population is a declaration with a scope
of its own, so its ids are reached through the node it populates, wrapped by
that slot's insertion count. How many nodes the read names is where the node is
written: `T`, `option[T]` behind a conditional (an `if`, a for-else, a slot
fallback, a boundary with a fallback), `list[T]` behind a `for` -- a loop
inside a loop a list of lists, and an `if` inside a `for` an entry per
iteration -- stamped on `ir.Select.Ref` (`ir.RefCount`, the wrappers outermost
first). The interpreter agrees through `interp.Node.Owner` — the instance
whose body wrote the node, nil in a population — and `View.Refs`. A compiled
test answers `c.x == null` with a presence check per id (`codegen.RefNullTest`,
`codegen.LowerRefNullTests`): html's `data-sngl-id`, fyne's and gtk4's
`__named_<id>`, bubbletea's conditions around the node, and android's
semantics tree. Each finds the node by its id alone, so in a test build
`passTestRefOwner` first renames a node another body writes under an id a test
reaches on the tested one (`b__own0`), before anything moves a node out of the
body that wrote it (`testdata/test_ref_owner.txtar`). Another body includes a
library override the build renders -- gtk4's datepicker writes a `#cal`
(`test_ref_owner_library.txtar`) -- and in a build of the whole program each
reached id is kept by the first tested component writing it and renamed in
the rest, a select off a renamed component's instance following
(`test_ref_owner_whole_program.txtar`). A diagnostic names a renamed handle by
`ir.Var.WrittenName`, the id the program wrote. A slot's reset
clears the field of a widget a test null-tests on fyne and gtk4, which still
held a widget the branch dropped. An
instance built at run time has no widget of its own: on fyne and gtk4 it hands
its root to `__present_<id>` where the root is bound, and on android its call
site holds a token in `SnglPresent` while it is composed
(`testdata/ref_null_test_instance.txtar`). `c.x[1] == null` null-tests one
copy, an iteration of the loops around the node with or without an `if`
between, by the copy's key as a trigger names one (`<id>PresentAt`, `codegen.PresenceAtMethod`), and `ir.IndexedRefIDs`
stamps the ids a test reaches either way
(`testdata/indexed_ref_null_test.txtar`). One copy of an instance built at run
time is handed its key in the `__copy` prop (`passCopyKeys`), and the call site
lists its root under that key where it binds it -- `ComponentRoot`'s second
argument, after the setter has re-keyed an instance a slot reuses: html's
`data-sngl-copy` beside `data-sngl-instance`, fyne's and gtk4's
`__copies_<id>`, one map for every record: a re-key replaces the entry and a
record's `Destroy` takes its root out, and android's `SnglPresent` entry
`id[key]`, each asked only for an id some such instance carries
(`codegen.KeyedInstanceIDs`); html places a static instance's root, built
once at a fixed position, with the same marks. bubbletea splices it and answers from the view (`testdata/indexed_ref_null_test_instance.txtar`).

**An instance is present wherever its call site is rendered**, whatever
its body renders now -- `name.empty` is the separate question of what it
renders. So a test's null test of a program component's instance is the
conditions of the `if`s around its call site and, for one copy, whether each
loop around it renders that copy (`answerInstancePresence`, in passTestReach,
read through the instance that wrote the call site, whose own presence is
asked first), on every target alike
(`cmd/sngl/testdata/test_reach_review_runs.txt`'s present.sngl). Where
something else decides the call site -- a boundary, a slot fallback, a
builtin node such as a page, a container inserting its children other than
once -- or the conditions read what a test cannot name -- a context, a loop's
variable, a prop bound to one -- the target's presence check answers instead,
as follows; one for a node the build folded away answers false
(`codegen.RendersNowhere`). A node reached through an instance whose body the
build folded it out of is absent in passTestReach itself: its null test is
the constant, a branch on one is decided -- through a local holding it too,
which stands for the ref -- and any other reach of it is refused where it is
written. A test firing an event its call site handles
none of is refused where it fires it.

**A spliced instance a test null-tests is found by its roots.** A splice
leaves no node of the instance's own, only the roots of its body -- the nodes
it renders first, through an `if`, a `for` or a boundary there -- which carry
the instance's id in `NodeInst.Instances` (`ir.InstancesOf`). bubbletea's
view answers from those. On every other compiled target `passCopyKeys` stamps
each root node `ir.InstanceRootProp` (`__instance_<id>`), holding the key of
the copy the instance stands in -- the loops around where it was written,
not a `for` of its own body -- or "" for one rendered once: html writes it as
`data-sngl-root-<id>`; fyne and gtk4 list the root in the instance's
`__copies_<id>` or `__present_<id>`, take it out where a render removes it
and where a record that listed it is destroyed (`__rooted`,
`__unlistInstance`); android holds a `SnglPresent` token while the root is
composed. The two splices that move the call site's `#id` onto the body's
first node leave a program component to passNoInlineComponents where that
node is one an `if`, a `for` or a boundary's fallback decides
(`ir.NodeIDRendersWithBody`). A null test of a node nothing decides is one
too, answered present (`testdata/indexed_ref_null_test_spliced.txtar`).

**An event's parameters are its declaration's, and an override reads what the
host reports and writes the prop it reports.** A platform override handles
its host widget's event, writes the two-way prop the control holds from what
the host reported, and emits the `sngl:ui` event with the same value:

```sngl
html.input(type="checkbox", checked=checked, @change(e) {
    checked = e.currentTarget.checked
    change(checked)
})
```

The write is what `passPropBindings` already makes of a body writing its own
two-way prop -- the `@checked` report a `:checked` binding's handler answers --
so nothing guesses which event reports a prop or which of its values does.
There used to be such a guess (`injectNativeEmit`): candidate event names by
the prop's type, a payload field matched by type, and a toggle where none
matched. Each target reads its host its own way, in its own `.sngl`:

- **html**'s element declares the DOM's event as an external struct,
  `#[js.native("Event")] struct Event { currentTarget Target }`, so a handler
  on an element is handed the DOM event and reads `e.currentTarget.value`.
- **fyne**'s primitives declare an event vocabulary by type, as their props
  are -- `@change(text string)`, `@toggle(flag bool)`, `@adjust(number int)`,
  `@pick(date option[time.date])` -- and a `Handler` names the Go callback
  whose `signature` spells the same parameters in Go. The promoted handler
  takes the SNGL handler's own parameters (`namedAsHandler`), the declaration
  and not a copy, so its body reads them by symbol; a Go lambda scopes its
  parameters as locals (`evalLambda`), or one named like a component's var
  read the var.
- **gtk4**'s GIR signals hand their callback the instance they fired on, which
  is what every generated event now declares, `@toggled(self GtkCheckButton)`; a component may name itself in its own signature for this.
  The override reads that widget with a getter `gtk4.sngl` declares --
  `checkActive`, `switchActive`, `entryText`, `calendarDate`, platform
  intrinsics answered where a handler body is translated (`readWidgets`),
  since which getter they become depends on the file's mode. The trampoline
  passes nothing, so a signal handler's `self` is the node it is connected
  on (`selfIsNode`); a widget a handler lifted out of a component built at
  run time hands on arrives as a value and is asserted to its field's Go type.
- **android**'s Compose callbacks pass the value: `func(on) { checked = on; change(on) }`; one passing nothing (radio's `onClick`) names the loop's
  option.
- **bubbletea**'s key press flips the state itself: `checked = !checked; change(checked)`.
- **none** renders a `Control[T]` primitive (`none:Control`) the trigger
  reaches, whose handler writes the prop; the snapshot prints the `sngl:ui`
  component in its place.

**A write the program makes fires none of a control's events, and each
override holds to it** (`cmd/sngl/testdata/program_write_fires_no_event_runs.txt`,
on every target). A handler reports only a value the program does not already
hold -- `if checkActive(b) != checked { … }`, fyne's `if v != value { … }` --
so a bound value written to the widget, whose host fires its change signal for
the write, reports nothing; a commit (an entry's `activate`, fyne's submit) is
not guarded, since pressing Enter commits whatever the value. A host whose
write fires its signal more than once is written quietly instead: gtk4's entry
overrides do not bind `text` but push it from
`effect(on=value, @mount(v) { if entryText(entry) != v { setEntryText(entry, v) } })`,
`gtk4.setEntryText` (a `widgetWrites` intrinsic beside the `widgetReads`
getters) blocking "changed", because gtk_editable_set_text deletes and
reinserts and a runtime instance's handler writing the text back re-entered
itself until the stack overflowed (`codegen/platform/gtk4/entry_echo_test.go`).
`sngl_calendar_select` blocks "day-selected" the same way; a test's invoker
fires the signal itself after selecting.

A handler attached in a statement list `readWidgets` reads names its widget
through its parameter, which `signalLambda` maps to the node once the node is
in scope, so `readWidgets` leaves those reads alone; read early, the parameter
was taken for a widget handed in as a value (`e.(gtk4rt.Handle)`).
`spellLocalNodes` then respells a node the slot declared as a local, which the
Go renderer otherwise writes as the receiver's field (`c.__n1`).

A test's trigger reaches the platform's input path: html writes the
arguments, by their declared names, onto the element and the dispatched event;
gtk4 sets the widget's state (which fires the signal) or its text; fyne calls
the callback; bubbletea and android press the key and click the control,
which flips it whatever the test passed. The interpreter enters at the node
the override renders whose handler emits the event (`forwardingDescendant`),
and a write of an instance's bound two-way prop writes the var its call site
bound (`Env.writeBound`). `testdata/toggle_change_payload.txtar`,
`ui_event_params.txtar` and `canvas_click.txtar` are the code,
`cmd/sngl/testdata/toggle_change_payload_runs.txt` and
`ui_event_params_runs.txt` the answer.

**A select is one control on every target**, so its `#id` names the widget a
test's `c.pick.change("Banana")` chooses through, as a user does: html's
`<select>`, fyne's Select, gtk4's GtkDropDown, bubbletea's `tui.Select`,
android's `SnglSelect` and the interpreter's own
(`cmd/sngl/testdata/select_change_runs.txt`). A select laid out as one widget
per choice leaves the id on the box around them, which no trigger reaches,
and a menu the trigger cannot type into is no input path a test has. Each
holds the bound value even before a choice carries it, since choices may
be state that arrives later, and neither offering choices nor writing the
value is reported as a choice:

- **gtk4** offers a GtkStringList through `offerChoices`, a platform
  intrinsic calling the template's `snglDropDownOffer` (gtk4rt in wrapped
  mode, cgo otherwise), whose C blocks `notify::selected` meanwhile; the
  handler reads `dropDownLabel`. The invoker selects the label, which
  notifies, and fires nothing after it. Each splice of the override has its
  own `#drop` (`freshNodeHandles`, as datepicker's `#cal`): the declaration's
  one handle, shared, made two selects in a component one handle rendered
  twice, which the build refused. A select a render slot builds reads its
  dropdown through the signal's parameter, which `readWidgets` leaves for
  `signalLambda` as above; spelled early, it read an unbound `d`
  (`cmd/sngl/testdata/controls_several_runs.txt`).
- **bubbletea**'s `tui.Select` is handed the choices on each view and update
  (`View(options)`, `Update(msg, options)`), its value is the truth and the
  cursor follows it, and a choice is the cursor moving or Enter on the row it
  is on, so `@change` fires when an update changed the value
  (`choosesOnMove`).
- **android**'s `SnglSelect` is a field with a menu beside it, Kotlin the
  platform ships as it does `SnglDateField`; typing a choice's label into the
  field chooses it, which is how the trigger's `performTextReplacement`
  reaches it.

**A tab is chosen by pressing it.** No node of a `ui.tabs` reports `@change`:
each tab of the strip a loop renders emits `change(k)` from its click, so the
`#id` a test fires through lands on the box around them and named nothing
that answers -- the interpreter said so, html chose nothing, and the Go
targets called an invoker nobody wrote. A trigger of an event the node the
id lands on does not report, where the body emits it from a looped node's
handler or callback handing on the loop's index (`ir.EmittedIndex`), is a
press of that node's copy at the index the trigger hands: `c.strip.change(1)`
clicks the tab at 1, which writes `selected` and fires `@change` as a user's
click does. `lower.NamePresses` gives the node the id `<id>__<event>` and
turns the trigger into `c.strip__change[1].click()`, the invoker each target
already has for one copy of a node loops render; both inliners call it,
before the emits are substituted, since the optimizer splices a body no
target overrides. Only a node under exactly one loop of the body is pressed,
since one index names one copy, and the optimizer rewrites a trigger only
once it keeps the splice. A trigger an earlier splice rewrote names the node
too, so two components each holding a `#strip` are each pressed through
their own. The interpreter finds the copy by evaluating the index in each
node's scope (`interp.View.Choosing`), among the nodes the component's own
body wrote and before any node that reports the event: a select in a panel
is the panel's writer's, and the press chooses the tab all the same. Android's `Tab` takes a
`modifier` so the copy carries its testTag. `cmd/sngl/testdata/tabs_runs.txt`
runs it on all six, through an instance as well.

**A record's content is counted by where it was written.** A data family's
content becomes a population of its container's hidden content slot,
branching on the record's number (`ir.RecordID`), and once the container is
spliced that branch sits in the container's loop over its records -- a tab's
panel in the strip's `for var k, t = items`. One iteration renders it, so a
`for` in the panel makes the only copies a test indexes, as the checker types
`c.cell` and the interpreter mounts it. `ir.RecordBranchOf` names the loop a
`t.__id == N` branch picks one record of, and `lower.blockLoops` leaves it out
of a copy's key (`passCopyKeys`) and of the loops a copy's prop read binds
(`nodeUnderLoops`); the interpreter found no loop at all, since `refLoops` did
not look in slot populations, and walks `ir.ViewBlocks` now. A panel with
state of its own is an instance whose render slot builds the copies: fyne and
gtk4 declare the Model's map of them once that slot's method is walked. And
`sngl test` lowers each group with that group's tests alone, as it generates
it: a key stamped for another group's test named a map this group never
declared. `cmd/sngl/testdata/tabs_runs.txt` runs it on all six.

**A `#id` on a visual node declares a handle, and `ir.Var.NodeHandle` is what
says so.** Every target stores one wherever it keeps the tree — a field of the
Model on the Go targets — rather than as a local, so a read of it has to be
qualified. There are two ways a reference can be recognised as one and only one
of them is a fact about the *declaration*: `ir.Ident.IsElementRef` is set on the
`__nN` references a lowering pass synthesizes, while a read of a program's own
`#id` resolves to the var the checker bound and carries nothing. Neither kind
lands in `Component.Vars`, so `ExprCtx.Resolve` answers for neither.

Tested, never name-matched, for the reason `ir.Param.Receiver` gives: a node id
is the author's word and `__`-prefixed names are not reserved. It is asked in
the Go language context rather than in a platform emitter because the question
— where does this handle live — has one answer for fyne, gtk4 and bubbletea;
each platform's own intrinsic path already wrote `m.<id>` for the references it
emits, which is why only a native call's *receiver* went bare and why it read
as a gtk4 bug. `testdata/node_handle_native_method.txtar` is the fixture:
`gtk_progress_bar_pulse` sets nothing, so GIR describes no property for it; it
is one of the *actions* `declgen` generates from GIR -- a method taking nothing
beyond its instance and returning nothing -- as a `#[cnative]` method reached
through the handle.

**A node's one-way prop may not be written**, which `refuseNodePropAssign`
reports on both the assignment and the toggle paths, through the node's `#id`
and through a parameter a handle was passed to alike. A one-way prop is
declarative: `ui.text(value=greeting)` says what the node shows for as long as
it is rendered, and reactivity re-evaluates it when `greeting` changes. A write
beside that is a second source of truth the next render undoes, so the program
that looks like it worked is the one whose write is silently gone — change the
state the prop reads instead.

**A two-way prop may be** (Jonathan's call): it is already a cell the host
writes -- a checkbox reports its clicks through it -- so a write from the
program is one more writer of that cell and lands where the host's report does.
`passHandleParams` makes `bound.checked = false` under `:checked=shown` a write
of `shown`, and inlines a call handing a handle to a function that writes one
through the parameter (`details.open()`); an unbound one is left to
`passImplicitState`, which names the instance's cell. The interpreter writes
through the handle the same way (`writeThroughHandle`, and a toggle through
one). `testdata/node_prop_write_two_way.sngl` runs it, `.txtar` is the code on
all six targets, and `error_node_prop_assign.sngl` is the one-way half.

What a *lowering* writes is untouched, and is how a prop reaches a host at all:
`passReactivity` and `passDeclarative` emit `__n0.value = expr` by the hundred.
The two are told apart by `ir.Var.Synthesized` — and, where lowered IR is
printed and checked again, by `checker.Config.Lowered`, since `text #__n0(…)`
re-parses as an ordinary node with an ordinary id and nothing in the text says
which side of the pipeline wrote it. Only a caller that lowered the IR itself
may set that flag.

**So a handle's remaining use is reading**, and a *prop* read does not compile
on the Go mutation platforms: `GoIRContext.Select` ends at
`operand + "." + ExportName(field)`, inventing a Go field by title-casing the
SNGL prop, so `box.value` is `m.box.Value` against a `widget.Entry` that spells
it `Text` — and against a gtk4 handle that is an `unsafe.Pointer` with no
fields at all. On html it reaches the emitter and renders an empty element
nothing fills. The one read that does work is a `#[cnative]` method's receiver,
above. Closing the rest needs a language↔platform read hook that does not
exist, and on gtk4 a getter is a call (`gtk4rt.EntryGetText`) whose name GIR
would have to supply per property, not a field. Until then a read is answered
at build time wherever the tree says the node is gone — see
`#[gen.renders(identity)]` under `sngl:x/gen`.

**Two nodes may share an id, but a handle that is *read* may not be rendered
twice.** The two halves are asked differently and deliberately so.
`uniqueNodeIDs` renames the later copies *by name*, because what that repairs is
the emitted namespace, where any two `#bar`s collide however unrelated. The
refusal is *by symbol*: `declareNodeIDs` runs per body, so two components each
writing `#bar` declare two vars and each read says which it meant, while two
spliced copies of one body share theirs and neither read can — all of them
resolve to the first copy's field, and the second widget is created and never
touched. `ir.NodeInst.Handle` is the link that makes the symbol reachable from
the node, since `ID` is only a name. Keyed by name instead, a `quiet()` that
reads nothing and renders one was refused for a `#bar` that a *different*
component read; `cmd/sngl/testdata/node_handle_read_duplicated.txt` holds both
halves apart.

**An `#id` is a declaration, hoisted to its component's scope.** It shadows
what an enclosing scope binds and is a redeclaration beside a var, a prop or a
method of that scope (`declareNodeID`), however deep in the body the node is;
inside an `if` or a `for` it is re-bound as the plain handle
(`declareOwnNodeIDs`), giving way there to the head's variables and the block's
own declarations, which are closer. It used to yield to any existing name
instead, and quietly: `card #card` beside `component card` bound nothing, the
node carried no `Handle`, the inliner recorded no splice of it, and a test's
`c.card.hits` went unrepointed on every compiled target
(`cmd/sngl/testdata/test_reach_id_named_like_component_runs.txt`,
`testdata/node_id_shadows_outer.sngl`, `error_node_id_redeclared.sngl`). A node
position looks a component up past the id (`Scope.LookupComponent`), which is
what `componentNamed` asks too.

**`#[foreign]` records what a declaration corresponds to outside SNGL.** It
lives in `sngl:macro` for the same reason `shape` lives in `sngl:ui/draw`, and
because its users are outside the compiler: a language plugin generating marked
SNGL to describe a foreign API, a platform package naming its host types.
`#[foreign("go:example.com/api", "api.Entry")]` gives the import path and the
name there; a struct field writes the name alone, `#[foreign(name="Title")]`,
having no package of its own. A mark's arguments bind as a call's do --
positionally or by name, constants only, a trailing flag list being a rest or
`flags=[…]` (`markArgsFor`). A function may add flags — `pure` and `async` — that state what a call
costs, because a foreign function's SNGL body describes it rather than
implementing it and nothing may be inferred from it. `pure` is the sharp edge:
it lets the compiler evaluate a call at build time, so a wrongly marked
function runs during a build.

**A pure function's value depends on its arguments and its source and on
nothing it reads while it runs.** Its result is stored between builds, keyed
by the call and validated against the Go closure it was built from, and
nothing records a file opened or a variable read at run time -- so such a
read is replayed stale after the thing it read changes, which for a folded
const is a wrong build. Hand the data in as an argument instead:
`file:`'s `names(pattern)` lists a directory at build time, which is how
website.sngl gives `docs.LibraryComponents` the snapshots it used to `os.Stat`.
Reading the compiler's own generated source is the one exception, and only
because the store reports it. A SNGL `const func` reading through
`sngl:x/gen` is the other, and for the same reason: every such read records
what it read (see *Trust*).

The mark imports nothing, resolves nothing and validates nothing, and never
confers type identity — only a scheme importer's `Foreign.Origin` unifies two
declarations. A marked declaration is still the program's own, which is what
`Foreign.Marked` says: a backend emits it, so the mark's `Name` is a name to
spell alongside that declaration and never a reference redirecting to one the
backend did not emit. Struct, struct field and function are the forms that
carry it; the others refuse it.

Not every compiler primitive is a package. The node operations a visual tree
lowers to (CreateNode, AppendChild, …) are `ir.NodeOps` constants: no program
can name them, no language registers an emitter for them, and their only
consumer is `codegen.WalkLowered` dispatching to a platform's
`IntrinsicTranslator`. A declaration would describe nobody's contract.
