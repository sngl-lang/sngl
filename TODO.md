# TODO

## C interop, a C language, and a win32 platform

The goal is `sngl:platform/win32`: classic Windows GUI programs over user32,
gdi32 and comctl32. Win32 is a C API, and so are GTK and cairo. A platform
built on a C library should name C and nothing else, and leave it to each
language to call C: Go through cgo, C directly, Python through cffi or
ctypes. Today that is not so. `sngl:x/c` and the declarations gtk4 writes
spell cgo (`C.CString`, `*C.cairo_t`, `unsafe.Pointer`), and gtk4's protocol
leans on a Go runtime package. `lib/platform/gtk4/gtk4.sngl` has 54
`#[go.native]` declarations next to 34 `#[cnative]` ones. So gtk4 builds only
in Go, and win32 written the same way would too.

So the work comes in three parts, in order:

1. **C interop belongs to the language translator.** The IR describes C (types,
   conversions, links, callbacks, structs) as C spells it. Each translator
   renders the interop for its language. Go is the first and only one to
   begin with, and the refactor changes no Go golden.
2. **A C language target**, the second translator to render it. Its ownership
   rules were already decided in `design/ownership.md` (*A C language
   target*).
3. **The win32 platform**, written against Part 1 only. Its runtime glue is C
   shipped with the platform, so it builds with either language.

Python is the third language the abstraction should admit. It is not part of
this plan, but each decision in Part 1 is checked against it: Python loads
a library at run time rather than linking one, and has no compiler step of
its own.

### What was verified on this host

None of this is in the repository yet; each was run by hand on 2026-11-03.

- `mingw-w64-gcc` and `wine` (11.19) are Arch packages in `extra`, and
  `[multilib]` is not needed: Arch's `wine` is a WoW64 build and runs a 32-bit
  `.exe` without it.
- `i686-w64-mingw32-gcc -static -mwindows` and the `x86_64` compiler build a
  plain C Win32 program (a window, a menu bar, a button, an edit control,
  a message box, GDI drawing) that runs under Wine.
- `CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -ldflags=-H=windowsgui` builds a GUI `.exe`, and `GOARCH=386 CC=i686-w64-mingw32-gcc` a 32-bit one. Both ran under Wine. In both, a
  `WNDPROC` that is a static C function calling an `//export`ed Go function
  received the messages, and the Go-side `GetMessage` loop ran and exited.
  Neither needed MinGW's runtime DLLs next to it.
- cgo spells Win32's typedefs as aliases of the type they name:
  `(*C.wchar_t)(p)`, `(*C.WCHAR)(p)` and `C.LPCWSTR(p)` are all accepted by
  `SetWindowTextW`. A `UINT`, `WPARAM` or `LPARAM` parameter refuses a
  `C.int`, which is what `CMarshal` writes for an `int` today.
  `int` parameters (`ShowWindow`'s `nCmdShow`) take one.
- Under `cage`, Wine draws through its **X11** driver on the Xwayland cage
  starts (cage sets `DISPLAY=:0` for its child). The **Wayland** driver
  (`HKCU\Software\Wine\Drivers\Graphics=wayland`) hung until killed. cage
  maximizes the window (1288×728 on the headless output), so a snapshot must
  fix the size, as gtk4's 800×600 does.
- With no display at all, `Graphics=null` runs a GUI program headless, and
  `PrintWindow` into a DIB section still captures the window's controls, menu
  bar and GDI drawing. Without that setting, Wine with no display hung.
- A fresh `WINEPREFIX` takes several seconds to create on first use.
  Killing a Wine process mid-setup left a prefix that failed with `could not load kernel32.dll` until it was recreated.
- cage needs `XDG_RUNTIME_DIR` short enough for a 108-byte socket path.
  `headless.RuntimeDir` already makes one under the system temp directory.

## Part 1: C interop belongs to the language translator

### Where Go leaks today

- **`#[cnative]` names are cgo spellings.** The mark's own doc says the name is
  "spelled as the emitting language reaches it" (`lib/macro/foreign.sngl`).
  Of the 43 names in `lib/`, `codegen/` and `internal/`:
  - 26 are bare C functions, which are neutral already.
  - 13 are `C.X` types or casts.
  - 2 are `*C.X` pointers.
  - 2 are `unsafe.Pointer`.

  gtk4's `declgen` writes more of them for every GIR class (`_c_`/`_to_`
  casts, `_as_` pointer casts written as `(*C.GtkButton)`, `_ptr_` as
  `unsafe.Pointer`; `codegen/platform/gtk4/declgen.go:165-316`).
- **`sngl:x/c` is cgo.** `str` is `*C.char`, `cstring` is `C.CString`,
  `integer` and `double` are `C.int` and `C.double`, `pointer` is
  `unsafe.Pointer` (`lib/x/c/c.sngl`).
- **The Go renderer guesses from the spelling.** It prefixes a bare name with
  `C.` and imports `unsafe` for an `unsafe.` one (`nativeCall`,
  `codegen/lang/golang/ircontext.go:1152`). A C function's `int` or `float`
  return is wrapped `int(…)` or `float64(…)` there too.
- **Callbacks exist only as Go.** gtk4 connects a signal through gtk4rt's
  `Connect`. That is a `#[go.native]`, whose C trampoline calls an `//export`ed
  Go dispatcher over `pkg/go/cbind`'s registry. No `#[cnative]` function can
  take a `func`.
- **Structs and out-parameters cannot cross at all.** `CMarshal` converts
  `string`, `int`, `float`, `bool` and `option[string]`, and hands anything
  else over as it is (`internal/lower/c_marshal.go:179`).
- **gtk4's runtime is Go.** gtk4rt holds the trampolines, the layout manager
  (`sngl_flex_measure`/`sngl_flex_allocate`, which is C in a cgo preamble),
  the toplevel and image handling. gtk4's inline-cgo mode writes Go with `C.`
  calls in `codegen/platform/gtk4`, and `SupportedLangs` is `{"go"}`.

### The design

1. **`#[cnative]` names C as C spells it.**
   - A function is its C name (`cairo_arc`), as today.
   - A type is a C type: `#[cnative("cairo_t *")] struct CairoContext {}`,
     `#[cnative("cairo_line_cap_t")]`, `#[cnative("const char *")]`.
   - The checker parses the name once into an `ir.CType` (a name, a pointer
     depth, `const`) on the declaration's `Foreign`, and refuses anything
     beyond that small declarator grammar: no arrays and no function-pointer
     types, which item 5 answers instead.
   - No language prefix is written anywhere: `C.` and `unsafe.` are the Go
     translator's to add.
