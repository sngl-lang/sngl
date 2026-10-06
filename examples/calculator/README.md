# Calculator

A pocket calculator, written to find out what SNGL is like to build a real
application in rather than a demo of one feature.

```
sngl test --platform none      examples/calculator   # 22 tests, the interpreter
sngl test --platform html      examples/calculator   # the same 22, in a browser
sngl test --platform bubbletea examples/calculator   # the same 22, as a Go binary

sngl generate --platform html      --lang none -o /tmp/calc     examples/calculator
sngl generate --platform bubbletea --lang go   -o /tmp/calc-tui examples/calculator
```

## How it is put together

Four packages, split the way the problem splits:

| package    | what is in it                                                   |
|------------|-----------------------------------------------------------------|
| `calc/`    | the state machine and its arithmetic. Imports no UI at all.     |
| `readout/` | the seven-segment display, drawn twice for two kinds of screen. |
| `keypad/`  | the keys, as data, and the grid that lays them out.             |
| (root)     | `app.sngl`, the wiring; `calc_test.sngl`, the tests.            |

**The state is a string.** `Calc.entry` is what the readout shows, not a
rendering of a number: a calculator that stored a float could not tell `3.`
from `3`, and that difference is the whole of what pressing the point does.

**Every key is a method that answers with the next calculator.** `digit`,
`point`, `operate`, `equals` and the rest take no arguments they do not need
and return a new `Calc`, so the `Calculator` component holds `var state calc.Calc` and each press
is one assignment — which is also the reactive update. Nothing in `calc/`
knows a screen exists, which is why the tests for it are ordinary assertions
about strings.

**The display is one data structure and two renderers.** `readout/glyph.sngl`
turns a character into a `Glyph` — seven booleans and a decimal point.
`canvas.sngl` paints each Glyph as seven rectangles on a `sngl:ui/draw` canvas;
`ascii.sngl` renders the same Glyph as three rows of `_` and `|` inside a
box-drawing frame. `readout.sngl` picks between them on `PLATFORM == bubbletea.platform`, which folds at build time — the web build carries no box
characters and the terminal build carries no canvas.

**The keypad is data.** `keypad/key.sngl` declares a `Key` carrying what
pressing it *means* (an `Action`, and the digit or operator it names) rather
than a label the handler has to parse back. `rows()` is a
`list<list<Key>>`, so the layout is five lines long however many keys there
turn out to be, and `Calculator` switches on the action.

**The keys fill the window.** Each row is an `hbox(style={flex=1})` and each
key a `button(style={flex=1})`, so the grid takes whatever room the window
gives it.

## What works where

| target      | tests | generated code                                   |
|-------------|-------|--------------------------------------------------|
| `none`      | 22/22 | — (the interpreter runs the program)             |
| `html`      | 22/22 | runs in a browser; the readout is a 2D canvas    |
| `bubbletea` | 22/22 | compiles and runs; ASCII readout                 |
| `fyne`      | 22/22 | compiles and runs; the readout is a 2D canvas    |
| `gtk4`      | 22/22 | compiles and runs; the readout is a cairo canvas |
| `android`   | 22/22 | Robolectric; the readout is a Compose canvas     |

`sngl snapshot examples/calculator` renders it on every target in its `output`
block.

Fyne reaches `style` in two halves, because the toolkit does. The layout half
(`flex`, `gap`, `padding`, `margin`) is one generated `fyne.Layout` per box;
the paint half (`background`, `color`, `fontSize`, `fontWeight`,
`borderRadius`) is a table of generated themes, since a Fyne widget takes its
colors from a theme and nowhere else. The twenty keys share three themes.

What a theme cannot reach is a widget that never asks: a plain container paints
no background, so `background` on a box is honored everywhere but there.
