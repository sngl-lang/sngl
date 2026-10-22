# Navigation

`sngl:ui/nav` says a
window is a surface and a page a destination. A stack shows one page, named by
its `#id` -- `pages.go(about)`, `pages.go(pkg, P{name="x"})`, `pages.back()`,
`nav.link(to=about, text="About")` -- and `pages.current` is the page
showing, read through the stack's handle. A page not showing is not mounted. A
link shows `text`, as `ui.link` does, and takes no content: no Go target's
button renders any.

**The stack's page and a page's params are state of the navigation, never
props.** `current` is a computed of the stack (`#[intrinsic("nav.current")] func stack[M].current()`), read with its parentheses elided as any zero-arg
method selected through a handle is, so nothing sets or binds it. A page's
`params` is a one-way prop: where the page starts, and what a `go` or a
`link` naming no params shows it with (`params option[T] = null`). The params
it is showing reach its content's population, `component content(p)`, and
nothing else; `pkg.params` reads what the call site wrote. They travel with
the history entry, so `back` restores them.

**The family is `_page[M]`, unexported, and `page` is its only member.** It
exists so `pages.current` has a type: `pages.current.title`,
`pages.current.meta.label` and `pages.current == about` read it, and a program
names a page and never the family -- so a func cannot take one as a
parameter, and `pages.go(pages.current)` is refused, since `go` takes a
`page[T, M]`. `meta M` is what a program says about every page of a stack
beside its href and title, one type for all of them; `stack[M]` binds M from
the pages written in it (`bindFromChildren`, every page held to the first),
which is what will let a menu iterate the pages.

**The compiler answers the stack and the page; the link has a body.**
`stack`, `page` and `link` carry `#[marks.builtin("navStack")]`, `navPage` and
`navLink`, as `effect` carries its kind: a builtin node is exempt from the
bodyless-library rule and from inlining -- the optimizer's included --
`composeOverriddenBuiltins` composes one a platform overrides, and
`passNavigation` finds them by the kind. A link's body is what it is wherever
there is no address to follow: a `ui.button` whose click runs `@click` and
then the unexported intrinsic `nav._follow(to, params)`, going to the page in
the stack that holds it. The mark stays on the link because which body it
renders is decided per node: html overrides it with `ui.link(href=nav.href(to, params))`, and a link in a `<dialog>` keeps the declaration's own, which
`Component.DeclaredBody` holds once the override is swapped in. Three answers:

- **The interpreter** is the reference. It runs the checked IR, so
  `sngl:platform/none` overrides the stack and the page with its `Stack` and
  `Page` primitives, renders the link's own body, and `internal/interp/nav.go`
  answers them and `nav._follow`, keeping the
  page showing on the stack's instance and the params a page is showing on
  the page's. The primitives cannot name `_page`: `Stack[F]` binds the family
  from what it holds, and `Page` is a `ui.node`.
- **A target declaring `#[gen.can(navigation)]`** renders them in its own
  codegen -- html's documents and routes, and android's `NavHost`
  (`codegen/platform/android/nav.go`). On android a page's content is a
  reactive position for the inliner, since the target mounts and unmounts it
  as the stack moves, so a component with state in it is built at run time.
- **Every target gets `passNavigationValues`**, the value half
  (`internal/lower/navigation.go`): each page's record (`_page__value`, the
  family's props at the stack's meta type plus an `id` numbered across the
  family's pages in the package) is made once and hung on the node
  (`NodeInst.Record`, walked by `ir.Walk` and `ir.Rewrite`), a page handle
  read anywhere -- a `go`'s or a link's `to` included -- is that record,
  `pkg.params` is what the call site wrote, `==` against a page compares
  ids, `pages.current` is the one call `stack.current(pages)`, and every page
  type the program names is the record's. What is left is the three nodes
  and the `go`, `back` and `current` calls, naming the stack by its handle
  and the page by its record.
- **Every target without the capability then gets `passNavigation`**, the
  structure half, which lowers what is left to plain UI before
  ImplicitState: a var holding the current page as its record, a var per page whose params are not the empty struct,
  starting at what its call site wrote, a history list of entries holding the
  page replaced and every params var, and the pages as an `if`/`else if`
  chain on `current.id`, so a page not showing is not mounted. `go` pushes,
  writes the params and the current page; `back` pops and restores both, and
  does nothing at the bottom; a `nav.link` is pointed at a copy of its
  declaration whose body has `_follow` lowered to the same `go`, a copy per
  node since which page that is is the node's. The `current` call is the cell,
  rebased through `c` when a test reads `c.pages.current`. Three shapes are
  refused with a position (`cmd/sngl/testdata/nav_lowering_refused.txt`): a
  stack directly under a `for`, whose cells would be one for every copy, a
  `go` naming its page through a value rather than a page's own `#id`, and
  params handed as an option value. `testdata/nav_stack_lowered.txtar` is
  the code on gtk4, fyne and bubbletea, and
  `cmd/sngl/testdata/nav_stack_runs.txt` runs the program on each and on the
  interpreter.

