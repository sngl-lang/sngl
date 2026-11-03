# TODO

## A win32 platform

`sngl:platform/win32` is a target for classic Windows GUI programs: windows,
controls and drawing through user32, gdi32 and comctl32, the API Windows
programs have been written against since Windows 95. It mirrors gtk4 in shape.
The platform's SNGL declares the C functions it calls and wraps them in the
stdlib's components, each widget's create, append, remove, set and event are
`#[gen.protocol]` functions, and a Go runtime package (`pkg/go/win32rt`, as
`gtk4rt` is) holds what no declaration can say: the window procedure, the
message loop and the calls that take a struct.

Programs are Go cross-compiled with cgo against MinGW, and run under Wine
for tests and `sngl run`.

### What was verified on this host

None of this is in the repository yet; each was run by hand on 2026-11-03.

- `mingw-w64-gcc` and `wine` (11.19) are Arch packages in `extra`, and
  `[multilib]` is not needed: Arch's `wine` is a WoW64 build and runs a 32-bit
  `.exe` without it.
- `CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -ldflags=-H=windowsgui` builds a GUI `.exe`, and `GOARCH=386 CC=i686-w64-mingw32-gcc` a 32-bit one. Both ran under Wine. In both, a
  `WNDPROC` that is a static C function calling an `//export`ed Go function
  received the messages, and the Go-side `GetMessage` loop ran and exited.
  Neither needed MinGW's runtime DLLs next to it.
- cgo spells Win32's typedefs as aliases of the type they name:
  `(*C.wchar_t)(p)`, `(*C.WCHAR)(p)` and `C.LPCWSTR(p)` are all accepted by
  `SetWindowTextW`. A `UINT`, `WPARAM` or `LPARAM` parameter refuses a
  `C.int`, which is what `CMarshal` writes for an `int` today (see step 1).
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
- **The C boundary moves into the IR first** (step 1). A `#[cnative]` call
  today takes a `string`, `int`, `float`, `bool` or an opaque handle, so most
  Win32 calls cannot be declared. `CMarshal` learns typed C scalars and UTF-16
  strings. Structs, out-parameters and callbacks stay in win32rt, as GTK's
  trampolines do in gtk4rt.
- **Both architectures; amd64 is the default.** An `arch` option on the
  platform node takes `amd64` or `386`.
- **The first milestone is a vertical slice** (steps 1–9): `ui.window`,
  `ui.text`, `ui.button`, `ui.input`, `ui.checkbox`, `ui.vbox`, `ui.hbox` and
  `ui.spacer`, events, style basics, goldens, and a Wine-under-cage test
  launcher with snapshots. The rest of gtk4's overrides come after (step 10).

### The steps

1. **Typed C scalars: a `#[cnative]` alias.**
   `#[cnative("C.UINT")] struct UINT = int` names a C typedef SNGL spells as
   an `int`. A caller passes an `int`, since an alias is what it names (spec,
   *Aliases*). A C function's parameter written with the alias crosses as
   `C.UINT(x)` instead of `C.int(x)`.
   - `markCNative` (`internal/checker/marks_impl.go:296`) accepts an
     `*ir.Alias` of `int`, `float`, `bool` or `string`, and records the C type
     on a new `ir.Alias.Foreign`. It refuses an alias of anything else, with
     a fixture.
   - The checker expands an alias where it is written
     (`internal/checker/aliases.go`, `expandAlias`), so nothing after the
     check meets one. The parameter has to remember it: `buildParams`
     (`internal/checker/resolve.go:800`) sets a new `ir.Param.CAlias *ir.Alias`
     where the written type is a `NamedType` naming an alias. It records a
     pointer, not the C name: marks run at the end of pass 1 (`afterMarks`),
     possibly after the parameter is built, and `CMarshal` reads
     `CAlias.Foreign` at lowering, when every mark has run. Check that
     `ir.ClonePackage` shares the alias rather than copying it, and that the
     dump writes it as a reference (`cmd/sngl/dumpomit.go`).
   - `CMarshal` (`internal/lower/c_marshal.go`): where a parameter's
     `CAlias` names a C type, a number converts through a cast to it. The cast is
     a `Foreign{Scheme:"c", Path:"C", Name:<the type>}` call, as gtk4's
     `toLineCap` is, which the Go renderer writes as `C.UINT(x)`
     (`codegen/lang/golang/ircontext.go`, `nativeCall`). A bool converts as
     `C.BOOL(b ? 1 : 0)`, which `NoTernary` lowers as it lowers `cbool`. A
     return typed with the alias is an `int` already, and the renderer's `int(…)`
     wrap covers it.
   - Fixtures first: `testdata/c_alias_argument.txtar` (a `UINT` argument is
     `C.UINT(…)`; deny `C.int(`), `testdata/error_cnative_alias.sngl` (an alias
     of a struct or a list is refused). Revert the `CMarshal` change and confirm
     the golden fails.
   - Document the alias form in `lib/macro/foreign.sngl`'s `cnative` doc, and
     the rule beside the `#[cnative]` bullet in `design/nodes.md`.
