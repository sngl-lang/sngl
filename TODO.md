# Pokédex audit

A review of `examples/pokedex` and of what it compiles to on each target, at
`850332d7a`. The work below goes in the order written: the language's pain
points first, then the bugs and the generated code.

## Bugs

8. **The other triggers should move to overrides of their events.** Done
   for select and radio, which say how a test raises `@change` in SNGL
   (`func ui.select[T].change[platform](value)`); the generated invokers
   (`codegen.TriggerEventName`, `ir.PressedNode`, bubbletea's payload
   invokers) still answer every other event.
9. **html has no node handles in JS**, so its select's trigger calls the
   override's handler logic (`pickLabel`) rather than setting the element's
   value and dispatching `change`; android and bubbletea do the same.
