# win32

How `sngl:platform/win32` gets its Win32 declarations and lays its controls
out. What the platform is and how it builds, runs, tests and draws is its
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
is gtk4rt's `sngl_flex_measure` and `sngl_flex_allocate`, measure then
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
