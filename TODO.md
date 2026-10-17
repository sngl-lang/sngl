# `match`, later

`match` is implemented and kept in the IR: the rules are in CLAUDE.md's
*Match* section and the specification's *The match statement*. What is left is
the longer road.

## Generated code a person would write

A target that declares `matches`, `matchValues`, `viewMatches` or
`optionMatches` writes its own switch; everything else gets the if chain or the
ternaries. The shapes still written as the chain where a host has a construct
for them:

- **A JavaScript match expression.** There is no switch expression, so it is
  the ternaries, and a subject that calls is bound to a local ahead of the
  statement.
- **An option subject in JavaScript.** An option of a scalar is the bare value
  or `null` there, so a `switch` would compare it as the chain's `===` does.
  html withdraws `optionMatches` for its script, since under `--lang go` the
  same capability would reach Go's server half, where an option is a pointer.
- **A view body on html, fyne and gtk4.** Their views are a tree built once
  and patched, so a match there is the `if` chain a render slot re-renders;
  `viewMatches` would need each to render the arms as a slot of its own.

## Language

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