**android is a NavHost.** A link is android's own `Link` primitive
(`lib/platform/android/nav.sngl`), since its body names a page and a
NavHost answers that only knowing which controller holds it. Each page is a destination with a typed route of
its own, a `@Serializable` class named for the page's record and id
(`_page__valueRoute2`), holding the params where the page has any with the
params its call site wrote as the default; the struct a params type reaches is
`@Serializable` too, and the params travel as their JSON through one
`snglNavType<T>()` per type, declared once at top level -- a NavType made
afresh each composition is a different graph each time, and Compose never got
idle. A route is a class even with nothing to hold, so a `go`, which names its
page by the record alone, is `pages__nav.navigate(Route(params = …))` whatever
the page. `back` pops, and does nothing at the bottom, where the system back
is the window's. `pages.current` is read off the back stack:
`currentBackStackEntryAsState()`, mapped to the family's record by one
`<Record>Of(entry)` per record type, and in test mode the state class holds the
controller so a test reads the same thing. A page's title, href, meta and
params are written in declarations rather than where the page is, so each is a
constant; a params type with no serializer (a datetime) and `--lang go`, whose
funcs live in golib with no NavController, are refused with a position
(`cmd/sngl/testdata/nav_android_refused.txt`). navigation-compose and
kotlinx-serialization-json join the scaffold, with the serialization plugin,
only for a program that renders a stack; the direct build adds the same
artifacts and plugin. `testdata/nav_stack_android.txtar` is the code, and
`nav_stack_runs.txt` runs the program under Robolectric.

**html is a document per page.** It declares `navigation` and asks for
`#[gen.wants(navigationHrefs)]`: a page is an address, so what is left of
navigation once a script is written is a browser following one.
`passNavigationHrefs` (`internal/lower/navigation_hrefs.go`), run after the
value half and before `composeOverriddenBuiltins` -- which is why
NavigationValues now runs ahead of it too: composition clears the kind they
find a page by -- writes the page's own params into a `nav.link` or a `go`
that passes none. html's override makes the link a `ui.link` to
`nav.href(to, params)`, which the optimizer folds to the page's href with each
`{name}` filled and escaped where the params are literals (`foldNavHref`), and
the html emitter answers where they are not. Only a link: a button that goes
to a page keeps its look and navigates when clicked, since `<a>` around a
`<button>` is not HTML a browser agrees on. It moves each page's content into its children under
`if pages.current.id == <id>` with the population's parameter on the node
(`NodeInst.Params`, the window's cell one level down). It refuses what an
address cannot carry: a params field the href names no placeholder for (so a
page's params all travel in its path), and a stack under an `if` that reads
state. A page whose href is a pattern on a static site is html's to refuse, as
it writes the page: whether a language serves routes is
`codegen.ServesRoutes`, which nothing in the lowering or the optimizer asks by
name.

html then overrides `nav.stack` and `nav.page` with its own `Stack` and
`Page` primitives (`#[intrinsic("html:stack")]`, `"html:page"`), and the
inliner carries the page's `Record` and `Params` onto the `Page` node with its
site (`ir.AttachNodeSite`). html's `documentsOf` writes one document per node
carrying a page record: the whole window with the stack standing for that page
alone, so the window and what sits beside the stack are the shell. In each
document `pages.current` folds to the page's record (`Fold.Answer`, which makes
every call of an intrinsic fold to a value -- the optimizer names no page), so
`title="Docs - {pages.current.title}"` is that page's title and the page's
`if` folds true; on a static site the params cell folds to the page's own, and
in route mode it is the document's `Params`, which the route binds from the
request on the server and `routeParamsJS` reads from the path on the client.
A static document is written at the page's href (`/about` is
about/index.html), and route mode serves a route per page named for it, with
`routesConflict` asking the pages. What no document's fold reaches -- a
function, a test body, the step an effect in a page settles by -- reads
`globalThis.__sngl_page`, which a document publishes only when its script
names it. A `go` is `location.assign` and `back` is `history.back()`: the
browser's history, so at its bottom it leaves the site. A window holds one
stack. Refused with a position:
`cmd/sngl/testdata/nav_html_refused.txt`. `testdata/nav_stack_html.txtar` and
`nav_stack_html_route.txtar` are the code, and `nav_stack_runs.txt` runs what
a document answers without leaving itself -- a test runs inside the document
it was loaded in, and a navigation ends it. So a test build writes no document
for a page at a pattern, which no test reaches, rather than refusing it as
`generate` does (`nav_stack_html_pattern_page_test_runs.txt`).

