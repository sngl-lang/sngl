# Images

**Where an image's pixels come from is a family, `ui.imageSource`**
(`lib/ui/image.sngl`), so a drawing whose shapes read state repaints by the
reactivity every canvas already has rather than a second one a value type
would need. Its members are `ui.file(path)` -- a `file:` asset the build
copies, which a Go target embeds (`golang.EmitAssetEmbed`, `assets.go`) and
android ships in the app's assets -- `ui.remote(url)`, fetched while the
program runs, and `draw.image(width, height) { shapes }`. Every container
takes a source through a slot: `ui.image`'s `src` (`option`; an image with
none shows nothing, as html's `<picture>` does), `avatar`'s, `markup.image`'s,
`draw.canvasImage`'s, and a window's `favicon`. There is no `src=` string:
one spelling (Jonathan's call). `draw.image` and `ui.image` collide under a
double dot import, and dot imports are an anti-pattern, so that is the cost
of the name rather than a reason against it.

**Each member is overridden per target to a primitive of the target's own**,
and the shared parts read the primitive structurally: a URL source carries a
`url` prop (`ir.ImageSourceURL`), and a drawing is a shape container in the
image source family, which `passShapeDraw`, `passCanvasInstances`,
`passCanvasReactivity` and `codegen.Canvases` already find. What a container
does with it is each target's:

- html: an image is a `<span class="sngl-image">` frame carrying the style,
  the click and the description (`role="img"`, `aria-label`), around the
  `<img>` its source renders (`codegen/platform/html/image.go`). The rules
  clipping the frame and fitting the picture are the page's wherever a frame
  is (`namesImageFrame`); written only beside a source, a frame showing its
  placeholder alone was not clipped (`TestAFrameWithNoSourceIsClipped`). A
  drawing
  paints into a canvas of its own and writes a data URL into `src`
  (`_snglImageDraw`) on every repaint. The document's favicon is the source
  its Window override put beside the content (`splitFavicon`): a literal URL
  is the head's `<link rel="icon">`, anything else a link answering a write
  of `src` with its `href`.
- fyne: `fynelayout.Image`, a widget whose `Box` holds the source -- an
  `ImageSource` showing the picture itself and reporting `@load`/`@error` to
  the image it lands in. A drawn source is rasterized
  and handed over (`SetImage`). A window takes a source as its icon
  (`Toplevel.Add`). The specs are written in Go as `Toplevel`'s are
  (`codegen/platform/fyne/image.go`), with `slotBox` naming the box a slot
  inside renders into.
- gtk4: the runtime's image box and a `GtkPicture` per source (rt's
  `image.c`), reached through protocol functions. A URL's picture is an
  owned derived value of its source (`ui.file`'s and `ui.remote`'s
  `picture`, the runtime's `SnglPicture`), decoded once per URL and released
  when the URL changes or the source unmounts; the source shows it and
  `canvasImage` paints it (`testdata/gtk4_image_pictures.txtar`, and
  `cmd/sngl/testdata/simulate_gtk4_image_pictures.txt` for the leak check).
  An image's `@failed` takes nothing and the override reads why off the image
  (`imageFailure`), as win32's does. A drawn source is painted through its
  protocol as a canvas is (`design/nodes.md`): its shapes are the handler of
  its `@paint`, which paints a texture of its size, and a write to what they
  read is its redraw. A URL is fetched over a `GSocketClient` rather than
  GIO's `GFile`, whose http is GVfs's, which a test's session bus does not
  run (`image_remote_runs.txt`). A window takes a source appended to it as
  its icon (`sngl_image_adopt`), from a root component's window too; GTK 4
  sets a window's icon by name only, so the picture is written into a private
  icon-theme directory (`cmd/sngl/testdata/gtk4_test_runs.txt`'s
  `pictures.sngl`).
- bubbletea: a drawing is a canvas; a URL is half-block cells fitted into a
  fixed box (`tui.Picture`), and a fetch arriving sends the program a message
  (`snglimage.OnArrive`).
- android: shipped Kotlin (`codegen/platform/android/assets.kt`). An image
  is `SnglImage`, a box that hands its source `@load` and `@error` through a
  CompositionLocal; a file is `SnglAssetImage`, read out of the app's assets;
  a URL is `SnglRemoteImage`, fetched with `HttpURLConnection` on
  `Dispatchers.IO` and kept once it arrives, and counted in
  `SnglAssets.active` while it is fetched, which a Robolectric test's wait for
  idle waits on as it does `SnglCodec.active` (`activeCounters`). A URL in a
  `canvasImage` still paints nothing. A drawing is a `Canvas`. The manifest's
  permissions are android's own mark (see *An android app asks for what it
  reaches* in [architecture.md](architecture.md)).
- none: the image holds its source, which a snapshot prints.

**An avatar is a wrapper on `ui.image` that does the fallback**: its
`fallback` slot -- the initials in a styled box unless the call site writes
its own -- stands in where `src.empty` or the image reported `@error`. A
failed load is remembered until the avatar renders again, since the image
that would say a later source loaded is no longer on screen. html, bubbletea
and none do not yet report an image's `@error`, so there only an empty source
shows the fallback.

`canvasImage` paints its source through a shape primitive whose rest slot is
the source family (`ir.IsImageSourceHost`): `passShapeDraw` runs its `@url`
handler with the URL in the payload, or brackets a drawing's shapes between
`@enter` and `@leave`, which map the drawing's space onto the box and clip --
so a drawing in a drawing is painted in place. `pkg/go/snglimage` is where
the Go targets load bytes: embedded, from the working directory, or fetched
once per URL. `testdata/image_sources.txtar`,
`codegen/platform/html/image_browser_test.go`,
`cmd/sngl/testdata/image_sources_runs.txt` and `image_remote_runs.txt`, which
fetches from the script harness's server on android, fyne and gtk4.
