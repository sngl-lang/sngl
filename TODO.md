Want to build a documentation site for SNGL:

1. Create platform snapshot generators. These generate PNG images of how a UI looks

- ./internal/testutil/webtest (copy from ../../jj-software/cog/internal/testing/webtest)
- I think charmbracelet has something for the terminal
- Chrome Debugging Protocol can take pics of web pages
- Will need a test that asserts the fake CSS looks like real platform; don't need to commit PNGs``
- Will need a Dockerfile with the deps to create all snapshots and assets

2. docs/ directory with markdown per page.

- Home: what it is, why it is, inspiration sources
- Getting Started
  - Architecture
  - Installation
  - First App: Web
  - Build Targets
  - Binding Data to the parent language
  - ...
- Language Reference
- Language Specification

3. Playground

- Compile compiler/preview logic to webassembly for static assets
- UI has editor in main area
  - Syntax highlight and LSP (maybe VSCode-based editor)
- Tabs on the right
  - Preview
  - AST

4. Static site generator

- Runs as gitlab pages job with Dockerfile deps image
- Generate assets for each supported platform
- Convert docs to HTML, ensure Syntax highlighting works; inject "View in Playground"
- Template complex pages with go templates
