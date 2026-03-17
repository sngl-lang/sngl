Instead of having the code generators generate fixtures to run the component and instrument it, then run the component in the real environment. For web, use chrome debugging protocol to run tests in a headless browser. For CLI, use a virtual terminal. (See charmbracelet vhs). Use the new cogtest tool to to run these tests. Merge their coverage into the coverage report.

For the CLI tests, use txtar and script in the comments to test the CLI interactions.
