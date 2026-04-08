module git.duckfam.us/jonathan/sngl

go 1.26.1

require (
	charm.land/bubbles/v2 v2.1.0
	charm.land/bubbletea/v2 v2.0.2
	charm.land/lipgloss/v2 v2.0.2
	fyne.io/fyne/v2 v2.7.3
	github.com/alecthomas/chroma v0.10.0
	github.com/alecthomas/chroma/v2 v2.14.0
	github.com/charmbracelet/glamour v0.8.0
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/charmbracelet/x/cellbuf v0.0.15
	github.com/davecgh/go-spew v1.1.1
	github.com/fsnotify/fsnotify v1.9.0
	github.com/go-rod/rod v0.116.2
	github.com/mattn/go-isatty v0.0.20
	github.com/muesli/termenv v0.16.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/yuin/goldmark v1.7.8
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	golang.org/x/image v0.38.0
	golang.org/x/tools v0.43.0
	gopkg.in/yaml.v3 v3.0.1
	rsc.io/script v0.0.2
)

require (
	fyne.io/systray v1.12.0 // indirect
	github.com/BurntSushi/toml v1.5.0 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260205113103-524a6607adb8 // indirect
	github.com/charmbracelet/x/ansi v0.11.6 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/fredbi/uri v1.1.1 // indirect
	github.com/fyne-io/gl-js v0.2.0 // indirect
	github.com/fyne-io/glfw-js v0.3.0 // indirect
	github.com/fyne-io/image v0.1.1 // indirect
	github.com/fyne-io/oksvg v0.2.0 // indirect
	github.com/go-gl/gl v0.0.0-20231021071112-07e5d0ea2e71 // indirect
	github.com/go-gl/glfw/v3.3/glfw v0.0.0-20240506104042-037f3cc74f2a // indirect
	github.com/go-text/render v0.2.0 // indirect
	github.com/go-text/typesetting v0.3.3 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/hack-pad/go-indexeddb v0.3.2 // indirect
	github.com/hack-pad/safejs v0.1.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jeandeaual/go-locale v0.0.0-20250612000132-0ef82f21eade // indirect
	github.com/jsummers/gobmp v0.0.0-20230614200233-a9de23ed2e25 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.21 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/nicksnyder/go-i18n/v2 v2.5.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rymdport/portal v0.4.2 // indirect
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c // indirect
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/ysmood/fetchup v0.2.3 // indirect
	github.com/ysmood/goob v0.4.0 // indirect
	github.com/ysmood/got v0.40.0 // indirect
	github.com/ysmood/gson v0.7.3 // indirect
	github.com/ysmood/leakless v0.9.0 // indirect
	github.com/yuin/goldmark-emoji v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/term v0.41.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

tool (
	git.duckfam.us/jonathan/sngl/cmd/sngl
	git.duckfam.us/jonathan/sngl/internal/cmd/docsgen
	git.duckfam.us/jonathan/sngl/internal/cmd/verify
)
