# Ownership gaps

One MR, `ownership-gaps`, for what the spec's *Owned values* lists as held
rather than released, which a C target cannot leave held. The branch starts
from `ownership`'s last commit; once !274 is on `main`, merge `origin/main`
(never rebase) so the MR targets `main` with nothing under it. Delete this
file before the MR leaves draft.

The ownership work (#110, !274) made every lifetime a static one: statement,
node, application, moved. Its review left a list of values the compiler
holds instead, and each is a lowering gap, not a language limit. Closing
them is the groundwork a C target needs, where `string` becomes owned and a
held value is a leak per frame. No runtime reference counting: the only
counting worth having is the host's, where the host already counts.

## The design

`design/ownership.md` holds it, and the spec's *Owned values* what it means.
Change those, not this file.

## Rules for every part

- Fixture first (`sngl fmt` writes it), and confirm it fails with the change
  reverted. Goldens assert generated code; `deny` asserts absence. The
  interpreter's tracker is the leak check: a fixture under `sngl test`, and
  `--simulate gtk4` where a native is involved.
- `go tool verify` passes at the end of each part, in a clean worktree
  (`git worktree add --detach`; the main checkout's `tmp/sharecost` breaks
  it; `export ANDROID_HOME=$HOME/Android/Sdk`). After any codegen change,
  `go tool verify -update` rerecords goldens and recorded runs; read each
  golden diff for behavior, not only hashes.
- Change the spec with the meaning, and the `design/` note with the
  mechanism, in the part that makes the change. Each part shortens the
  spec's held list; the list is empty when the MR is done.
- Subagents get the comment rule from `~/.claude/CLAUDE.md` pasted into their
  prompt.
- At the end of each part: commit, push, update the MR description, tick the
  part here.
- When a task is done -- a part, or any item asked for on its own -- end the
  final message with the prompt for the next session: the next open item or
  part, what to read first, and how to confirm it, ready to paste.

## Part 1: containers and structs own what they hold

The biggest hole, and where a C target bleeds first. A value in a list, a
map, an `option` or a struct field is held today, and the program releases
it by hand when it takes it out, which the spec says and nobody does.

- [X] A type that holds an owned type is itself owned, by composition: a
  struct with an owned field, `list[T]`, `map[K, T]` and `option[T]` for an
  owned `T`. `ir.ReleaseOf` answers for each, with a release the pass
  synthesizes (`__release_<type>`) that releases what the value holds, newest
  first; a struct's release is written once per struct, a collection's once
  per element type.
- [X] A collection write moves the value in: `push`, `set`, an index
  assignment and a field assignment release what they replace, and `remove`,
  `pop` and `clear` release what they take out unless the result is stored.
  This is what the spec already says of `push` ("held with the list"), made
  true for the rest.
- [X] A var of a composed type is an owned var: `OwnedState` releases it on
  overwrite and at unmount, `ReleaseTemps` releases a temp of one after the
  statement, and the application lifetime applies to a literal of one built
  from constants.
- [X] The interpreter's handle follows the value through the container: a
  copy of a list holds the same handles, and releasing the list marks them.
  `sngl:ui`'s image sources and gtk4's pictures are the first library use to
  check under `--simulate gtk4`.
- [X] Spec: strike "a value held in a list, a map, a struct field or an
  option" from the held list, and say what a collection write does with an
  owned value.
- [X] Found on the way, fix here: the interpreter refuses an assignment to an
  imported package's var ("cannot assign to undefined variable"), which
  predates ownership and keeps `owned_imported_var` a golden rather than a
  fixture under `sngl test`.

## Part 2: a value the statement may not make

Every entry here is an expression the pass refuses because the statement may
not evaluate it. A target without ternaries already lowers an arm to a
statement before `ReleaseTemps` and so releases it; do that for every target.

- [ ] A ternary's or `match` expression's arm that allocates an owned value,
  on every target: the arm becomes a statement under an `if`, bound to a
  local, before the pass. `NoTernary` is the shape; the new lowering is
  owned-typed arms only, so a target with ternaries keeps them elsewhere.
- [ ] The right of `&&` or `||` the same way, and a `for` head bound to a
  local once, outside the loop, released after it.
- [ ] A temp bound before a later temp of the same statement raises while
  being bound: the catch `catchReleasing` writes wraps the bindings too, so
  the temps bound so far are released on the way out.
- [ ] Spec: strike the first two held entries.

## Part 3: `#[retains]` outside an effect

A program should not have to know where a `#[retains]` native may be called
from, so the lifetime is the node's wherever a node encloses the call.

- [ ] A `#[retains]` argument handed from an event handler, a timer's
  `@tick` or a component function gets the enclosing component's node
  lifetime: the same `__retN` cell `retain` makes for an effect's handler,
  released when the native is handed the next value and at unmount. Only a
  call with no node around it -- a free function, a package-level statement
  -- is refused at check time, naming the parameter and what it keeps.
- [ ] Audit `lib/`: no stdlib function exposes a `#[retains]` parameter a
  program could reach outside a node. Today gtk4 has none to write
  (`design/ownership.md`); pin that with a test over the library's
  declarations so a future one is noticed.
- [ ] Spec: strike the held entry, and say where a retained value lives.

## Part 4: the host's counting

GObject already counts references, so an owned GObject's release is an
`unref` and a copy stored in two places is a `ref`. This is the one place a
count earns its keep, and it is the host's, not SNGL's.

- [ ] A `#[owned]` struct may name a `retain` beside its release
  (`#[macro.owned(unref, retain=ref)]`): a value of such a type stored in a
  second var, field or collection is retained there, and each holder
  releases its own. A type without one keeps today's rule: a value stored
  twice is released twice, and the tracker says so.
- [ ] gtk4's pictures and any `gir:` type marked owned use it, so a picture
  shown in two places is one decode and two references. A `gir:` struct
  whose class is a `GObject` is declared with `g_object_ref`/`g_object_unref`
  by `declgen`.
- [ ] The interpreter counts: a handle carries how many holders retained it,
  and a leak is a handle with a holder left at teardown.
- [ ] Spec and Effective SNGL: say when to give a type a retain and when
  not to.

## Part 5: strings and the statement arena (design only)

The C-specific question, decided here and written down, with no code until a
C target exists to need it. In C `string` is owned, every concatenation and
interpolation allocates, and the choice is between a free per temp and an
arena per event or frame reset when it ends.

- [ ] Write the decision into `design/ownership.md`'s *Out of scope* section,
  replacing it: an arena for statement-lifetime values of types with no
  destructor (strings), explicit release for natives (a cairo surface cannot
  live in an arena), release-on-overwrite for state either way. Say what a
  C target's `ReleaseTemps` emits for each lifetime and what `CMarshal` does
  when the source string is already a C string.
- [ ] Note what Parts 1 to 4 leave for that target: nothing in the held
  list, so a C program leaks only what it holds in a `#[retains]` native
  with no node around it, which the checker refuses.

## Part 6: a sanitizer check on gtk4 (one-off, not a test)

A check of the generated code's correctness after Parts 1 to 4, run by hand
and written up, not a permanent suite: GTK keeps caches alive on purpose, so
no report is ever empty and a threshold would be a number nobody can defend.

- [ ] Build a gtk4 program with `go build -asan` and
  `CGO_CFLAGS=-fsanitize=address`, so Go and C are both instrumented, and run
  it under `ASAN_OPTIONS=detect_leaks=1` with a suppression file
  (`LSAN_OPTIONS=suppressions=…`) for GTK's and GLib's static allocations.
  Not valgrind: Go's allocator and stack switching are noise it has no
  suppressions for. ASan sees C allocations only -- a C string, a pixbuf, a
  cairo surface -- which is every allocation the held list names on gtk4;
  Go-held memory is the collector's and never a leak to it.
- [ ] The programs: `examples/pokedex` on gtk4, and the canvas-text and
  picture fixtures (`owned_canvas_text`, `gtk4_image_pictures`), each driven
  through its recorded run or its tests so every handler and frame runs,
  and ended by SIGTERM so the application tears down first. The report at
  exit is then a real hold, since `__snglTeardown` has released everything
  with a lifetime.
- [ ] Run the same programs at `ownership`'s last commit for the baseline,
  and diff: a hold Parts 1 to 4 were meant to close that is still reported
  is a bug with a fixture, an allocation the translator writes after
  lowering (`ownCStrings`) is #293's, and a GTK static goes in the
  suppression file with a line saying what it is.
- [ ] Write the result into `design/ownership.md`: what was run, what each
  report showed, and what is left to #293. Keep the suppression file under
  `tmp/` or in the note; it is not a test.

## Part 7: docs

- [ ] `design/ownership.md`: the held list paragraph says what is left,
  which is nothing, and each part's mechanism is in the section it changes.
- [ ] Spec's *Owned values*: the held list is gone; what replaces it is the
  rule for collections and the rule for a retained value's lifetime.
- [ ] Effective SNGL's *Owned values*: the "what the compiler holds" paragraph
  becomes the collection and retain guidance; the table gains the composed
  types.
- [ ] Delete this file.