A document holds no other page's node, so it leaves out a handler's writes to
one (`dropOtherPagesWrites`): a select beside the stack writes every node that
shows what it changed, on whichever page, and the home document's write to the
item page's label threw before the handler reached the heading it does hold
(`testdata/nav_stack_html_other_pages_writes.txtar`).

**A stack's pages are fixed when it is built, and need not be written one by
one.** A `for` over a constant writes a page per element, an `if` over
constants one or none, and **pages compose in components as widgets and
shapes do**: a component written among a stack's pages is inlined to the page
primitives it renders, whatever it declares. Whether its `#id` is a value of
the page family is a separate question, `ir.IsFamilyValue`: a member
declaring the family's props is one, and one declaring none of them composes
members without being one (`Type.IsAssignableTo` refuses it, and
`bindFromChildren` skips it). The optimizer splices the second kind wherever it
is written (`inlineComponentCall`, whatever its props), which is what folds an
`if` over what the call site handed it -- md's `site` and its layout -- and
`spliceComposedPages` inlines whatever the optimizer did not before
passNavigationValues numbers the pages, since the inliner runs long after.
A member whose family is inferred from its body binds the family's type
parameters at their defaults, so a wrapper may declare `href` and `title` and
render the page from them (`_page`'s `meta` defaults to `M{}` for it to omit). A copy of a page under loops -- nested to any depth -- shares the page's id,
and its record holds which copy it is, `copy`, a `list[int]` of one index per
enclosing loop, outermost first (`ensureLoopIndex` makes each loop's index
explicit, `__copy`, `__copy1`, …), present on a family's record only where some
page of it is under a loop. `==` compares the ids and the copies, element by
element against whichever side's indices are known where it is written
(`copyMatch`) and through a synthesized `__nav_copy_eq` where neither is, since
Go's slices have no `==` and JavaScript's arrays compare by reference. The
page's `#id` names that copy inside its loops and is nothing outside them (the
checker's "rendered inside a for" diagnostic). A looped page may take params,
held in one cell for the page -- only one copy shows -- which starts at the
starting copy's params, read back by index into each loop's iterable
(`loopedStartParams`, a pure function the fold answers), and a `go` or link
naming a copy and passing none hands it its own (`handLoopedParams`). html
writes a document per copy: html's `documentsOf` enumerates the copies
(`documentCopies`), folds an `if` and walks each loop with its variables
bound, and clones the window holding only the statement the copy is in, so a
site of a thousand pages holds one page's tree at a time; a loop of pages is no
render slot (`loopsOverPages`, asked by passReactivity and
passSlotChildInstances) and the page primitive under one is a rendering root
for the inliner, as a window is. passNavigation keeps the loops: each copy is
the `if` on `id` and `copy`, and where a copy may be the start the stack's
first page is `<stack>__start()`, which looks for the page at "/" among the
copies and falls back to the first written -- folded at build time where the
iterables are constants. android declares a destination per page: a looped
page's route class holds the copy as a dotted string (`copy = "1.0"`, since
typed routes carry plain types) and its params as an optional the copy's own
fill in, and the destination and the record mapping bind each loop's
variables back from it by index. The start is `NodeInst.Start`, the search
passNavigationValues hands a target that declares stacks itself, which the
fold makes the record (and android runs at composition where it did not). A
composition nothing instantiates after its splice is dropped. Refused with a position
(`cmd/sngl/testdata/nav_pages_written_around_refused.txt`): a `for` over state
or an `if` reading it, and a composition of pages with state of its own.
`testdata/nav_stack_pages_written_around.sngl` and `nav_stack_pages_nested.sngl`
run it on the interpreter, which needed nothing new -- each copy is an
instance of its own -- and the two `.txtar`s are the code on html, gtk4, fyne,
bubbletea and android.

