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
exists; the headers decide its spelling.

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
written in a form C interop crosses (`design/nodes.md`, `#[cnative]`):

- A handle (a pointer to a `struct HWND__` or a `NativeTypedef` over
  `void *`) is an opaque `#[macro.cnative("HWND")] struct HWND {}`.
- A scalar typedef is `struct UINT = int` under its C name.
- A struct is written field for field, if every field crosses.
- A constant is a `const`, if its value fits an `int`.
- A function is `#[macro.cnative("SetWindowTextW")] func SetWindowText(…)`.
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

- a callback with no context (a `WNDPROC`);
- arrays and unions;
- GDI+, WIC and the common controls' notification structs.

The SNGL in `win32.sngl` calls `api.sngl` for what a single Win32 call does
(`SetWindowText`, `ShowWindow`, `SendMessage` with a generated `BM_SETCHECK`)
and the runtime for the rest.

## The Go wrappers: `sngl tool cgogen`

**A Go program calls C through a package that never changes.** Cgo runs over
every package that imports `"C"`, so a program calling C directly paid for it
on each build. `sngl tool cgogen sngl:platform/win32` (`internal/cgogen`, run
by `go generate ./lib`) writes `lib/platform/win32/cgo`: one exported Go
function per C function the platform and `api` declare, named for it
(`sngl_win32_append` is `SnglWin32Append`), and the Go type each handle they
cross and each C struct they declare is (`type HWND = C.HWND`, `type Look struct`): a struct no function crosses still gets one, since a program's own
literal of it names the wrapper package's. Both SNGL
packages go into one Go package, because a handle is a type of the cgo
package declaring it, and `C.HWND` in two packages is two types.

**A wrapper's body is the call a direct one makes.** The tool builds a SNGL
function per wrapper whose body calls the declaration, lowers it with
`lower.CWrappers` (`passCMarshal`'s conversions, and no ternary), and writes it
with the Go language's own emitter. So a string, a struct and a callback cross
as they do in direct mode, a string copied for C is freed after the call as
`passReleaseTemps` frees it (`c.free` is written beside the wrappers), and
the callbacks' dispatchers are written into the wrapper package
(`callbacks.go`). A wrapper taking an `option` branches on null, since C takes
NULL only where a call writes null. A declaration with a `ref` parameter has
no wrapper yet: a call of one hands back a record of its results that the
program's package declares, which the wrapper package cannot name. The file
lists each, with why, and a Go program calling one is refused at generate
(`unwrappedCalls`, `codegen/lang/golang/ccompiler.go`): the program links
none of the package's C, which is the wrapper package's, so it has no cgo of
its own to call it through.

**A change to the runtime's C builds the wrappers again.** The package
compiles `rt/sngl_win32.c` through a stub including it, and Go's build cache
does not see a file outside the package that one includes: a test agent built
after a change to the runtime linked the old one from the cache. So each file
cgogen writes carries the digest of the C sources (`sourcesDigest`), and a
change to them is a change to the package, which `go generate ./lib` writes.

**Each declaration names its wrapper.** The tool writes
`#[go.native("duckfam.us/sngl/lib/platform/win32/cgo", "cgo.SnglWin32Append")]`
beside each wrapped `cnative` mark, and `ir.Foreign.Also` keeps it beside the
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
told the same (`protocolSplice`). A `#[cnative(…, cast)]` to a type with a
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
the loop through the wrappers (`SnglWin32Run`). The loop's calls and the
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
makes. The record is a `ctl` reached by `GetPropW(w, "sngl.ctl")`, and holds:

- the node's kind;
- its children in order, since `GetWindow(GW_CHILD)`'s order is z-order;
- what its look asks for (`sngl_win32_set_look`, from `style.sngl`'s
  `lookOf`): flex, gap, padding, least width and height, hidden, color,
  background and font size;
- for a box, how it lays its children out: its mode and direction
  (`sngl_win32_set_mode`, `sngl_win32_set_vertical`).

Layout reads only these records and each control's measured size. The shape
is gtk4's runtime's `sngl_flex_measure` and `sngl_flex_allocate`, measure then
allocate. The code is win32's own, because its modes and its controls'
metrics are its own.

**Measure.** `measure` is a node's natural size in device pixels. It sums the
following, then adds the node's padding and raises the result to its least
size:

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

- A line gives each child its measured length along the axis and the whole
  box across it. Spare room is shared by `flex`, and what rounding leaves goes
  to the last child that flexes.
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