2. **UTF-16 strings in `sngl:x/c`.** A `W` function wants UTF-16, and its `A`
   twin reads the system code page, not UTF-8, so a `char *` is wrong for
   any non-ASCII text.
   - `sngl:x/c` declares `#[cnative("*C.wchar_t")] struct wstr {}`, an alias
     `#[cnative("*C.wchar_t")] struct utf16 = string` for a parameter to be
     written with, and the conversion
     `#[marks.builtin("cWString")] const func cwstring(s string) wstr => _wide(_utf16(s))`. Here `_utf16` is a `#[go.native]` of a new
     `cbind.UTF16(s string) unsafe.Pointer` (`pkg/go/cbind`, which has no
     cgo), and `_wide` is `#[cnative("(*C.wchar_t)")]`. That makes `sngl:x/c`
     import `sngl:language/go`. It already spells cgo's `C.CString` and
     `unsafe.Pointer`, so this adds no new dependency in kind; the C target
     (`design/ownership.md`, *A C language target*) would answer it
     differently.
   - The string lives in Go memory, so it is not `#[owned]` and `ReleaseTemps`
     frees nothing. cgo permits a pointer to Go memory holding no Go pointers
     as an argument for the length of the call. Win32 copies every string
     the slice hands it (window text, class names, control text).
   - `CMarshal`: a `string` parameter whose `CAlias` names a C type crosses
     through the `sngl:x/c` conversion whose result is that C type:
     `cstring` for `*C.char`, `cwstring` for `*C.wchar_t`. One that no
     conversion makes is a lowering error naming the type.
   - `option[c.utf16]` crosses as the string or NULL, as `optionString` does
     for `option[string]`. `CreateWindowExW`'s window name and many `W`
     parameters are optional.
   - Fixture: `testdata/c_utf16_argument.txtar`. Add a CLI script that builds
     and runs a `GOOS=windows` program once step 7 exists.
3. **The generator: `internal/cmd/win32gen` → committed SNGL.**
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
   - What it writes:
     - **Handle types** as opaque `#[cnative("C.HWND")] struct HWND {}`
       (`HWND`, `HMENU`, `HINSTANCE`, `HFONT`, `HBRUSH`, `HDC`, `HICON`,
       `HCURSOR`).
     - **Scalar typedefs** as step 1's aliases (`UINT`, `DWORD`, `WPARAM`,
       `LPARAM`, `LRESULT`, `BOOL`, `COLORREF`, `ATOM`).
     - **Constants** as SNGL `const`s (`WS_*`, `WS_EX_*`, `WM_*`, `BS_*`,
       `BM_*`, `BST_*`, `ES_*`, `EN_*`, `SS_*`, `SW_*`, `SWP_*`, `COLOR_*`,
       `IDC_*`, `GWLP_*`).
     - **Functions** whose every parameter is a scalar, a handle or a `W`
       string (`c.utf16`), with each optional pointer as an `option`.
   - It skips a function taking a struct, an out-pointer or a callback, and
     reports each in a comment block at the end of the file. win32rt
     wraps the ones the platform needs.
   - Always the `W` entry point, never the `A`, and the name without the
     suffix where the metadata gives both.
   - Find out how win32json spells a 32-bit/64-bit difference
     (`GetWindowLongPtrW` is a macro over `GetWindowLongW` on 386). Generate
     the declarations both architectures can call, or keep that call in
     win32rt.
