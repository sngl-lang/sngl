//go:build !js

package html

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Run implements codegen.Runner. It opens the generated index.html in the
// default web browser.
func (g *Generator) Run(dir string, _ map[string]string, args []string) error {
	path := filepath.Join(dir, "index.html")
	return openBrowser(path)
}

func openBrowser(url string) error {
	var cmd string
	var cmdArgs []string

	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
		cmdArgs = []string{url}
	case "darwin":
		cmd = "open"
		cmdArgs = []string{url}
	case "windows":
		cmd = "cmd"
		cmdArgs = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform %q for opening browser", runtime.GOOS)
	}

	return exec.Command(cmd, cmdArgs...).Start()
}
