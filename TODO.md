Instead of having the code generators generate fixtures to run the component and instrument it, then run the component in the real environment. For web, use chrome debugging protocol to run tests in a headless browser. For CLI, use a virtual terminal. (See charmbracelet vhs). Use the new cogtest tool to to run these tests. Merge their coverage into the coverage report.

The HTML/web-specific logic has begun to sprawl throughout the repository. The html platform should contain the majority of it. Refactor those

For the CLI tests, use txtar and https://pkg.go.dev/rsc.io/script in the comments to test the CLI interactions. Run the subcommands in-process, so the coverage can be captured.