4. **`pkg/go/win32rt`.** cgo over `windows.h`, linked with `-luser32 -lgdi32 -lcomctl32`, and the half of the platform no declaration can
   say.
   - One registered window class whose `WNDPROC` is a static C function
     calling an `//export`ed Go dispatcher. Events reach handlers through
     `cbind.Register`, indexed by the control's `HWND` (`GWLP_USERDATA`, or a
     Go map) and the notification (`WM_COMMAND` with `BN_CLICKED`,
     `EN_CHANGE`).
   - The message loop as `Run`, `Quit`, and a posted-work queue for
     `asyncPost`: `PostMessageW` of a private `WM_APP` message, as gtk4 posts
     an idle callback.
   - **Layout.** Win32 has no layout manager. A child control sits at
     the pixel position it is given, and nothing moves it when its parent is
     resized. win32rt keeps a box tree beside the controls: a vbox or hbox is
     a container window or a record. It measures each child
     (`GetTextExtentPoint32W` for text and buttons, fixed metrics for an
     entry and a checkbox), lays out by `flex` on `WM_SIZE`, and moves
     children with `DeferWindowPos`. This is the largest piece of win32rt,
     and the measure → allocate shape of gtk4rt's `sngl_flex_measure` and
     `sngl_flex_allocate` (`pkg/go/gtk4rt/look.go`) is the model.
   - **Fonts.** A control created bare draws in the System font. Every
     control gets `WM_SETFONT` with the message font from
     `SystemParametersInfoW(SPI_GETNONCLIENTMETRICS)`.
   - **Common Controls v6 and DPI.** Themed controls need an activation
     context from a manifest. Link a `.syso` resource (made by
     `x86_64-w64-mingw32-windres` or `i686-w64-mingw32-windres` from a
     committed `.rc`) into the program, or call `CreateActCtxW` at start-up
     with an embedded manifest. Decide which keeps `go build` to a
     single command. Declare per-monitor DPI awareness there too. Check
     what Wine draws with and without it.
   - Snapshot: `PrintWindow` into a DIB section, encoded with `image/png`.
     Wine's X11 driver under cage supports it, as verified above.
   - Tests in the package run under cage through
     `testutil.MaybeReexecUnderCage`, as gtk4rt's do. They are cross-built
     and run under Wine, so the package's `go test` needs a launcher of its
     own (see step 8). Its runs are recorded like gtk4rt's
     (`internal/cmd/verify/testrun.go`).
5. **`lib/platform/win32`.**
   - `win32.sngl`: the platform node.
     - It takes `#[gen.can(asyncPost, instanceState)]` and
       `#[gen.cannot(reactivity, declarative, inlineComponents, structSpread)]`, as gtk4 and fyne do, since Win32 also owns its controls
       and diffs nothing.
     - Options: `package`, `main`, `@run`, `arch` (`"amd64"` default,
       `"386"`), and `reservedNames`.
     - It imports `sngl:x/c` and declares one `c.link(include="windows.h", system=true, ldflags=["-luser32", "-lgdi32", "-lcomctl32"])`.
   - The primitives (`#[marks.intrinsic("win32:Toplevel")]`, `Button`,
     `Static`, `Edit`, `CheckBox`, `Box`) and their protocol functions. Each
     create calls `CreateWindowExW` with the class name and style bits from
     `api.sngl`, through a win32rt call that parents and registers it. Each
     set is a `SetWindowTextW` or `SendMessageW` from `api.sngl`, and each
     event an `on` through win32rt.
   - The overrides for the slice: `ui.window`, `ui.text`, `ui.button`,
     `ui.input`, `ui.checkbox`, `ui.vbox`, `ui.hbox`, `ui.spacer`.
   - `style.sngl`: `lookOf` and `const unstyled`. `flex`, `gap`,
     `padding` and `minWidth`/`minHeight` reach win32rt's layout, and
     `color`, `background` and `fontSize` reach `WM_CTLCOLOR*` and a created
     font. Everything else goes in `unstyled` with a reason.
     `lib/style_coverage_test.go` holds it to every field.
   - `doc.sngl`, the package doc.
6. **`codegen/platform/win32`.**
   - Registered with `codegen.RegisterNative("win32", …)` and blank-imported
     in `codegen/platform/platforms.go`.
   - It is gtk4's wrapped mode with GIR, declgen, the cgo mode, canvas and
     markup taken out. It builds a MutationModel. Every op is a declared
     protocol, so the translator only places nodes: model fields,
     the application's attach (`toplevel.go`), render slots and instances
     (`instance.go`). Start from fyne's file layout, which carries no cgo
     mode, and gtk4's `toplevel.go`, `instance.go` and `wrapped.go` for the
     node handling.
   - `FirstUnimplementedNode` refuses what the slice does not cover yet,
     so `sngl test` reports a skip rather than a Go compile error.
   - Everything cgo- or snapshot-specific is behind `//go:build !js` with a
     `_js.go` stub, so the playground's wasm builds.
   - Edit the hard-coded platform switches: `internal/snapshot/snapshot.go:467`
     (`LangForPlatform`), `docs/docs.go:439` and
     `cmd/sngl/consteval_output_test.go:64`.