**A window lost `href` and `params`, and `output(entry=…)` went**: a destination is a page, and a stack starts at the page at "/", or
its first. Every program that was a window per page is a window holding a
stack of pages, the window's title the page's (`title=pages.current.title`),
and html writes the same documents from it. A `var` written in a page's body is state of the page, which starts again when the page is left, as any block's is (see *A `var` written in a block of a view is state of that block*).

Three limits that are not navigation's. On bubbletea a stateful component in a
page is spliced into the Model, so its state survives the page being left --
the divergence a window under an `if` has there. A page's handle read above
its `nav.page` in source has the type arguments its props bind -- its params
-- since the checker reads them quietly before the body around the node
(`preSpecializeHandles`, `cmd/sngl/testdata/nav_page_named_above_runs.txt`);
what the children bind, a stack's meta type, still waits for the node, so a
`go` above a stack whose pages carry a meta does not check
(`cannot pass page[struct {}, Meta] as page[struct {}, struct {}]`). And a node in a page's
content is rendered only while the page shows, and a component there is built
at run time on the targets that keep an instance's state, so a compiled test
reads neither (`c.label.value`, `c.tally.hits`), which is why `nav_stack_runs`
tests a narrower surface than `testdata/nav_stack.sngl`.

What it needed from the checker is general:

- **A component type keeps its type arguments** (`page[T]`, resolved like a
  struct's), and a node's `#id` is typed by what its call site bound
  (`specializeHandle`), so a func binding `T` from a handle holds the value
  beside it to that type. Written bare, a generic component type is its
  defaults, as a struct type is (`page` is `page[struct {}]`), and a
  parameter with no default has to be written. A select through one reads
  its props, and its computeds' results, under those arguments, and a handle
  reached from outside its body (`c.pkg`) carries what its call site bound
  (`handleArgs`). A qualified component name (`nav.page`) is a type too, not
  only a family.
- **A family may take type parameters**, and a member binds them in its
  return position: `page[T, M]` is a `_page[M]` (`Component.FamilyTypeArgs`,
  `ir.FamilyArgs`). A member declares each family prop at the family's type
  under that binding, a member's handle is assignable to the family at the
  arguments it binds, and a select through a family value reads its props at
  the value's. A generic slot's family arguments are bound by the children
  written in it where no prop pinned them (`bindFromChildren`, recorded per
  node in `nodeArgs`). `testdata/family_generic.sngl`.
- **A package names its own unexported declarations**: the export rule is the
  one between packages, and a bare type name resolving in this package (or in
  the library source being loaded) is not held to it.
- **`T{}` is the zero of what T is bound to**: typed `T` in the declaration,
  and written out by each call that omits it (`typeParamZeroFor`), for a func
  and for a component prop, the library's included, since no backend can
  spell a type parameter's zero. A default that *holds* one, `{v=T{}}` or
  `[T{}]`, is written the same way, at the types the call bound with each
  `T{}` inside it that type's zero; it used to evaluate to `{}`
  (`testdata/generic_default_zero.sngl`). A generic signature's defaults see
  its type parameters.
- **A two-way prop is never required**: unbound, it starts from its default
  or its type's zero.
- **A window's own props see its body's handles uncounted**, so
  `title="App - {pages.current.title}"` reads one node rather than an option.
- **`==` on two handles needs one declaration.**
- **A method whose receiver is not its first parameter** (every `sngl:`
  method, a component's) infers its type parameters from all of its
  arguments; the shift for the receiver-as-param form dropped `to`. And a
  component another package declares finds its methods in its own table
  when its bare name is not in scope (`pages.current` off `nav.stack`).

And from the interpreter, each a rule for every component:

- **An instance the mount no longer reaches loses its state**
  (`evictUnreached`), as every compiled target already does: a component under
  an `if` that went false starts again from its initializers.
- **A handle compares by instance**, the scope the interpreter keeps for it,
  through the test runner's wrapper as well (`InstanceValue`).
- **An event on a component whose call site handled none of it reaches what
  it renders** (`InvokeElementEvent`), the way a user's click reaches the
  widget: `c.link.click()` clicks what the link's override draws.
- **A bare call of an intrinsic the interpreter implements runs it**, as a
  qualified one always did, and so does an intrinsic computed selected
  through a handle (`intrinsicMethod`, and the test runner's `GetField`).

`TestRunFixtures` runs `SKIP(codegen)` fixtures too: the directive opts a
fixture out of code generation, and the interpreter is the reference such a
fixture is held to.
