Evaluate constant CEL expressions and shake dead branches before emitting. Include PLATFORM and TARGET as constant expression.

I need ways to define data/methods/etc that the hand-written language code should provide in addition to the internal state we can define now. I should also be able to define a hook for the language code to receive when internal data is changed.

Create a live-reload for the HTML target as a build option. Preserve internal state and re-render. (This will require generating JS that can replace the initial HTML when live-reload is enabled.) Use this to build a WYSIWYG interface in the browser. Provide drop downs for platforms.