7. **Cross-compiling generated programs.** Nothing sets GOOS for a
   generated program today. `go.buildModule`
   (`codegen/lang/golang/build.go`) runs the host's `go build`, and
   `cmd/sngl/build.go:168` (`isWindowsTarget`) reads a `goos` option no
   platform sets.
   - The platform says what it builds for. Either the win32 node's options
     reach `go.buildModule` as `GOOS=windows`, `GOARCH` from `arch`,
     `CGO_ENABLED=1` and `CC` (the MinGW compiler for the arch), or the node
     writes its own `gen.build`/`gen.run` over a new intrinsic. Decide which
     after reading `lib/x/gen/command.sngl` and how `sngl:language/go` writes
     its own.
   - `-ldflags=-H=windowsgui` for a program; not for a test agent, which
     talks over stdin and stdout (step 8).
   - The binary is `<name>.exe` (`binaryName`).
   - `sngl run` runs it under `wine`.
   - `internal/gencache/inputs.go:533` already keys on `GOOS`, `GOARCH`,
     `CGO_ENABLED` and `CC` from the environment. Make sure the values
     set for the build, not only the ambient ones, are in the key.
8. **Tests: a launcher, records and goldens.**
   - `codegen.TestLauncher` on the generator (see
     `codegen/platform/android/launcher.go` and `html/launcher.go`). It
     cross-builds the agent and starts `wine agent.exe` with stdin and
     stdout piped as the `RPCChannel` `pkg/go/testagent` speaks. It reports
     `codegen.SkipError` where `wine`, the MinGW compiler or cage is
     missing.
   - **Open question:** whether a console-subsystem `.exe` under Wine passes
     Unix pipes through to its standard handles cleanly enough for the
     JSON-RPC. If not, fall back to a TCP port, as android does over
     `adb forward`.
   - Wine setup:
     - A `WINEPREFIX` per test cache directory, made once with
       `wineboot -i` outside cage and reused, since one is slow to make.
     - `WINEDEBUG=-all`.
     - `Graphics=x11`, with `DISPLAY` set to cage's own. `headless.Env`
       leaves the developer's `DISPLAY` in place today, so an X11 client in
       cage could reach the real desktop. Clear it, or check that cage always
       overrides it.
   - The snapshot is win32rt's `PrintWindow` capture at a fixed client size.
     cage maximizes the window, so pick the size with `SetWindowPos` after
     the window is mapped, or run Wine's virtual desktop
     (`explorer /desktop=sngl,800x600`).
   - `internal/toolchain/toolchain.go`:
     - a `win32` case in `Unavailable` (go, `x86_64-w64-mingw32-gcc` or
       `i686-…`, `wine`)
     - `PresentsWindows` true for win32
     - win32 in `Build`'s Go group and in `Exec`
     - Wine's failure strings in `SkipReason`
   - The `Dockerfile` installs `wine`, `mingw-w64` and `xwayland` beside
     cage. Check that Debian's cage starts Xwayland.
   - Goldens `testdata/win32_*.txtar`: the generated Go for each slice
     component, with a recorded run under `run/go/win32` and
     `testrun/win32/go/…`.
   - CLI scripts `cmd/sngl/testdata/win32_*.txt`, behind a `[win32]`
     condition in `cmd/sngl/script_test.go`: build, run, and a click that
     writes state, read back from a snapshot.
   - Confirm each new fixture fails with its behavior reverted.
9. **Docs.**
   - `design/architecture.md`: a win32 bullet under *Platform Details*, with
     its runtime package and cross-compilation.
   - `design/win32.md` (or a section in `design/architecture.md`):
     the generator, the metadata pin, and the layout engine.
   - CLAUDE.md's `codegen/platform/{…}` list.
   - The platform's page in the docs site (`go tool docsgen`).
10. **After the slice.**
    - The rest of gtk4's override set:
      - `ui.image` (GDI+ or WIC through win32rt)
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
    - Shapes and the canvas over GDI: `#[cnative]` calls from `api.sngl`, as
      gtk4 draws them over cairo, or `#[gen.wants(canvas)]` as fyne does.
    - A second window, `@closed`, `visible` and `@run`, per
      `design/windows.md`.
    - Interpreter simulation (`--simulate win32`), if the protocol functions
      run there as gtk4's do.
    - A `winmd:` import scheme, if a program wants to declare a Win32 call the
      committed file does not hold. It is not needed for the platform itself.

### Open questions

- Should `GOARCH=386` stay a supported default-off option once `amd64` works,
  or be tested in CI too? Wine runs both on this host, and CI's Debian image
  needs `wine32` for 386.
- The common-controls manifest: a linked resource or an activation context
  at start-up (step 4).
- The cross-compile hook: platform options read by `go.buildModule` or a
  platform-written `gen.build` (step 7).
- Pipes or TCP for the test agent under Wine (step 8).
