Update Fyne to use codegen.RenderTemplates
Refactor dependency tracking to the optimizer so codegen (html/fyne) can reuse it.
/plan Create an example that includes a multiline textbox that accepts Go code (default to a hello world) and allows you to parse the source and populate the AST into a tree view. Language: go, platforms fyne, bubbletea, and android. Use external Go code to get the parser from the stdlib.
