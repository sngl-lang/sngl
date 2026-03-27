module git.duckfam.us/jonathan/sngl

go 1.26.1

require (
	github.com/alecthomas/chroma/v2 v2.14.0
	github.com/charmbracelet/glamour v0.8.0
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/charmbracelet/x/cellbuf v0.0.15
	github.com/fsnotify/fsnotify v1.9.0
	github.com/go-rod/rod v0.116.2
	github.com/muesli/termenv v0.16.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/yuin/goldmark v1.7.8
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	golang.org/x/tools v0.24.0
	gopkg.in/yaml.v3 v3.0.1
	rsc.io/script v0.0.2
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymanbagabas/go-udiff v0.4.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/exp/golden v0.0.0-20250806222409-83e3a29d542f // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.20 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/ysmood/fetchup v0.2.3 // indirect
	github.com/ysmood/goob v0.4.0 // indirect
	github.com/ysmood/got v0.40.0 // indirect
	github.com/ysmood/gson v0.7.3 // indirect
	github.com/ysmood/leakless v0.9.0 // indirect
	github.com/yuin/goldmark-emoji v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.31.0 // indirect
)

tool (
	git.duckfam.us/jonathan/sngl/cmd/sngl
	git.duckfam.us/jonathan/sngl/internal/cmd/verify
	git.duckfam.us/jonathan/sngl/internal/cmd/docsgen
)
