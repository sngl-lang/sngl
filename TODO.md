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

- How much of gtk4rt moves to C in this part. The trampolines must, since
  item 5 replaces them, and so must the layout manager, which is C already.
  The rest (toplevels, images, menus, tabs) is ordinary GTK calls a SNGL
  protocol could make itself once callbacks and structs cross. Decide whether
  that is Part 1's work or gtk4's own migration.

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
   file's. A trigger emits the signal its handler is connected to, so one
   carrying a value or a property's notification is refused, and so are
   `t.snapshot`, `t.wait` and `t.setContext`.
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

- Whether the arena resets per turn on every platform's loop, or only where
  the platform's runtime says a turn ended. GTK's main loop and Win32's
  message loop both have a natural point: on gtk4, each callback the program
  is handed resets it as it returns to the loop.

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
2. **The C runtime: `lib/platform/win32/rt/`.** C sources and a header,
   reached by the platform's `c.link(include="windows.h", system=true, library=…, sources=…)` with `-luser32 -lgdi32 -lcomctl32`.
   - One registered window class whose `WNDPROC` finds the control's
     handlers through `GWLP_USERDATA` and calls them through Part 1's
     callbacks: `WM_COMMAND` with `BN_CLICKED`, and `EN_CHANGE`.
   - The message loop as `run` and `quit`, and posting work to the loop for
     `asyncPost` (`PostMessageW` of a private `WM_APP` message, as gtk4 posts
     an idle callback). Go's half of the post, from a goroutine, is the Go
     language's own (Part 1, item 7).
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
   - **Common Controls v6 and DPI.** Themed controls need an activation
     context from a manifest. Either link a resource compiled with `windres`
     from a committed `.rc`, or call `CreateActCtxW` at start-up with the
     manifest embedded as a C string. The second needs no resource step in
     any language's build. Declare per-monitor DPI awareness there too, and
     check what Wine draws with and without it.
   - Snapshot: `PrintWindow` into a DIB section. The PNG encoding is the
     language's: Go's `image/png` for the Go agent, and whatever the C test
     agent uses for C.
3. **`lib/platform/win32`.**
   - `win32.sngl`: the platform node.
     - It takes `#[gen.can(asyncPost, instanceState)]` and
       `#[gen.cannot(reactivity, declarative, inlineComponents, structSpread)]`, as gtk4 and fyne do, since Win32 also owns its controls
       and diffs nothing.
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
4. **`codegen/platform/win32`.**
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
     - Records for runtime instances, weak holds and copies tables come
       after the slice, as the protocol ops gtk4's move needs.
   - `FirstUnimplementedNode` refuses what the slice does not cover yet,
     so `sngl test` reports a skip rather than a compile error.
   - Everything snapshot-specific is behind `//go:build !js` with a `_js.go`
     stub, so the playground's wasm builds.
   - Edit the hard-coded platform switches: `internal/snapshot/snapshot.go:467`
     (`LangForPlatform`), `docs/docs.go:439` and
     `cmd/sngl/consteval_output_test.go:64`.
5. **Cross-compiling.** Nothing sets a target OS for a generated program
   today. `go.buildModule` (`codegen/lang/golang/build.go`) runs the host's
   `go build`, and `cmd/sngl/build.go:168` (`isWindowsTarget`) reads a `goos`
   option no platform sets.
   - The platform says what it builds for (Windows, `arch`), and each
     language turns that into its own build:
     - **Go:** `GOOS=windows`, `GOARCH`, `CGO_ENABLED=1` and `CC=<arch>-w64-mingw32-gcc`.
     - **C:** the MinGW compiler directly.
   - **Decided:** a build option on the platform node names the target, and
     the language's build reads it. It defaults to the host, and win32, which
     targets only Windows, cross-compiles on Linux. `sngl build` writes an
     `.exe`, and `sngl run` on Linux runs it under `wine` without being asked.
   - A program links as a GUI program (`-H=windowsgui` for Go, `-mwindows`
     for C), its test agent too, which talks over TCP.
   - The binary is `<name>.exe` (`binaryName`).
   - `sngl run` runs it under `wine`.
   - `internal/gencache/inputs.go:533` keys on `GOOS`, `GOARCH`,
     `CGO_ENABLED` and `CC` from the environment. Make sure the values
     set for the build, not only the ambient ones, are in the key.
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
   - Still open:
     - Snapshots: the C agent answers no `t.snapshot` on any platform yet.
       Under the null driver, `PrintWindow` into a DIB section captures a
       window at a size the test fixes.
     - A trigger of one copy of a node a loop renders (`c.row[1].click()`)
       is refused by passPlacement.
7. **Docs.**
   - `design/architecture.md`: a win32 bullet under *Platform Details*, with
     its C runtime and cross-compilation.
   - `design/win32.md` (or a section in `design/architecture.md`):
     the generator, the metadata pin, and the layout engine.
   - CLAUDE.md's `codegen/platform/{…}` list.
   - The platform's page in the docs site (`go tool docsgen`).
8. **After the slice.**
   - The rest of gtk4's override set:
     - `ui.image` (GDI+ or WIC through the runtime)
     - `ui.scroll` (a scrolling container window)
     - `ui.tabs` (`WC_TABCONTROL`)
     - `ui.progress` (`PROGRESS_CLASS`)
     - `ui.select` (`COMBOBOX`)
     - `ui.textarea` (`ES_MULTILINE`)
     - `ui.menubar` and `ui.menu` (`CreateMenu` on the window, which is
       what `ui.window`'s menu slot wants)
     - `ui.table` and `ui.tree` (`WC_LISTVIEW`, `WC_TREEVIEW`)
     - `ui.modal` (a modal owned window)
     - `ui.datepicker` (`DATETIMEPICK_CLASS`)
     - `ui.tooltip` (`TOOLTIPS_CLASS`)
     - `time.timer` (`SetTimer`)
   - Shapes and the canvas over GDI, as `api.sngl` calls the way gtk4 draws
     them over cairo.
   - A second window, `@closed`, `visible` and `@run`, per
     `design/windows.md`.
   - Interpreter simulation (`--simulate win32`), if the protocol functions
     run there as gtk4's do.
   - A `winmd:` import scheme, if a program wants to declare a Win32 call the
     committed file does not hold. It is not needed for the platform itself.

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

### Open questions

- How a callback (`#[c.context]`, `#[c.release]`) crosses a wrapper: gtk4rt
  registers Go handlers through `cbind` today, which the generator would
  have to write.
- What a wrapper costs per call against direct cgo, which is one cgo call
  either way, and whether strings should be converted inside the wrapper
  (as gtk4rt does) rather than by `CMarshal` in the program.
- What `purego` cannot reach: a struct passed by value, a callback's
  context, and a macro (`GetWindowLongPtrW` on 386) each need checking
  against its limits.
