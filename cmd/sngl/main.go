package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme"
)

var rootCmd = &cobra.Command{
	Use:          "sngl",
	Short:        "SNGL compiler and toolchain",
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringP("directory", "C", "", "change to directory before running")
	rootCmd.PersistentFlags().String("project", ".", "project root directory")
	rootCmd.PersistentFlags().String("format", "text", "output format (text, json, sarif)")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress non-error output")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output (info-level logging)")
	rootCmd.PersistentFlags().Bool("debug", false, "debug output (debug-level logging)")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if dir, _ := cmd.Flags().GetString("directory"); dir != "" {
			if err := os.Chdir(dir); err != nil {
				return err
			}
		}

		debug, _ := cmd.Flags().GetBool("debug")
		verbose, _ := cmd.Flags().GetBool("verbose")
		quiet, _ := cmd.Flags().GetBool("quiet")

		level := slog.LevelWarn
		switch {
		case debug:
			level = slog.LevelDebug
		case verbose:
			level = slog.LevelInfo
		case quiet:
			level = slog.LevelError
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})))

		return nil
	}

	rootCmd.AddCommand(compileCmd)
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(stubCmd("lint", "Lint SNGL files"))
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(lspCmd)
	rootCmd.AddCommand(fmtCmd)
	rootCmd.AddCommand(previewCmd)
	rootCmd.AddCommand(docCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(dumpCmd)
}

// hasPathPrefix reports whether p is under the directory prefix.
// It uses filepath.Rel to handle separators and clean paths correctly.
func hasPathPrefix(p, prefix string) bool {
	rel, err := filepath.Rel(prefix, p)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// isUnderGoCache reports whether the given executable path is inside
// the Go build cache or a Go temporary build directory.
func isUnderGoCache(selfPath string) bool {
	goCache := os.Getenv("GOCACHE")
	if goCache == "" {
		if home, err := os.UserHomeDir(); err == nil {
			goCache = filepath.Join(home, ".cache", "go-build")
		}
	}
	if goCache != "" && hasPathPrefix(selfPath, goCache) {
		return true
	}
	// go tool builds to <tempdir>/go-build<digits>/... when running on the fly.
	// Match any directory in tempdir that starts with "go-build".
	tmpDir := filepath.Clean(os.TempDir())
	dir := selfPath
	for {
		parent := filepath.Dir(dir)
		if parent == tmpDir {
			return strings.HasPrefix(filepath.Base(dir), "go-build")
		}
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// proxyToGoTool checks if "go tool sngl" is available and execs into it.
// The SNGL_NO_PROXY env var prevents infinite recursion.
// If the current binary is already in the Go cache, proxying is skipped.
func proxyToGoTool() {
	if os.Getenv("SNGL_NO_PROXY") != "" || (len(os.Args) > 1 && os.Args[1] == "completion") {
		return
	}

	// If we're already running from the Go cache or a Go temp build dir,
	// no need to proxy.
	selfPath, err := os.Executable()
	if err == nil {
		selfPath = filepath.Clean(selfPath)
		if isUnderGoCache(selfPath) {
			return
		}
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return
	}

	// Resolve the "go tool sngl" binary path.
	toolPath := exec.Command(goPath, "tool", "-n", "sngl")
	toolPath.Env = append(os.Environ(), "SNGL_NO_PROXY=1")
	toolOut, err := toolPath.Output()
	if err != nil {
		return
	}
	toolBin := strings.TrimSpace(string(toolOut))

	fmt.Fprintf(os.Stderr, "sngl: proxying %s -> %s\n", selfPath, toolBin)

	args := append([]string{goPath, "tool", "sngl"}, os.Args[1:]...)
	env := append(os.Environ(), "SNGL_NO_PROXY=1")
	if err := syscall.Exec(goPath, args, env); err != nil {
		fmt.Fprintf(os.Stderr, "sngl: proxy exec failed: %v\n", err)
	}
}

func main() {
	proxyToGoTool()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