2. **A conversion is an IR operation, not a function named after a Go
   identifier.**
   - `CMarshal` writes an `ir.CConvert{Kind, To *ir.CType}`:
     - a string to `char *` or to UTF-16
     - a number to a C typedef
     - a bool to `int`
     - a pointer to another pointer type (gtk4's `_as_` casts and its `void *` widget handle)
   - The `sngl:x/c` conversions keep their `#[builtin]` marks, which say which
     operation they stand for. They lose their `#[cnative]` spellings.
   - The translator renders the operation:

     | Operation          | Go                                   | C                                          | Python (cffi)           |
     |--------------------|--------------------------------------|--------------------------------------------|-------------------------|
     | string to `char *` | `C.CString(s)`                       | the arena's NUL-terminated string, no copy | `s.encode()`            |
     | string to UTF-16   | `cbind.UTF16(s)` cast to `*C.ushort` | a converter in the C runtime               | `s.encode("utf-16-le")` |
     | pointer cast       | `(*C.T)(unsafe.Pointer(p))`          | `(T *)p`                                   | `ffi.cast`              |

   - gtk4's hand-written casts (`toLineCap`) and declgen's `_to_` and `_as_`
     functions become this operation.
3. **Typed scalars and wide strings are part of the IR's C.**
   - **Typed scalars:** `#[cnative("UINT")] struct UINT = int` names a C
     typedef SNGL spells as an `int`. A caller passes an `int`, since an
     alias is what it names (spec, *Aliases*). A C function's parameter
     written with the alias crosses through a cast to it, not as a C `int`.
   - **Wide strings:** `sngl:x/c` declares
     `#[cnative("const unsigned short *")] struct utf16 = string` as the
     spelling of a parameter that crosses as UTF-16, and `option[c.utf16]`
     as one that may be NULL. `unsigned short` rather than `wchar_t`, which
     is 32 bits off Windows and needs a header: cgo makes a typedef an alias,
     so `*C.ushort` is what `LPCWSTR` takes (checked on Linux with a
     stand-in typedef; MinGW is not on this container).
   - The checker expands an alias where it is written (`expandAlias`), so the
     parameter has to remember it. `buildParams`
     (`internal/checker/resolve.go:800`) records a new
     `ir.Param.CAlias *ir.Alias`. It records the alias, not its C name, because
     marks run at the end of pass 1 (`afterMarks`), possibly after the
     parameter is built.
   - `markCNative` (`internal/checker/marks_impl.go:296`) accepts an alias of
     `int`, `float`, `bool` or `string`, and refuses any other.
   - Check that `ir.ClonePackage` shares the alias, and that the dump writes
     it as a reference.
   - The string's memory differs by language. In Go it is Go memory, valid
     for the call, which cgo permits since it holds no Go pointers, so it is
     not `#[owned]`. In C it is the arena's. Each string a call hands Win32
     or GTK is copied by the callee, which is what the plain conversion
     assumes. A `#[retains]` parameter is the existing mechanism for one that
     is not.
4. **`c.link` describes the library, and each language builds against it.**
   - Go writes the cgo preamble from it, as `golang.CPreambleLines` does. C
     writes the `#include` lines and passes the flags to its compiler.
   - Python links nothing: it opens a shared library at run time. So
     `c.link` gains the library's name (`library="user32"`, `"gtk-4"`), which
     pkg-config's `--libs` gives for the `c:pkg:` scheme. A Python translator
     would also need the declarations as cffi `cdef` text, which the IR's
     `#[cnative]` declarations are enough to write.
   - `c.link` also gains `sources`: C files the platform ships, compiled into
     the program (item 7).
5. **A callback is a C function type the language implements.**
   - A `#[cnative]` function may take a SNGL `func(…)` where C takes a
     function pointer and a `void *` context, the shape GLib's signals,
     `qsort_r` and most C APIs offer. The declaration names which parameter
     is the context (a mark on the parameter, or a flag on `cnative`).
   - The translator writes the trampoline. Go writes a static C function in the
     preamble calling an `//export`ed dispatcher over `cbind`'s registry, which
     is gtk4rt's `Connect` made generic. C writes a function taking the
     closure's environment as the context. Python writes `ffi.callback`, kept
     alive while it is connected.
   - A callback with no context parameter, as `WNDPROC` is, is not declared
     this way. The platform's C runtime (item 7) receives it and finds the
     context itself (`GWLP_USERDATA`).
   - Releasing a callback is the connection's lifetime, as `cbind.Release`
     is today. Tie it to the node that connected it through
     `design/ownership.md`'s node lifetime.
6. **A C struct is a SNGL struct with a C layout, and an out-parameter is a
   `ref`.**
   - `#[cnative("RECT")] struct RECT { left int; top int; right int; bottom int }`, whose fields are typed with item 3's aliases where C's are typedefs.
   - The translator converts it field by field at the call: Go through `C.RECT`,
     C as the struct itself, Python through `ffi.new("RECT *")`.
   - A `ref[RECT]` parameter (spec, *References*) is a pointer the callee
     fills: `GetClientRect(hwnd, &r)` writes `r` when the call returns, which
     is what `ref` already means.
   - An array, a union or a struct that holds pointers to C memory is not
     covered. Such calls stay in a platform's C runtime.
7. **A platform's runtime is C, not a language's.** A platform may ship C
   sources beside its SNGL (`lib/platform/<name>/rt/*.c` and a header),
   named by its `c.link`'s `sources`.
   - Each language compiles them into the program: cgo compiles `.c` files in
     the generated package's directory, the C target compiles them with the
     program, and cffi compiles them through `set_source` or a prebuilt shared
     library.
   - What gtk4rt and `cbind` do in C today belongs there: trampolines,
     layout managers and a window procedure.
   - What stays a language's own is only what needs the language's runtime:
     posting work to the UI thread from a goroutine, and snapshots for the
     test agent.
8. **The interface.** `LangTranslator` gains a `CInterop` face
   (`codegen/codegen.go`):
   - spell a `CType`
   - render a `CConvert`
   - render a C call and a C struct value
   - write a callback's trampoline
   - build against a `c.link`

   golang implements it first. `nativeCall`'s `C.` and `unsafe.` cases and
   its `int(…)` wrap move behind it.

### The steps

1. **Neutral spellings, Go rendering, no golden changes.** Done: no golden
   changed. A cast is `#[cnative("cairo_line_cap_t", cast)]`, returning the
   struct that declares the type it names. `CConvert.Via` keeps the cast a
   conversion was written through, for the dump. The deny test is the
   checker's own refusal of a language's spelling
   (`testdata/error_cnative_spelling.sngl`), which covers every
   `#[cnative]` name rather than `lib/`'s.
   - Add `ir.CType` and the declarator parse in `markCNative`.
   - Add `ir.CConvert`, and have `CMarshal` write it.
   - Add golang's `CInterop`.
   - Rewrite `sngl:x/c`, gtk4's hand-written `#[cnative]` declarations and
     `declgen`'s output in C spellings.
   - The goldens that hold C calls must not change: `testdata/c_link.txtar`,
     `c_bool_argument.txtar`, `gir_transfer_full.txtar` and gtk4's
     `cgo=true` goldens. They are the refactor's proof. Deny `C.` and
     `unsafe.` in `lib/` source with a test over every `#[cnative]` name.
2. **Typed scalars and UTF-16** (design item 3). Done:
   `testdata/c_alias_argument.txtar` (`towupper`'s `wint_t`),
   `testdata/c_utf16_argument.txtar`, `testdata/error_cnative_alias.sngl`.
   An alias takes `#[cnative]` and no other mark. The alias's kind is
   checked once it resolves (`checkCAlias`), since a mark calling into
   resolution makes an initialization cycle through `markImpls`.
3. **`c.link` gains `library` and `sources`** (design items 4 and 7). Done:
   the c: scheme writes `library` from the first `-l`
   (`cmd/sngl/testdata/c_pkgconfig.txt`), and `c_utf16_argument.txtar` ships
   its C function. A library's sources are read from `lib.FS`, whose
   `go:embed` holds only `.sngl` until the first platform ships one: add the
   pattern with it.
4. **Callbacks** (design item 5). Done for any `#[cnative]` function:
   `testdata/c_callback_argument.txtar` runs a held and a retained callback
   through shipped C, and `error_c_callback.sngl`,
   `error_c_context_mark.sngl` and `error_c_context_argument.sngl` hold the
   rules. The context is a parameter marked `#[c.context]`, typed
   `c.pointer`, which no caller passes, and a function C calls to release
   it `#[c.release]`. A `#[retains]` callback is never released; tie it to
   the node that called it (`design/ownership.md`'s node lifetime) as an
   owned value is, where C offers no release.
   - **gtk4 under `cgo=true` connects through it**: declgen's
     `signalConnect` is `g_signal_connect_data`, and a `notify::` event its
     own `on` protocol (`testdata/gtk4_cgo_connect.txtar`,
     `cmd/sngl/testdata/gtk4_cgo_connect_runs.txt`). The default build keeps
     `gtk4rt.Connect`: a connect calling C there puts every program with a
     handler on the cgo path (`design/architecture.md`, *Which file a
     program gets*). GLib releases each handler through `destroy`, marked
     `#[c.release]`, so a handler goes with its widget.
5. **Structs and `ref` out-parameters** (design item 6). Done:
   `testdata/c_struct_argument.txtar` passes a struct by value, has C fill
   one and two `ref[int]`s, and a field written with a typedef's alias; the
   rules are `error_c_struct.sngl` and `error_c_struct_mark.sngl`. A struct
   is converted at each call rather than held in C's layout, since cgo's
   field types would reach every SNGL expression reading a field.
6. **Docs.** Done: `cnative`'s doc in `lib/macro/foreign.sngl`,
   `lib/x/c/doc.sngl`, the `#[cnative]` bullet in `design/nodes.md`, the
   `CInterop` note in `design/architecture.md`, and `design/ownership.md`'s
   C target, where `CMarshal` still runs for what no caller passes.

### Open questions

- **Decided: what of gtk4rt moves where is settled with gtk4's move onto
  placement** (Part 3, step 8). What is ordinary GTK calls -- toplevels,
  images, menus, tabs -- becomes protocol functions in `gtk4.sngl`, as
  placement puts a platform's logic in SNGL. Only what cannot be SNGL stays
  in the C runtime: the layout's measure and allocate, and the trampolines
  until a callback crosses wholly. gtk4's C programs already have that
  runtime in `lib/platform/gtk4/rt/`; its Go programs keep gtk4rt until the
  move.

## Part 2: A C language target

`lib/language/c` and `codegen/lang/c`, the second translator to implement
`CInterop`, where it is mostly the identity.

- **Ownership as decided.** `string`, `list` and `map` are owned, with no
  destructor. Statement temporaries live in a per-turn arena the event loop
  resets. A value stored is copied to the heap (`__snglHeap`), and freed on
  overwrite by `OwnedState`'s effects. `CMarshal` still runs, for the
  arguments no caller passes, and C renders its conversions as identities or
  casts (`design/ownership.md`, *A C language target*).
- **What C needs that no target has needed:**
  - closures as an environment struct and a function pointer
  - generics, monomorphized (`design/types.md`)
  - a runtime for lists, maps, string formatting and interpolation,
    `option`, payload enums, and `async` continuations

  The runtime is C shipped in `pkg/c/`, as `pkg/go/` is Go's.
- **Which C.** The output compiles as both C11 and C17, held by
  `-std=c11` and `-std=c17` with `-Wall -Wextra -pedantic -Werror`. A program
  needing more than both admit gets a build option rather than a newer floor.
  gcc is the reference compiler; clang and MinGW are held to it wherever the
  host has them, and a test missing one skips rather than fails. `int` is
  `int64_t`, so the spec's 64 bits hold on C as on Go and Kotlin.
- **gtk4 is the first platform**, ahead of win32 (Part 3). gtk4 already calls
  GTK through `#[cnative]` declarations under `cgo=true`, so on C its widget
  declarations are those, whatever the option says. What a C program calls in
  gtk4rt moves to C shipped with the platform (`lib/platform/gtk4/rt/`, named
  by its `c.link`'s `sources`), and the Go build under `cgo=true` calls the
  same C.
- Recorded runs and goldens follow the Go ones: `out/c/<platform>/…`,
  `run/c/<platform>` and `testrun/<platform>/c/…`.

### The steps

1. **The language and its runtime.** `lib/language/c` declares the `c` node:
   its capabilities, its reserved words, and `cc` and `std` options.
   `codegen/lang/c` renders functions, structs, enums and statements over
   scalars and strings, and `pkg/c/sngl.h`/`sngl.c` hold the arena and the
   string operations. Its tests compile what it writes with every compiler
   present, under both standards, and run it. Done:
   `codegen/lang/c/testdata/run/*.txtar` run under `TestRun`, which builds
   each with gcc, clang and MinGW where the host has them (Wine runs the
   last). A program's top-level names take an `s_` prefix, since it shares
   C's one namespace with GTK's headers. What C does not yet write is refused
   at generation, naming the construct.
2. **gtk4 writes C for the smallest program**: a window, a box, a label and a
   button whose click counts (the program `gtk4_cgo_connect.txtar` holds).
   gtk4's generator gains a C emitter beside its Go one, the gtk4rt calls such
   a program makes move to `lib/platform/gtk4/rt/`, and callbacks cross as the
   C translator's closures. Golden `out/c/gtk4`, and a script test that
   builds and runs it under cage.
   Done for that program: `testdata/gtk4_c_counter.txtar` (built with every
   compiler under both standards) and `cmd/sngl/testdata/c_gtk4_build.txt`.
   - Under C, gtk4 runs its cgo declarations whatever the option says: a
     platform's Configure is handed the language (`build.LangOption`).
   - What a cgo build still calls in gtk4rt -- a window, a box's append, a
     look -- is rt's C under the name `c_emit.go`'s `native` gives it, and
     anything else there is refused by name. Those calls should become
     `#[cnative]` declarations of rt's C, which the Go cgo build calls too.
   - `rt/look.c` is the one copy of the look's C: gtk4rt includes it.
   - A function pointer a C function takes is cast to the type
     `#[c.type("GCallback")]` names, which C needs and cgo does not.
   - Not yet: no closure's environment is freed but a callback's, which C
     releases, and the arena is never reset, since nothing copies a stored
     string out of it yet (step 4's ownership).
3. **`sngl test` on C.** The test agent's JSON-RPC (`pkg/go/testagent`) in
   C, and the test lowering, so a test function clicks, asserts and reads
   state. `run/c/gtk4` is recorded as `run/go/gtk4` is. Done for a click:
   `cmd/sngl/testdata/c_gtk4_test_runs.txt`. The agent is
   `pkg/c/runtime/sngl_test.c`, single-threaded, a failed `must` and a panic
   leaving a test by `longjmp`; the C language launches it
   (`LaunchTest`). Each test builds the program, since its state is the
   file's. A trigger emits the signal its handler is connected to, a value
   written into the widget first; `t.wait` runs GTK's loop, `t.snapshot`
   draws what the test shows (`sngl_capture`), and `t.setContext` writes the
   program's context var. `t.tick` and `t.test`, which only the interpreter and JS
   implement, are refused.
4. **The rest of the language**, each against the gtk4 goldens that need it:
   lists, maps, options, payload enums and `match`, closures, generics,
   string formatting and units, `async`. Each construct C does not yet write
   is refused by name at generation, never written as C that fails to
   compile.
   Done: of the gtk4 programs among the goldens, every one C refuses
   names what it needs, and what it refuses is a Go native (`fmt`, `os`,
   `time.sleep`, a family emitter written for Go), `sngl:http`,
   `sngl:json`, `sngl:settings`, a loop over an `iter`, and a test reaching
   a runtime instance's state. Ownership is built as
   `design/ownership.md`'s *What the C target builds* says, with the leaks
   it lists.
5. **The rest of gtk4**: styles, images, scrolls, menus, tabs, toplevels and
   canvases, moving what each needs out of gtk4rt into C. Done:
   `lib/platform/gtk4/rt/` (`design/architecture.md`, *gtk4 in C*), held by
   `cmd/sngl/testdata/c_gtk4_test_runs.txt`.
6. **Docs**: the spec's `int` row, `design/architecture.md`'s per-language
   notes, and `design/ownership.md`'s C target as built. Done.

### Open questions

- **Decided: the arena resets at `sngl_turn_leave`**, which each platform's
  runtime already calls where a turn ends: on gtk4 as a callback returns to
  GTK's loop, on win32 as a dispatched message does. Not before every value
  that outlives its turn -- a string stored in a global, a field, a
  collection -- is kept (`Context.Keep`) rather than left in the arena;
  resetting first would leave those dangling.

## Part 3: The win32 platform

`sngl:platform/win32` mirrors gtk4 in shape. Its SNGL declares the C functions
it calls and wraps them in the stdlib's components, and each widget's create,
append, remove, set and event are `#[gen.protocol]` functions. Its runtime is
C shipped with the platform (Part 1, item 7). It holds the window procedure,
the message loop and the layout engine. It builds with Go (through Part 4's generated wrappers, cgo against MinGW)
and with C (MinGW directly), and runs under Wine for tests and `sngl run`.

### Decisions

- **The C declarations are generated once and committed.** The source is
  [win32json](https://github.com/marlersoft/win32json), a JSON rendering of
  Microsoft's [win32metadata](https://github.com/microsoft/win32metadata)
  (`Windows.Win32.winmd`, which windows-rs and CsWin32 are generated from). It
  records each function, struct, enum, constant and callback type, and which
  pointer parameters are in, out or optional. Both projects are maintained:
  win32json was last updated in July 2026 and win32metadata had a preview
  release in October 2026. The classic API itself does not change, so the
  generator runs by hand and pins a win32json commit. Unlike GIR, the
  metadata has no widget model: a control is a class name (`"BUTTON"`), style
  bits and messages (`BM_SETCHECK`). So the widget wrappers are written by
  hand, over generated declarations.
- **Both architectures; amd64 is the default.** An `arch` option on the
  platform node takes `amd64` or `386`, and the test matrix, CI included,
  runs both.
- **The first milestone is a vertical slice:** `ui.window`, `ui.text`,
  `ui.button`, `ui.input`, `ui.checkbox`, `ui.vbox`, `ui.hbox` and
  `ui.spacer`, events, style basics, goldens, and a Wine-under-cage test
  launcher with snapshots. The rest of gtk4's overrides come after.

### The steps

1. **The generator: `internal/cmd/win32gen` → committed SNGL.** Done:
   `lib/platform/win32/api` (`sngl:platform/win32/api`), held formatted by
   win32gen's own test and checked as every library package is. win32json
   says what is declared and which parameters are out or optional; each C
   spelling comes from MinGW's preprocessed headers for both architectures,
   since the metadata writes `UInt32` where C writes `UINT` or `DWORD`, which
   cgo tells apart. An optional handle is written as a handle, losing only
   NULL. `GetWindowLongPtrW` is a macro on 386, so the C runtime calls it.
   - Run as `go tool win32gen`, it fetches win32json at a pinned commit (in a
     const) into a temp directory. The partitions are
     `UI.WindowsAndMessaging`, `Graphics.Gdi`, `UI.Controls`, `Foundation`
     and `System.LibraryLoader`. It writes
     `lib/platform/win32/api.sngl` with a `// Code generated … DO NOT EDIT.`
     header. It never runs in `verify`. A test checks only that the
     committed file parses, checks and is formatted.
   - It writes what a wanted-list (a Go slice in the tool) names, plus what
     those declarations reach. It does not write all of user32, which is
     thousands of declarations, most of them never called.
   - What it writes, all in Part 1's C spellings:
     - **Handle types** as opaque structs (`#[cnative("HWND")] struct HWND {}`, and `HMENU`, `HINSTANCE`, `HFONT`, `HBRUSH`, `HDC`, `HICON`,
       `HCURSOR`).
     - **Scalar typedefs** as aliases (`UINT`, `DWORD`, `WPARAM`, `LPARAM`,
       `LRESULT`, `BOOL`, `COLORREF`, `ATOM`).
     - **Structs** the slice needs (`RECT`, `SIZE`, `POINT`,
       `NONCLIENTMETRICSW`), with Part 1's struct crossing.
     - **Constants** as SNGL `const`s (`WS_*`, `WS_EX_*`, `WM_*`, `BS_*`,
       `BM_*`, `BST_*`, `ES_*`, `EN_*`, `SS_*`, `SW_*`, `SWP_*`, `COLOR_*`,
       `IDC_*`, `GWLP_*`).
     - **Functions** whose parameters Part 1 can cross: scalars, handles,
       `c.utf16` strings, structs, and `ref` out-parameters.
   - It skips the rest (callbacks with no context, arrays, unions) and
     reports each in a comment block at the end of the file.
   - Always the `W` entry point, never the `A`, and the name without the
     suffix where the metadata gives both.
   - Find out how win32json spells a 32-bit/64-bit difference
     (`GetWindowLongPtrW` is a macro over `GetWindowLongW` on 386). Generate
     the declarations both architectures can call, or keep that call in
     the C runtime.
2. **The C runtime: `lib/platform/win32/rt/`.** Done. C sources and a header,
   reached by the platform's `c.link(include="windows.h", system=true, library=…, sources=…)` with `-luser32 -lgdi32 -lcomctl32`.
   - One registered window class whose `WNDPROC` finds the control's
     handlers through `GWLP_USERDATA` and calls them through Part 1's
     callbacks: `WM_COMMAND` with `BN_CLICKED`, and `EN_CHANGE`.
   - The message loop as `run` and `quit`, and posting work to the loop for
     `asyncPost`: `sngl_post` (`pkg/c/runtime/sngl.h`), which C writes for
     `async.start` and `async.do` on every platform, is a private `WM_APP`
     message to the parked window, as gtk4's is an idle callback
     (`cmd/sngl/testdata/c_win32_test_runs.txt`). Go's half of the post,
     from a goroutine, is the Go language's own (Part 1, item 7).
   - **Layout.** Win32 has no layout manager. A child control sits at
     the pixel position it is given, and nothing moves it when its parent is
     resized. The runtime keeps a box tree beside the controls: a vbox or
     hbox is a container window or a record. It measures each child
     (`GetTextExtentPoint32W` for text and buttons, fixed metrics for an
     entry and a checkbox), lays out by `flex` on `WM_SIZE`, and moves
     children with `DeferWindowPos`. This is the largest piece of the
     runtime, and the measure → allocate shape of gtk4rt's
     `sngl_flex_measure` and `sngl_flex_allocate` (`pkg/go/gtk4rt/look.go`,
     C already) is the model, possibly shared.
   - **Fonts.** A control created bare draws in the System font. Every
     control gets `WM_SETFONT` with the message font from
     `SystemParametersInfoW(SPI_GETNONCLIENTMETRICS)`.
   - **Common Controls v6 and DPI.** The runtime activates Common Controls 6
     with `CreateActCtxW` at start-up, from the manifest it holds as a C
     string and writes to a temporary file, which needs no resource step in
     any language's build; Wine draws its flat themed buttons under it, where
     5 drew bevels. It is per-monitor DPI aware where Windows has the calls,
     found at run time, and system aware where not: a look's lengths are
     SNGL px scaled to the window's DPI where layout reads them, and the
     fonts follow a toplevel's DPI as it lays out and on `WM_DPICHANGED`.
     Checked by hand under Wine at 96 and 144 DPI, which no test pins, since
     a prefix holds one DPI.
   - Snapshot: `PrintWindow` into a DIB section. The PNG encoding is Go's
     `image/png`: the Go agent's own, and the driver's for the raw pixels the
     C agent sends.
3. **`lib/platform/win32`.** Done.
   - `win32.sngl`: the platform node.
     - It takes `#[gen.can(insertBefore, asyncPost, instanceState)]` and
       `#[gen.cannot(reactivity, declarative, inlineComponents, structSpread)]`, as gtk4 and fyne do, since Win32 also owns its controls
       and diffs nothing. With `instanceState` a component with state of its
       own is a record, a library override's included, and a test reaches
       an instance, or one copy of those a loop renders, through it
       (`cmd/sngl/testdata/c_win32_test_reach_runs.txt`).
     - Options: `package`, `main`, `@run`, `arch` (`"amd64"` default,
       `"386"`), and `reservedNames`.
   - The primitives (`#[marks.intrinsic("win32:Toplevel")]`, `Button`,
     `Static`, `Edit`, `CheckBox`, `Box`) and their protocol functions,
     calling `api.sngl` and the runtime only, with no `#[go.native]`:
     - Each create calls `CreateWindowExW` with the class name and style bits
       from `api.sngl`, then the runtime to parent and register the control.
     - Each set is a `SetWindowTextW` or `SendMessageW`.
     - Each event is an `on` through the runtime's handler table.
   - The overrides for the slice: `ui.window`, `ui.text`, `ui.button`,
     `ui.input`, `ui.checkbox`, `ui.vbox`, `ui.hbox`, `ui.spacer`.
   - `style.sngl`: `lookOf` and `const unstyled`. `flex`, `gap`,
     `padding` and `minWidth`/`minHeight` reach the runtime's layout, and
     `color`, `background` and `fontSize` reach `WM_CTLCOLOR*` and a created
     font. Everything else goes in `unstyled` with a reason.
     `lib/style_coverage_test.go` holds it to every field.
   - `doc.sngl`, the package doc.
4. **`codegen/platform/win32`.** Done.
   - Registered with `codegen.RegisterNative("win32", …)` and blank-imported
     in `codegen/platform/platforms.go`.
   - `SupportedLangs` is `{"go", "c"}` once Part 2 exists, `{"go"}` until
     then.
   - **Decided: placement is SNGL, not a generator's Go.** gtk4 writes it
     twice, in Go (`compiler_ir.go`) and in C (`c_emit.go`, `c_record.go`).
     win32 writes it neither way. What every platform shares -- a widget as
     a record's field or a local, a record's constructor and teardown, an
     instance built and destroyed, a slot's children appended -- is a
     lowering pass writing plain IR, which each language already renders.
     What one platform decides -- where a window attaches, what hosts a
     slot, how an anchor is held, what a record's root is, what teardown
     releases -- is a protocol function in its own package. win32's slice
     builds it; gtk4 moves onto it after, its recorded runs in both
     languages holding it unchanged.
   - **The design.** After `passDeclarative` a program is already nearly
     plain IR: each intrinsic left (`lower.CreateNode`, `AppendChild`,
     `RemoveChild`, `AttachHandler`, a prop's write) carries the protocol
     call it stands for. So placement is a lowering pass, `passPlacement`,
     which a platform asks for with `#[gen.wants(placement)]`:
     - `var x Button = CreateNode(tag, made)` becomes `var x H = made`, where
       `H` is the platform's handle type: the return type of its
       `#[gen.protocol(handle)]` function, a `#[cnative(…, cast)]` the
       protocol bodies call to reach the handle (`HWND` on win32). Every type
       the program writes as one of the platform's intrinsic components or
       as `ui.node` becomes `H`.
     - An append, a remove and a connect become the call they carry, the
       connect's handler parameter replaced by the handler; a handler nobody
       wrote is dropped. A prop's write becomes its set call.
     - The application's attach and detach (`ir.AppParent`) and a slot's
       insert before its next sibling are protocol functions too:
       `attach`/`detach` on the platform node and `insertBefore` on a
       container, which `can(insertBefore)` and `slot_placement.go` reach
       without anchors.
     - What is left is a package of structs, vars and funcs. Each language
       writes such a package whole (a `ProgramEmitter` beside
       `FileEmitter`): Go and C, the package body as the function that
       builds the UI. `codegen/platform/win32` writes only the entry point
       around it, per language.
     - Records for runtime instances and copies tables are done
       (`internal/lower/placerecords.go`, `design/nodes.md`): a record is a
       box struct in plain IR, and a component rendering several nodes or a
       slot at its top renders into the platform's
       `#[gen.protocol(contents)]`. Weak holds come with gtk4's move.
   - What the slice does not cover is refused by the check, per target,
     naming the component (`reportBodylessLibComponents`), as on every
     platform; no built-in node kind is left unwritten, a boundary included
     (`cmd/sngl/testdata/render_raise_reaches_its_boundary_runs.txt`), so
     `FirstUnimplementedNode` has nothing to list.
   - `sngl snapshot` is written (`codegen/platform/win32/snapshot.go`), behind
     `//go:build !js` with a `_js.go` stub, so the playground's wasm builds.
   - `LangForPlatform` is the platform's first language, from the registry.
     `cmd/sngl/consteval_output_test.go` checks a Go build's imports, which
     win32 joins with Part 4. The docs gallery (`docs/docs.go`'s
     `platformOrder`) shows win32 (step 7); gtk4 is not in it.
5. **Cross-compiling.** Done for C, Go with Part 4.
   - The platform says what it builds for, and the language's build reads it.
     win32's generator writes `target windows/<arch>` into C's `sngl.build`
     (`codegen/lang/c/compile.go`), and `c.buildProgram` picks MinGW's
     compiler for it (`ForTarget`), whatever `cc` says.
     `cmd/sngl/testdata/c_win32_build.txt` holds both architectures.
   - A program links as a GUI program: `-mwindows` is in the platform's
     `c.link`, so its test agent, which talks over TCP, links that way too.
   - MinGW names the binary `<name>.exe`, which `sngl build` reports.
   - `sngl run` runs it under `wine` (`c.launch`), checked by hand: a GUI
     program does not exit by itself, and `@run` is not written for win32.
   - **Go, with Part 4:** `go.buildModule` (`codegen/lang/golang/build.go`)
     runs the host's `go build`. It will read the same target and set
     `GOOS=windows`, `GOARCH`, `CGO_ENABLED=1`, `CC=<arch>-w64-mingw32-gcc`
     and `-ldflags=-H=windowsgui`. `internal/gencache/inputs.go` keys on
     those variables from the environment, so the values set for the build,
     not only the ambient ones, must be in the key.
6. **Tests: a launcher, records and goldens.** Done for C: the agent is
   cross-compiled for the program's `arch` and runs under Wine, connecting
   back to a loopback listener (`codegen/platform/win32/launcher.go`,
   `design/testing.md`), and a test's trigger is the platform's
   `#[gen.fires]` (`design/nodes.md`). Recorded for both architectures in
   `cmd/sngl/testdata/c_win32_test_runs.txt`.
   - **Changed from the plan:** the prefix draws with Wine's null driver, not
     X11 under cage. It needs no display, so `DISPLAY` is cleared and nothing
     reaches the developer's desktop, and with no window manager a window
     keeps the size the program gives it. Under cage, Xwayland kept a window
     at its creation size while Wine laid it out at the size `attach` asked
     for, which was the "box does not fill the window" seen in the first
     screenshots; the layout itself fills.
   - Snapshots are done: `PrintWindow` into a DIB section, sent as raw pixels
     the driver encodes as a PNG (`design/testing.md`).
   - `t.setContext` writes the program's `__ctx_<name>` var
     (`clang.SetContexts`), and win32 asks for `stdlibContextParam` as gtk4
     does (`cmd/sngl/testdata/c_set_context_runs.txt`).
   - A trigger of one copy of a node a loop renders (`c.row[1].click()`)
     and a null test of one are done, through the package's map from each
     copy's key (`cmd/sngl/testdata/c_win32_loops_runs.txt`).
   - A test reaching into what is built at run time -- a node a render
     slot or a record makes, `c.w.p.go.click()`, a node under a branch of
     the package body -- is done: the node is held in a package var of its
     option (`holdRead`), which a record sets as it makes it and lets go of
     as it is destroyed (`cmd/sngl/testdata/c_win32_test_reach_runs.txt`).
7. **Docs.** Done: `design/architecture.md`'s win32 bullet under *Platform
   Details*; `design/win32.md`, the generator, its metadata pin and the
   runtime's layout; CLAUDE.md's lists; and the docs site, which has no page
   per platform, names win32 and `c` where it lists targets
   (`docs/index.md`, `docs/learn/installation.md`, the tour) and shows win32
   in the gallery (`lib/snapshots`).
8. **After the slice.** Done.
   - Every `sngl:ui` component builds on win32 in C, held to
     `cmd/sngl/testdata/c_win32_controls_runs.txt`:
     - textarea (`ES_MULTILINE`), select (`CBS_DROPDOWNLIST`), progress and
       spinner (`PROGRESS_CLASS`, a marquee where indeterminate), and
       `time.timer` (`SetTimer` on the parked window);
     - grid, stack, splitview, card, drawer, popover and toolbar, as the
       box's layout modes (grid, flow, stack, equal shares), a frame and a
       click, with a style's `display` read; divider, link, toggle and
       radio;
     - tabs (`WC_TABCONTROL`), scroll, tooltip (`TOOLTIPS_CLASS`) and modal,
       a window of its own over the program's;
     - the window's menu bar (`CreateMenu`), which `ui.window`'s menu and a
       `ui.menubar` share, and `ui.menu` in place;
     - table (`WC_LISTVIEW`), tree (`WC_TREEVIEW`) and datepicker
       (`DATETIMEPICK_CLASS`);
     - image, file and remote (WIC, the program's carried files in
       `sngl_win32_assets.c`, `URLDownloadToCacheFile`).
   - A second window, `visible` and `@closed`: `window_dialog_runs.txt`. And
     `@run` (`testdata/win32_c_run_handler_runs.txtar`), which a golden's
     `exec` runs under Wine through `codegen.ProgramRunner`.
   - The canvas and its shapes: win32's `Canvas`, placed through its
     protocol, its drawing the handler of its `@paint` and each redraw its
     `#[gen.protocol(redraw)]` (`design/nodes.md`), painted by GDI+ through
     a context whose calls are cairo's (`testdata/win32_c_canvas.txtar`,
     `TestSnapshotCanvas`). gtk4 still draws its canvas from its generator
     (#294). Left on win32: `canvasImage` and `draw.image`, which are
     refused. Left on the other targets: `passCanvasReactivity` redraws a
     canvas under a branch, flattened into its render slot, only where it
     is painted through its protocol, since html's redraw named a local of
     the slot's render; gtk4 in C does not redraw one a write outside the
     slot changes.
   - `sngl:ui/markup`: a flow is a runtime window that wraps and draws the
     words of the spans it holds, each a window never shown, placed through
     the protocol (`testdata/win32_c_markup.txtar`, `TestSnapshotMarkup`).
     An inline image is its description, as on gtk4.
   - Follow-up: a `richText` in SNGL that draws on a canvas, the default for a
     platform with no rich text of its own. win32's C flow is its outline:
     resolve each span's look from its parents, break the words into pieces
     of one look, wrap them to the width, draw each line on one baseline at
     the advances measured, and keep each link's rectangles for a click. It
     wants three things of `sngl:ui/draw` first: measuring a run of text in a
     font, the canvas's width at run time with the height the drawing comes
     to handed back to layout, and where a click landed. win32 could then
     drop its flow for it.
   - Interpreter simulation: `--simulate win32` runs the platform's
     overrides and protocol over the interpreter's stubs for every
     `#[cnative]`, as gtk4's does (`cmd/sngl/testdata/simulate_win32.txt`).
     A string win32 is handed is a `c.utf16` the turn's arena holds, so the
     leak tracker has none of its strings to follow. What `--simulate` does
     not do on either target is a test's trigger an override relays from an
     event of its own (`@input` from an edit's change), a select's pick or a
     timer's tick.
   - A `winmd:` import scheme, if a program wants to declare a Win32 call
     the committed file does not hold. It is not needed for the platform
     itself.

## Part 4: Generated cgo wrappers

Cgo is slow to build: every package with `import "C"` runs cgo over its C
preamble, and a generated program that calls C directly pays that on every
change to the program. gtk4 already avoids it by hand: wrapped mode calls
gtk4rt, an unchanging Go package that holds the cgo, so its build is cached
and the program itself has no `import "C"` (`design/architecture.md`, *Which
file a program gets*). But gtk4rt's wrappers are written by hand and cover
what someone wrapped, and win32's Go path would need the same again.

### The design

- **A dev-time tool generates the wrappers.** It reads every C declaration
  in a platform package (each `#[macro.cnative]` function, and the types
  they cross) and writes a Go package with one wrapper per declaration, the
  cgo in it and none in the program. Beside each `cnative` mark it adds a
  `go.native` mark pointing at its wrapper.
- **The Go codegen sees both marks and writes one**, by a build option.
  **Decided:** gtk4's `cgo=true` becomes an enum, `direct`, `wrapper` or
  `purego`, defaulting to `wrapper`. `wrapper` falls back to `direct` for a
  declaration with no wrapper, so a declaration the tool cannot wrap still
  builds. `purego` calls C through
  [purego](https://github.com/ebitengine/purego), with no cgo at all.
- **Decided: where the wrappers go is a flag.** The tool takes the Go
  import path and the destination file on its command line, and detects
  both by default: the module from the nearest `go.mod`, and a path beside
  the platform package (as `lib/platform/win32/rt` is).
- **`go generate` keeps it current.** The wrapper package and the marks are
  committed, and a test holds them to the declarations, as win32gen's test
  holds `api.sngl`.
- **It needs more than one native mark on a declaration.** `ir.Foreign` (on
  `Func`, `Var` and types) holds one correspondence today. Make it a set
  keyed by language, which is useful on its own: one declaration can name
  its Go and its C spelling. A struct to embed wherever marks are allowed
  keeps the field in one place rather than once per declaration kind.
- **One tool for every C platform:** gtk4 (replacing gtk4rt's hand-written
  wrappers and the generic `SetString`/`ChildCall` calls where a declaration
  exists), win32, and any C platform added later.
- **Ship it as `sngl tool cgogen`**, so a user's own package of `cnative`
  declarations gets wrappers the same way.
- **Decided: win32's Go path is the tool's first user.** win32 does not
  call C directly from Go first and move over later; its Go path is built on
  the generated wrappers, and gtk4 moves onto them after.

### The steps

1. **`ir.Foreign` names one spelling per language.** Done. A second native mark
   of another scheme is kept in `Foreign.Also` rather than refused, and
   `Foreign.In(scheme)` finds either. The C spelling stays the primary,
   whichever mark comes first, since every C pass reads it there.
2. **The tool: `internal/cgogen`, run as `sngl tool cgogen`.** Done
   (`design/win32.md`), for a library package; a user's package of
   `cnative` declarations is still to come, and so is a wrapper for a `ref`
   parameter.
   - It loads a package, and wraps each `#[cnative]` function that is no
     cast, once per C name, in a Go function named for it
     (`sngl_win32_append` → `SnglWin32Append`). Each handle type and C
     struct the functions cross becomes an exported Go type in the same
     package: a handle is an alias of cgo's (`type HWND = C.HWND`), and a
     struct is the program's own Go struct.
   - **A wrapper's body is direct mode's call.** The tool makes a SNGL
     function for each wrapper whose body calls the declaration, runs
     `lower.CMarshal` over them, and writes the package with the Go
     language's own emitter. So a string, a struct, a `ref` and a callback
     cross exactly as they do in a direct call. A callback's dispatcher and
     its context table are written into the wrapper package, which answers
     the first open question below. Strings are converted inside the
     wrapper, which answers half of the second: a program in `wrapper`
     mode has no `import "C"`, so nothing converts them in the program.
   - The cgo preamble is the package's `c.link`: its flags, its include,
     and a `.c` file per source that includes it from where it is, since
     cgo compiles only the `.c` files in the package's own directory.
     Both are behind the platform's `GOOS` where it has one (`windows`).
   - It writes `#[go.native(path, name)]` beside each `cnative` mark in the
     package's `.sngl` files, and is kept current by `go generate`. A test
     regenerates both and compares them with what is committed, and builds
     the package with MinGW for both architectures.
3. **The Go language calls a wrapper.** Done: Go reads a declaration's Go
   spelling (`Foreign.In("go")`) wherever it reads its `Foreign`, and
   `passCMarshal` and the splices after it are told the build's language
   and leave a wrapped call alone (`design/win32.md`, *Go calls the
   wrapper*; `testdata/c_go_wrapper_call.txtar`). A `ref` parameter has no
   wrapper yet: its call's record of results is the program package's.
4. **A placed program in Go** (`golang.WriteProgram`). Done.
5. **win32 in Go.** Done, and Go is win32's default language:
   `SupportedLangs`, the entry and `@run`'s, the cross-compile, carried
   files through a call, `async.do`, `sngl snapshot`, and `sngl test`, whose
   agent writes the placed program's tests as C's `WriteTests` does
   (`design/win32.md`, *win32 in Go*). Every win32 script runs its programs
   in both languages, and Go's snapshots are held to C's.
6. **gtk4 onto the wrappers** moved to Part 5. Generating gtk4rt's wrappers
   would replace only its thin calls (`New`, the generic sets, `ChildCall`,
   `Connect`), which already reach every GIR class with no cgo in the
   program. Most of gtk4rt is a runtime with nothing to generate: the look
   and flex layout, scroll, tabs, menus, images, toplevels and the loop.
   gtk4 gets the wrappers once that runtime is SNGL over `cnative`
   declarations, as win32's is.

### Open questions

- What a wrapper costs per call against direct cgo, which is one cgo call
  either way.
- What `purego` cannot reach: a struct passed by value, a callback's
  context, and a macro (`GetWindowLongPtrW` on 386) each need checking
  against its limits.

## Part 5: gtk4's runtime in SNGL, and three ways to call C

gtk4 has two runtimes that do one job. `pkg/go/gtk4rt` is Go (and C) for a
Go program, and `lib/platform/gtk4/rt` is C for a C program, which
`c_emit.go`'s `native` table maps each gtk4rt call onto. win32 has one: its
protocol is SNGL calling `cnative` declarations (`sngl:platform/win32/api`
and the runtime's C), and each language calls those as it calls C. gtk4
moves to that model, and then every language and every way of calling C
reaches it the same way.

### The goal

- **The runtime is SNGL over `cnative` GTK.** What gtk4rt and `rt/` do -- the
  look and the flex layout, scroll, tabs, menus and the way back, images,
  toplevels and the application, keys, posting -- becomes `gtk4.sngl`'s
  protocol functions calling GTK's own functions. What GTK can reach only as
  C stays C in `rt/`, as small as it can be: the layout manager's `measure`
  and `allocate` vfuncs (a `GtkLayoutManager` subclass), a signal trampoline
  of a shape a callback cannot take, and whatever else needs a GObject
  subclass.
- **The stdlib's GTK declarations are committed.** They are generated once
  from the bundled GIR into a tracked file beside `gtk4.sngl`, in the same
  package, so a program writes `gtk4.GtkButton` as it writes `html.button`,
  and `gtk4.sngl` no longer imports `gir:Gtk-4.0`. A program can still
  import `gir:`; a class it reaches there that has no wrapper is called
  directly.
- **gtk4 moves onto placement** (`#[gen.wants(placement)]`), as win32 is, so
  Go writes the program with `golang.WriteProgram` and C with its own, and
  `compiler_ir.go`, `c_emit.go`, `c_record.go`, `wrapped.go` and the
  inline-cgo scaffold retire. Weak holds come with it (Part 3, step 4).
- **`cgo` becomes an enum: `direct`, `wrapper` or `purego`, defaulting to
  `wrapper`.** `direct` calls C from the program. `wrapper` calls the
  package `sngl tool cgogen` writes (Part 4), and falls back to `direct` for
  a declaration with no wrapper. `purego` calls `libgtk-4.so` through
  [purego](https://github.com/ebitengine/purego) with no cgo at all.
- **`pkg/go/gtk4rt` is deleted**, and its tests become tests of the runtime
  through a program (`cmd/sngl/testdata`, the gtk4 records), in both
  languages.
- **A C record gives back what it kept when it is destroyed.** A component
  built at run time -- a row a keyed `for` keeps -- is a record, and in C its
  destroy runs the teardown and gives back nothing else: not its vars' and
  props' kept values, not the boxes and closure environments it made, and,
  on win32, not the record itself. Every row a list rebuilds leaks.
  `c_record.go` (gtk4) and placement's records (win32) both do this today,
  against `design/ownership.md`. The fix lands in placement, for both, once
  gtk4 is on it: the record's destroy drops each field it kept and the box,
  and a test in each language holds the kept set's size across a rebuild.

### The steps

1. **gtk4 moves onto placement, with its widgets in its own package and its
   runtime in SNGL.** One step, since the widget declarations can be
   committed in only one form: the one calling GTK's C, which C needs. In the
   old generator that puts cgo in every Go program; placement's
   `golang.WriteProgram` calls cgogen's wrappers instead, as win32's Go
   does, so a Go program keeps no `import "C"` of its own.
   - **Built beside gtk4, then swapped in.** The new platform is
     `sngl:platform/gtk4next` (`codegen/platform/gtk4next`) while it grows,
     so every slice is tested and gtk4 keeps working. Once it does all gtk4
     does it is renamed `gtk4`, and the old generator, `compiler_ir.go`,
     `c_emit.go`, `c_record.go`, `wrapped.go` and the inline-cgo scaffold
     are deleted.
   - **The widgets.** `internal/cmd/gtk4gen` writes `widgets.sngl` beside
     the platform's own SNGL from the bundled GIR (`gtk4.WidgetSource`), and
     `go generate ./lib` runs it before cgogen. A class keeps its C name
     (`gtk4.GtkButton`), as an HTML element keeps its tag's. A program's
     `gir:Gtk-4.0` import is unchanged.
   - **The runtime** is `rt/`'s C, already there for gtk4's C programs,
     which the protocol calls through `cnative` as win32's calls
     `sngl_win32_*`. What gtk4rt does in Go and `rt/` does not, moves to
     `rt/` or to SNGL. What stays C is the layout manager's `measure` and
     `allocate`, the trampolines a callback cannot take, and any GObject
     subclass.
   - **Slices**, each in both languages with a script test and snapshots:
     1. A window, a box, a label and a button whose click counts. Done:
        `cmd/sngl/testdata/gtk4next_test_runs.txt`, in both languages,
        its snapshots held to gtk4's C. The runtime is a copy of `rt/` in
        `lib/platform/gtk4next/rt` until the swap; what it gained:
        `sngl_gtk_main`, `sngl_gtk_init` and `sngl_gtk_step` for Go's
        entry and agent, `sngl_signal_connect`/`sngl_notify_connect`/
        `sngl_signal_emit` (a handler crosses as `SnglFn` and its context,
        and a test fires a signal), the scaffold's `sngl_set_prop_*`,
        `sngl_gtk_carry`/`sngl_gtk_carry_file` in place of an extern asset
        table, `sngl_gtk_capture_file` for Go's snapshot, a node attached
        that is no toplevel shown in one of its own, and `int` for a bool
        it is handed, as SNGL's C hands one. Along the way: `c.link` takes
        `pkgConfig`; cgogen names a struct with a leading underscore as an
        exported Go type (`_p_GtkWidget` → `PGtkWidget`); and a cast to a
        wrapped type converts to the wrapper's type in Go unless its operand
        is a node or that type already. The placed widgets
        (`gtk4.WidgetSource`) skip a class made with another object
        (GtkApplicationWindow), and an action is a method casting its
        receiver.
     2. The look and the layout (`style.sngl`), and every leaf widget.
     3. Toplevels, several windows, `@run` and the application's teardown.
     4. Scroll, tabs, menus and the way back, keys.
     5. Images and the canvas, with its draw function (#294).
     6. Async posting, timers, `sngl test`'s agent and `sngl snapshot`.
     7. The swap: every gtk4 golden and recorded run on the new platform,
        weak holds, and the old generator deleted.
2. **A C record gives back what it kept**, in placement, for gtk4 and win32
   alike, with a test in each language that a rebuilt list's kept set does
   not grow. With it, a retained C closure that writes a captured local
   writes a heap box, not the stack address it writes today.
3. **`cgo` becomes `direct`, `wrapper` or `purego`.** `direct` and `wrapper`
   come with step 1. `purego` is attempted then, against the open questions
   below.
4. **`pkg/go/gtk4rt` is deleted**, its tests moved to programs in both
   languages.

### Open questions

- Which parts of the runtime must stay C, and how `purego` reaches them:
  through `purego.NewCallback`, through a shared library built from `rt/`, or
  not at all. A GObject subclass through purego is the hardest case.
- Whether the canvas's draw function (#294) lands first, since it is the
  last of gtk4's Go-only callbacks.
- What a program's build costs in each mode, measured as Part 3's table
  measured wrapped against cgo.
