# `match`, later

`match` is implemented and kept in the IR: the rules are in CLAUDE.md's
*Match* section and the specification's *The match statement*. What is left is
the longer road.

## Generated code a person would write

A target that declares `matches`, `matchValues` or `viewMatches` writes its
own switch; everything else gets the if chain or the ternaries. The shapes
still written as the chain where a host has a construct for them:

- **An option subject.** Kotlin's `when` takes `null ->` beside the element's
  values; Go and JavaScript compare an option differently from its element, so
  `Switchable` refuses every option today. A per-language answer needs the
  capability to say which subject types a language switches on.
- **A view body on bubbletea.** Its view is Go statements, so a `switch` could
  stand there as a `when` does on android; its view walks (focus order, loop
  widgets, presence, overlays) each need a match case before it can declare
  `viewMatches`.
- **A JavaScript match expression.** There is no switch expression, so it is
  the ternaries, and a subject that calls is bound to a local ahead of the
  statement.
- **An arm that breaks out of a loop** in Go and JavaScript, which a labelled
  loop would let stay a switch.

## Language

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
