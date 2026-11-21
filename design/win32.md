# win32

How `sngl:platform/win32` gets its Win32 declarations, wraps them for Go and
lays its controls out. What the platform is and how it builds, runs, tests and draws is its
bullet in `design/architecture.md`'s *Platform Details*; placement, the
canvas's protocol and test triggers are `design/nodes.md`; rich text is
`design/markup.md`.

## The declarations: `internal/cmd/win32gen`

**`sngl:platform/win32/api` is generated once and committed.** Win32 has no
introspection format a compiler can read at build time the way GTK has GIR,
and the classic API does not change, so `go tool win32gen` runs by hand and
writes `lib/platform/win32/api/api.sngl` with a `DO NOT EDIT` header. It never
runs in `verify`: it needs the network and both MinGW compilers.
`internal/cmd/win32gen`'s `TestCommittedIsFormatted` holds the committed file
to what the tool writes, short of running the tool: parsed, and formatted as
`sngl fmt` writes it. `TestLibraryHoldsNoInvalidType` checks it as it checks
every library package.

**What is declared comes from the metadata, and how C spells it from MinGW.**
The declarations come from [win32json](https://github.com/marlersoft/win32json),
a JSON rendering of Microsoft's
[win32metadata](https://github.com/microsoft/win32metadata). That metadata is
the source windows-rs and CsWin32 are generated from. It records each function,
struct and constant, and whether each pointer parameter is in, out or
optional. But it writes a parameter's type as the metadata's own (`UInt32`),
where C writes `UINT` or `DWORD`, and cgo refuses one for the other. So the
tool preprocesses `windows.h` and `commctrl.h` with
`x86_64-w64-mingw32-gcc -E` and `i686-w64-mingw32-gcc -E`, and takes each
prototype, typedef and struct from the headers. The metadata decides what
exists; the headers decide its spelling. The metadata has no widget model -- a control
is a class name (`"BUTTON"`), style bits and messages (`BM_SETCHECK`) -- so
the platform's widgets are written by hand over the generated declarations.

**Both architectures, amd64 the default.** The platform node's `arch` option
takes `amd64` or `386`, and the tests, CI's included, build and run both
(`testdata/win32_c_386.txtar`, `c_win32_test_runs.txt`).

**The pin.** `commit` in `main.go` is the win32json commit the file was
written from, and its first twelve characters are in the generated header.
`-json <dir>` reads `api/*.json` from a checkout rather than fetching them.
Moving the pin is a deliberate act: rerun the tool, and review the diff of
`api.sngl` like any other change to the library.

**It writes what the platform asks for, never all of user32.** Its wanted
lists are Go slices in the tool:

- `partitions`: the win32json files to read.
- `functions`: C names.
- `structs`: structs wanted beyond those a function reaches.
- `constants`: name prefixes (`WS_`, `WM_`, `BM_`, …), whole names where the
  entry has no trailing `_`.

Everything a wanted function reaches is written too. Each declaration is
written in a form C interop crosses (`design/nodes.md`, `#[c.native]`):

- A handle (a pointer to a `struct HWND__` or a `NativeTypedef` over
  `void *`) is an opaque `#[c.native("HWND")] struct HWND {}`.
- A scalar typedef is `struct UINT = int` under its C name.
- A struct is written field for field, if every field crosses.
- A constant is a `const`, if its value fits an `int`.
- A function is `#[c.native("SetWindowTextW")] func SetWindowText(…)`.
  It is always the `W` entry point, named without the `W` where the metadata
  also has an `A`.

A non-optional out pointer to a written struct is a `ref[RECT]`. An optional
string is an `option[c.utf16]`. An optional handle is written as a plain
handle: only NULL is lost, and a SNGL handle cannot be NULL anyway. A function
returning `BOOL` returns its alias (`WINBOOL`), an `int` the program tests
against 0, because C's BOOL is not a `bool`.

**What it cannot write it names, and the runtime calls instead.** A wanted
function is skipped, with its reason in a comment block at the end of the
file, in these cases:

- it is missing from either architecture's headers, as `GetWindowLongPtrW`
  is on 386, where it is a macro;
- it is spelled differently on the two architectures;
- MinGW and the metadata disagree on its parameters;
- a parameter or its return does not cross (`InvalidateRect`'s
  `const RECT *`).

Each such call stays in `lib/platform/win32/rt`, where C calls it directly.
That is also where everything goes that interop does not reach at all:

- arrays and unions;
- WIC and the common controls' notification structs.

The SNGL in `win32.sngl` calls `api.sngl` for what a single Win32 call does
(`SetWindowText`, `ShowWindow`, `SendMessage` with a generated `BM_SETCHECK`)
and the runtime for the rest.

## The runtime in SNGL

**The runtime moves into the platform's SNGL as gtk4's did**, one area at a
time, each pinned by a run first (`cmd/sngl/testdata/c_win32_*_runs.txt`,
whose snapshots C, Go through the wrappers, Go called directly and C for
386 must draw unchanged). What a single Win32 call does is api's
(`SetWindowText`, `SendMessage` with `BM_SETCHECK`, `EnableWindow`); a call
win32gen skips the platform declares itself (`InvalidateRect`, whose
rectangle is a `const RECT *`, an option of a handle at that type for
NULL); and GDI+, which win32gen reads none of, is `gdiplus.sngl`. GDI+'s
objects are `void` to C (`gdiplusgpstubs.h`), so a handle to one is
declared `void *`: spelled `GpPath *`, the wrappers held a type cgo would not
pass where it takes an `unsafe.Pointer`.

**A canvas's drawing context is the platform's, over GDI+.** `Gfx` is the
runtime's `sngl_win32_gfx`, which holds GDI+'s graphics, the path being
built and the paint's serial, read through `field` declarations; the rest of
what cairo's context keeps -- the current point, the color, the line, the
font, what each save kept -- is package state of the platform's (`_x`,
`_pen`, `_saved`, `gdiplus.sngl`), made afresh where the serial differs from
the last paint's, since the runtime begins each paint (`gfx_begin`) and no
paint is begun inside another. C kept the current point as a `REAL`; SNGL
keeps a float, rounded to `REAL` at each call, where C had rounded it, so
the pixels are C's (`c_win32_paint_runs.txt`). Only `Gfx.image`, which reads
the canvas's decoded pictures off the runtime's record, is still C.

**What holds the rest in C.** Each is a gap in the language, not a choice:

- **A Win32 callback is `__stdcall` on 386**, which a `#[c.static]`
  callback says with `convention="CALLBACK"` (`design/nodes.md`): C writes
  it into the function it hands Win32 and hands it at the typedef with no
  cast, and Go hands a C function of that convention calling its export.
  Before it, the C target's cast through `void (*)(void)` silenced what
  MinGW reports (`initialization of 'WNDENUMPROC' ... from incompatible pointer type`) and a Go export was cdecl; Wine restores the stack around
  such a call, so a run passed, and Windows promises nothing.
- **A window's record stays the runtime's** (`ctl`). `c.held` needs a
  `#[c.release]` parameter ("is a copy C holds until it calls the function
  a #[c.release] parameter is handed"), and none of Win32's per-window slots
  -- `GWLP_USERDATA`, a property, a subclass's reference data -- calls
  anything as the window goes; the record would be let go on
  `WM_NCDESTROY`, in a window procedure. `SetWindowLongPtrW` is a macro on
  386, which cgo cannot call. Everything reading the record -- layout,
  painting, events, the tree -- stays C with it.
- **A pointer in a message's WPARAM or LPARAM** crosses where the
  declaration marks the parameter `#[c.type("LPARAM")]`
  (`design/nodes.md`): C casts it, and Go calls a C function taking the
  pointer and casting it, since `C.LPARAM(p)` converts no pointer and an
  integer would keep nothing alive for the call; a cast takes a
  WM_NOTIFY's LPARAM back to its NMHDR. It holds only for a message sent,
  never one posted.
- **What C writes as UTF-16.** A C function answers no `utf16`, and a
  wide buffer C fills (`GetWindowTextW`, `CB_GETLBTEXT`, `LVM_GETITEMTEXTW`)
  has no type, so the runtime answers UTF-8 (`sngl_win32_get_text`). A C
  struct's field may be a string or a handle (`LVITEMW`'s text, `NMHDR`'s
  window) but no function pointer (`WNDCLASSEXW`'s window procedure: a
  class is registered by a C function taking the procedure as a
  `#[c.static]` parameter) and no array (`BITMAPINFO`'s colors).
- **The language runtime's ABI.** `sngl_post`, `sngl_loop_wait`, http's
  `sngl_http_start` (`rt/http.c`) and the C test agent's channel
  (`rt/sngl_win32_agent.c`) are what C's own runtime calls.

## The Go wrappers: `sngl tool cgogen`

**A Go program calls C through a package that never changes.** Cgo runs over
every package that imports `"C"`, so a program calling C directly paid for it
on each build. `sngl tool cgogen sngl:platform/win32` (`internal/cgogen`, run
by `go generate ./lib`) writes `lib/platform/win32/cgo`: one exported Go
function per C function the platform and `api` declare, named for it
(`sngl_win32_append` is `SnglWin32Append`), and the Go type each handle and
each C struct they declare is (`type HWND = C.HWND`, `type Look struct`): one
no function crosses still gets one, since a program's own literal of a
struct names the wrapper package's, and a program holding a handle spells it
by its mark -- without one, as cgo's `*C.X`, which a program calling C
through wrappers has no `C` for ("undefined: C"). The wrappers' bodies spell such a
struct by the name the package declares (`golang.WrapperTypeName`), not by
the mark naming it, which the first generation runs without: it said
`_Allocation` where it declared `Allocation`. Both SNGL
packages go into one Go package, because a handle is a type of the cgo
package declaring it, and `C.HWND` in two packages is two types.

**A program's own package is wrapped by the same tool.** `sngl tool cgogen
<dir>` checks the directory as a package a program imports and writes its
wrappers into `<dir>/cgo`, in the program's Go module, and its marks into
the directory's files, which a `//go:generate sngl tool cgogen .` beside them
keeps in step (`cgogen.Dir`). A program reaches the wrapper package through
the temporary module it builds in, which replaces the host module with its
root as it replaced sngl's checkout (`codegen.WriteGoMod`;
`cmd/sngl/testdata/cgogen_user_package_runs.txt`).

**A wrapper's body is the call a direct one makes.** The tool builds a SNGL
function per wrapper whose body calls the declaration, lowers it with
`lower.CWrappers` (`passCMarshal`'s conversions, and no ternary), and writes it
with the Go language's own emitter. So a string, a struct and a callback cross
as they do in direct mode, a string copied for C is freed after the call as
`passReleaseTemps` frees it (`c.free` is written beside the wrappers), and
the callbacks' dispatchers are written into the wrapper package
(`callbacks.go`). A wrapper taking an `option` branches on null, since C takes
NULL only where a call writes null. A wrapper of a declaration taking a `ref`
is copy-in, copy-out, as `lower.RefParams` lowers every call of one: it takes
the value and hands back the one out, or a record of the result and each out
(`GetClientRectResult`), field for field the one the program declares, which
the program converts (`nativeCall`; `c_win32_test_runs.txt`'s `ref.sngl`). A
declaration a program reaches with no wrapper -- a `gir:` class gtk4's
wrappers do not cover -- is called directly: the program includes the
package's headers, and the headers its link ships, and none of its C, which
is the wrapper package's (`golang.CLinks`); a handle it holds as the wrapper
package's type crosses through `unsafe.Pointer` to its own cgo's, since
`C.HWND` in two packages is two types, and one C answers crosses back the
same way (`cmd/sngl/testdata/gir_scheme.txt`). A typedef the declaration
does not flag a pointer is either -- GLib's `gboolean` a number, Win32's
`HWND` a pointer -- so it crosses as its bits, which are one C type's in
both packages: `C.HWND(h)` did not compile, and a program declaring a
Win32 function of its own taking api's `HWND` built only calling C
directly (`cmd/sngl/testdata/c_win32_interop_runs.txt`).

**A static callback is wrapped, and so is a variadic function.** A declaration
taking a `#[c.static]` callback (`design/nodes.md`) is wrapped, though the
wrapper package cannot name the program's function C is to call: the
wrapper takes it as a Go `func` value, and the package's `callbacks.go`
exports one function per static parameter, named for the declaration and
the parameter (`snglStatic__customLayout_measure`,
`golang.WrapperStaticName`), calling through a package variable each call of
the wrapper sets. So a parameter calls one function at a time, the last
call's, which is all gtk4's probe layout and a flow's store hand it. Two
declarations of one C function taking a static callback are two wrappers
(`takesStatic`), whatever their types: sharing one, C called the second's
function through the pointer it kept for the first, and gtk4's flex, grid
and probe layouts all measured as the last made did
(`cgogen_user_package_runs.txt`'s `split_keep`). What an
`#[c.outs]` one answers is the program's record, which the package
declares again field for field (`GtkCustomLayoutNewMeasure`,
`golang.WrapperOutsName`), and the program's call hands the wrapper an
adapter converting the one into the other (`nativeCall`). cgo calls no
variadic function, so a variadic declaration's wrapper calls a C function of
its one shape (`snglV_<name>`), which the package's preamble defines as the
`static inline` one a program's own cgo would include from the
`sngl_variadic.h` it writes (`golang.VariadicShims`); the program writes
none for a call a wrapper takes. It was skipped, and a Go program calling
one then had `import "C"` of its own: gtk4 emits a signal through
`g_signal_emit_by_name` (`_signalEmitByName`), which
`testdata/toggle_change_payload.txtar` runs and denies `import "C"` in.

**A change to the runtime's C builds the wrappers again.** The package
compiles `rt/sngl_win32.c` through a stub including it, and Go's build cache
does not see a file outside the package that one includes: a test agent built
after a change to the runtime linked the old one from the cache. So each file
cgogen writes carries the digest of the C sources (`sourcesDigest`), and a
change to them is a change to the package, which `go generate ./lib` writes.

**Each declaration names its wrapper.** The tool writes
`#[go.native("duckfam.us/sngl/lib/platform/win32/cgo", "cgo.SnglWin32Append")]`
beside each wrapped `c.native` mark, and `ir.Foreign.Also` keeps it beside the
C spelling, which stays the primary (`claimForeign`). A declaration's position
is its first mark's line, which is how the tool finds where to write it.
Rerunning `win32gen` drops the marks from `api.sngl`; `go generate ./lib`
writes them again.

**Go calls the wrapper.** `ir.Foreign.In("go")` is a declaration's Go
spelling, and the Go language reads it wherever it reads a declaration's
`Foreign` for a call or a type (`wrapper` and `spelling`,
`codegen/lang/golang/cinterop.go`): a call is written as any `go.native`
call, into the wrapper package, and a handle or a C struct is the wrapper
package's type. Nothing rewrites the declarations, since the library's are
frozen and a protocol function placement splices is the library's own.
`passCMarshal` is told the build's language (`Features.language`, the
language node's `#[gen.name]`, which is also the scheme its native marks
spell in) and converts nothing for a call of a declaration with a spelling
there; the splices placement and the declarative passes make after it are
told the same (`protocolSplice`). A `#[c.native(…, cast)]` to a type with a
Go spelling is its argument, since both sides are the one handle there
(`testdata/c_go_wrapper_call.txtar`, where `time` stands in for a wrapper
package). A C build keeps every call C's, and the interpreter, simulating C,
marshals as C does.

**The wrappers write C, and name their own types bare.** The context writing
them (`GoIRContext.Wrapping`, the package's import path) calls each
declaration's C spelling, never the wrapper it is writing, and spells a type
the package declares unqualified (`typeToGo`); the callbacks it exports take
a C type as C spells it (`CallbacksSource`'s `wrapping`). A Go program's own
`CLinks` and callbacks leave out a wrapped package's, which the wrapper
package links: compiled into the program as well, its sources would define
each symbol twice.

**Or the program calls C itself.** The Go language's `cgo` option says how
Go calls C: `"wrapper"`, the default, as above; `"direct"`, every C
declaration from the program; and `"purego"`. The build records `"direct"`
on its copy of the program (`ir.Package.CCalls`, `build.ApplyCgo`,
which `PrepareTarget` and a test's launch run), and every reader of a
wrapper reads it there: lowering has no wrapper language, so `CMarshal`
converts every call; the Go writer's `wrapper` answers nil, so a call is
`C.` and a handle or struct is cgo's type; and `CLinks` leaves out no
package, so the program writes `import "C"`, compiles each link's sources
beside it and imports no wrapper package, which would define each runtime
symbol a second time. The program declares each library C struct it reaches
(`libraryCStructs`), as the wrapper package declares it, and discards a C
call's converted result where the call is a statement. Nothing rewrites a
declaration here either: a protocol function a splice reaches is the frozen
library's. A function taking a `ref` is called this way too
(`c_win32_test_runs.txt`'s `refrun.sngl`), and a gtk4 program runs whole (`testdata/gtk4_go_direct_runs.txtar`).

**Or through purego, with no cgo.** Under `"purego"` lowering marshals for a
language that takes Go's own values (`cPurego`): no call is converted and a
cast is its operand. Each C function the program calls is a func variable
of its own (`snglC_<name>`), which `sngl_purego.go`'s init binds through
`pkg/go/cload` to the function of that name in the library its package's
c.link names (`library=`): `lib<name>.so`, or the first version of it that
opens, on Linux, `.dylib` on macOS and `<name>.dll` on Windows. A call
converts each argument to the Go type of its C type's size -- an int is
`int32`, a float `float64`, a bool an `int32`, an alias the size of the C
type it spells (`Param.CAlias`, `Func.ReturnCAlias`) -- and the result
back; a string crosses as purego copies it, and a handle C holds opaquely as
an `unsafe.Pointer`. What purego cannot do is refused, naming it: a package
whose c.link ships C to compile (gtk4's and win32's runtimes), a c.link
naming no library, a `ref` C fills, a C struct by value, and a callback,
since purego's are never given back
(`testdata/c_go_purego_call.txtar`, `go_cgo_purego_refused.txt`).

**What each costs a build.** Rebuilt after a change to the program, its
dependencies built already (November 2026, one Linux machine): a gtk4
program takes 0.4 s calling the wrappers and 4.3 to 5.5 s called directly,
since cgo runs over the program and the runtime it compiles every time; a
bubbletea program calling libm takes 0.2 s called directly and 0.1 s
through purego. `wrapper` stays the default for that.

**A platform's entry calls the runtime through functions of the program's
own.** The entry, the test agent, `async.do`'s post, a test's wait and the
carried files call `sngl_rt.go`'s `snglWin32Run`, `snglPost` and the rest
(`goshim.go` in each platform), which hand the call to the wrapper package
or, called directly, make it themselves, a closure crossing as a context
their own trampoline looks up. The platform's `reservedNames` holds each, so
the hooks and the entry are written once whatever the option says.

**Its test holds both, and builds the package.** `TestCommitted` regenerates
the package and the marks and compares them with what is committed;
`TestBuilds` builds the package with both MinGW compilers, recorded in
`internal/cgogen/testdata/builds/<arch>` as a digest of the package, the
runtime it includes and `pkg/go/cbind`. The package's files are behind
`//go:build windows`, since `windows.h` is no other system's, and its `.c`
file includes `rt/sngl_win32.c` from where it is, because cgo compiles only
the `.c` files of the package's own directory.

## win32 in Go

**A placed program is written whole, as in C.** `golang.WriteProgram` writes
what `passPlacement` left: the program's types, each package var a field of
the one state `m`, each const a file-scope var, each function free (a method
as the free function `LiftsToFreeFunc` names, as on every Go platform), and
`snglInit`, `snglBuild` and `snglTeardown`. win32 writes the entry, which runs
the loop (`snglWin32Run`, `sngl_rt.go`). The loop's calls and the
snapshot's are declared in `win32.sngl` for the wrappers to carry (`_rtRun`,
`_rtSnapshot`, `_rtStep`), though no SNGL calls them. Where the platform node
wrote `@run`, WriteProgram writes the handler as `snglRun` and the entry is
C's `appMain` in Go: the windows held, the handler handed `os.Args[1:]` and a
`run` that presents them, runs the loop and refuses a second call
(`testdata/win32_go_run_handler_runs.txtar`).

**What placement writes is held as a free function holds it.** The writer
had only been asked for a Model's methods, and three answers changed for a
placed program. A node placement made is a local of the function that made
it, so an element ref nothing else resolves -- a `LocalVar` placement wrote
without a symbol, or a handler's read of the node it is on -- is that local,
not a field of `m`. A record is a box (`StructDef.RefBox`), so Go spells it
`*T` and builds it with `&T{…}`, and `ir.CopiedStruct` answers nil for one:
every copy shares it. A handle C keeps opaque (`HWND`) has no composite
literal; its zero is `*new(T)`, and is left out of a struct literal.

**It is cross-compiled by the Go toolchain calling MinGW.** win32 writes
`sngl.build`'s `target windows/<arch>` line for Go as for C, and
`golang.BuildModule` reads it: `GOOS`, `GOARCH`, `CGO_ENABLED=1` and `CC` the
MinGW compiler C's matrix names, into an `.exe`. The wrapper package's
`-mwindows` makes it a GUI program.

**The files a program carries reach the runtime through a call.** C's entry
hands the runtime its build's `sngl_win32_assets` table (`sngl_win32_carry`);
Go's `assets.go` embeds each file and hands it over before the program builds
(`sngl_win32_carry_file`, which copies it). The runtime looks a URL up in
both.

**`async.do` posts through the runtime's `sngl_post`**, as C's does: a
platform intrinsic (`codegen/platform/win32/async.go`) calls the wrapper
`SnglPost` with the closure.

**`sngl test` in Go is C's agent written from the same tests.**
`golang.WriteTests` writes each test of the placed program as C's
`WriteTests` does -- reset (teardown, then `snglInit`), `snglBuild` unless
the test mounts, the body through Go's test lowering -- and registers it with
`testagent`. The entry dials the driver's loopback port, as C's agent does,
and serves it on a goroutine through `testagent.Serve`, which hands each run
of tests back to main to run on the thread its windows belong to. A test's
`wait` pumps the loop through `SnglWin32Step` (a platform intrinsic, as
gtk4's is), and a snapshot draws the shown window at its size into a file
(`sngl_win32_snapshot` with no size), which the agent sends as the PNG the
driver makes of C's pixels. Each win32 script runs its programs in both
languages, the snapshot scripts holding Go's to the PNG C's run wrote.

**The snapshot tests hold Go to C.** Each of `snapshot_test.go`'s programs is
built in both languages, and the window Go draws must be C's, pixel for
pixel; the record's digest covers both programs, the wrappers and the runtime
they include.

## The runtime's layout

**Win32 has no layout manager.** A child window sits at the pixel position it
was given, and nothing moves it when its parent is resized. So
`lib/platform/win32/rt/sngl_win32.c` keeps a record beside every window it
makes. The record is a `ctl` the runtime's own table finds by the window's
handle (`ctl_of`). It was a window property, which under Wine is a trip to
its server on each read, and a layout reads thousands. It holds:

- the node's kind;
- its children in order, since `GetWindow(GW_CHILD)`'s order is z-order;
- what its look asks for (`sngl_win32_set_look`, from `style.sngl`'s
  `lookOf`): flex, gap, padding, least width, the width and height its
  style gives, how a line box aligns its children, hidden, color, background
  and font size;
- for a box, how it lays its children out: its mode and direction
  (`sngl_win32_set_mode`, `sngl_win32_set_vertical`).

Layout reads only these records and each control's measured size. The shape
is gtk4's `_flexMeasure` and `_flexAllocate`, measure then
allocate. The code is win32's own, because its modes and its controls'
metrics are its own.

**Measure.** `measure` is a node's natural size in device pixels. It sums the
following, then adds the node's padding, raises the result to its least
width, and takes the width and height its style gives in place of its own.
A size had been the least a node was, so the Pokédex's 96 px artwork grew
to the picture's 475 px as each arrived, moving every card in the grid
(`TestSnapshotSized`):

- text, a button, a check box and a link are their text in their font
  (`DrawTextW` with `DT_CALCRECT`) plus fixed margins. A button is at least
  75 px wide;
- an entry, a text area, a progress bar and a date picker are fixed metrics,
  in lines of their font where they have lines;
- a table, a tree and tabs ask the control for their rows and columns;
- a canvas is the size it was given;
- a flow is its words wrapped to 240 px (`design/markup.md`);
- a box is `box_measure`, which follows its mode:
  - a line (a vbox or an hbox) is its children end to end, with gaps;
  - a grid is its rows of equal columns;
  - a flow is its widest child, and the height it last wrapped to;
  - a stack is its largest child;
  - equal shares are its largest child times their number.

A hidden node, by its prop or by its look, takes no room.

**Allocate.** `lay_out` places a box's laid children inside its client area,
less padding and a framed box's edge, as its mode says.

- A line gives each child its measured length along the axis. Across it, a
  child takes the whole box, unless the box's `alignItems` places it at its
  start, center or end at its measured size, or the child's style sizes it
  across, which places it at the start, as CSS stretches only a child with no
  size of its own. Spare room is shared by `flex`, and what rounding leaves
  goes to the last child that flexes.
- A grid makes columns of equal width, each row as tall as its tallest
  child.
- A flow wraps its children into rows.
- A stack gives every child the whole box, the last on top.

Every move in one box is a single `BeginDeferWindowPos` batch. After the
batch, `place` lays out each child that holds others in the bounds it was
given:

- a box or scroll by `lay_out`;
- tabs by showing its selected page under the strip;
- a flow by rewrapping;
- an image by sizing its picture to fill it, which paints itself fitted.

A scroll lays its children out at the size they measure along each axis it
scrolls, offset by its position, and sets its bars from that.

**A component's contents box grows by what it holds.** A component that
renders a slot, or several nodes, at its top renders them into the box
`#[gen.protocol(contents)]` makes (`sngl_win32_contents`). That box flexes by
the most any child in it flexes (`flex_of`), as gtk4's `sngl_contents_new`
grows, and as CSS's `display: contents` makes its children the items of the
box holding it. It had no flex of its own, so the Pokédex's home, a `for`
over its lookups beside a `flex=1` vbox, kept its measured height in the
window, and its grid kept the 200 px a scroll measures at
(`TestSnapshotContents`).

**A node with no background shows the nearest one around it.** Each box
erases in the background of the nearest box holding it that has one
(`inherited`), and answers a control's `WM_CTLCOLOR*` with it, as a browser
shows a box's background through what it holds. It had painted the class's
face color, so a calculator's dark vbox showed only where nothing covered it.
A look set redraws what it holds for the same reason.

**A button given a background is drawn by the runtime.** A push button's
theme paints over whatever `WM_CTLCOLORBTN` answers, so a look with a
background makes the button `BS_OWNERDRAW`, and its box draws it on
`WM_DRAWITEM` (`paint_button`): flat in its background, darker while
pressed, its text centered in its color.

**A click bubbles.** Every override connects a node's `@click` whether the
program bound one or not, so a text or an image in a clickable card holds an
empty handler of its own, and took the click from the card. A press on a box,
on text (its `STN_CLICKED`) or on a picture, which lets the click through to
its image, fires its own handler and then every one of the boxes holding it
up to the toplevel (`press`), as a browser bubbles one. A button, a check box
and a link fire only their own. A test's trigger fires only the node's.

**A picture paints once off screen.** It erases nothing and paints its
backdrop and its image into a buffer it then copies, and keeps its bitmap
scaled to the size it was last painted at (`scaled`). Erasing first and
scaling the whole picture on each paint showed the window behind it for a
frame on every layout, which a resize makes many of.

**When it runs.** Anything that changes a measurement calls `relayout`:

- an append, insert or remove (`adopt`);
- a look set;
- a text set;
- a node shown or hidden.

`relayout` posts `SNGL_WM_LAYOUT` to the toplevel once. The `laying` flag
holds back every later request until that message runs, so a build that
appends a hundred controls lays out once.

The message then does three things:

1. It refreshes fonts for the toplevel's DPI.
2. It grows the window to its content (`fit`), never shrinking a window the
   user sized.
3. It lays out from the top.

`WM_SIZE` on a toplevel lays it out directly, and `WM_DPICHANGED` refreshes
its fonts and takes the size Windows suggests.

**A layout does only what changed.** Measured on a Pokédex-sized page under
Wine (90 cards, November 2026), a layout took 295 ms and a line of scrolling
190 ms. It now takes 3 ms, and a line of scrolling 0.3 ms:

- A node keeps what it measured (`measured`, `size`) until `relayout` is
  called on it or on something it holds, which clears it from there to the
  toplevel; a DPI change clears it in `refresh`. A box asks for its
  children's sizes for itself and again for each box above it, and each
  text's was a `DrawTextW`.
- A window is moved only where its box's layout puts it somewhere else
  (`placed`), and a box repaints only where something in it moved or it
  changed size, since what changes otherwise repaints itself.
- A text set to what it shows already is not set again
  (`sngl_win32_set_text`): a render writes every prop it binds, changed or
  not, and each set measured the text and everything holding it again. The
  Pokédex re-renders its grid as each species' name arrives, so a page of
  names arriving measured every card once a name, which held the window for
  seconds after a page more was shown.
- A scroll moves what it holds with `ScrollWindowEx` rather than laying it
  out at the new position (`scrolled`), from the extent its last layout
  found (`max_y`, `page_y`), and reports its end from that too (`reach`).

A flow, as either a box's mode or rich text, learns its height only by
wrapping at the width it is given. When that height changes it records it
(`flow_h`) and asks for another layout, which measures it at the new height.
The loop ends because the height for a width is fixed.

**Where a control is before it is placed.** A control must have a parent
window when it is created, and placement creates a node before it appends it.
So every control is created in the parked window: a `WS_POPUP` of the box
class that is never shown. An append moves the control under its parent with
`SetParent`.

The parked window also does three other jobs:

- it owns the timers;
- it receives `sngl_post`'s messages;
- it owns the tooltip window every box with a tip shows through.

**DPI.** A look's lengths are SNGL px, a 96th of an inch. `px(n, dpi)`
scales them to the window's DPI wherever layout reads them, so a record holds
the px it was given and not device pixels. The `dpi` a record holds is its
toplevel's, set by `refresh`. A control created in the parked window takes
its toplevel's DPI when it is put in one.

## Requests

**win32 answers sngl:http with WinHTTP rather than C's libcurl**
(`rt/http.c`): Windows carries it, so `app.exe` ships no curl. Its link
ships C's `sngl_http.h` alone, named `sngl:language/c/rt/sngl_http.h`, the
way a library `c.link` names another package's source, and defines its
client: each request runs on a thread of its own and its answer is posted
back to the loop (`sngl_post`), as libcurl's are, and C's own wait
(`sngl_fetch.c`) waits on it. A wait off a work's stack runs the loop
through `sngl_loop_wait`, which a quit ends: the wait then gives up as
cancelled.
