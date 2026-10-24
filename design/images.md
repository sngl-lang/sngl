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
  `<img>` its source renders (`codegen/platform/html/image.go`). A drawing
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
- gtk4: a gtk4rt image box and a `GtkPicture` per source, which fits itself to
  the image it lands in by watching its own `notify::parent`. A URL's picture
  is an owned derived value of its source (`ui.file`'s and `ui.remote`'s
  `picture`, a `gtk4rt.Picture`), decoded once per URL and released when the
  URL changes or the source unmounts; the source shows it and `canvasImage`
  paints it, scaled once per box size rather than per frame
  (`testdata/gtk4_image_pictures.txtar`, and
  `cmd/sngl/testdata/simulate_gtk4_image_pictures.txt` for the leak check).
  Before, a source fetched its own URL and a drawing downloaded and scaled
  the texture on every frame (see [ownership.md](ownership.md)). A drawing makes
  every gtk4 build inline cgo (a canvas does), so the image runtime is called
  from cgo-mode code too, which is why gtk4rt's exported callbacks are
  `snglRt*`: the cgo scaffold exports `snglGoDispatch` and `snglActivate` of
  its own. GTK 4 sets a window's icon by name only, so the picture is written
  into a private icon-theme directory (`iconize`).
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
