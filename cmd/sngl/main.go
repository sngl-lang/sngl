package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
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

		tuneGC(cmd)
		return nil
	}

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

func hasPathPrefix(p, prefix string) bool {
	rel, err := filepath.Rel(prefix, p)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

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

// longLived marks a command that stays up serving requests, so tuneGC leaves
// its collector alone.
const longLived = "sngl.longLived"

// oneShotGCPercent is the collector target for a command that runs a build and
// exits. A compile allocates tens of megabytes it holds to the end, and at the
// default of 100 the collector ran through about 40% of `sngl generate`'s CPU
// re-marking that live heap: 400 halved the CPU of a four-target build of
// examples/showcase and took a fifth off its wall time.
const oneShotGCPercent = 400

// oneShotMemoryLimit bounds what oneShotGCPercent trades. A target of 400 lets
// the heap reach five times what is live, which for a small build is tens of
// megabytes and for the docs site -- over 200MB live -- was 1.3GB. Past the
// limit the collector runs as often as it must to stay under it, so a big
// build pays in CPU instead: the docs site ran in less CPU *and* less memory
// than at the default (5.3s and 580MB against 6.3s and 700MB).
const oneShotMemoryLimit = 512 << 20

// tuneGC raises the collector target for a one-shot command, under a memory
// limit. A server -- `sngl lsp`, `sngl preview` -- keeps the defaults, since it
// lives long enough for a larger heap to matter and short builds are not what
// it spends its time on. An explicit GOGC or GOMEMLIMIT wins over each.
func tuneGC(cmd *cobra.Command) {
	if cmd.Annotations[longLived] != "" {
		return
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(oneShotGCPercent)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(oneShotMemoryLimit)
	}
}

// SNGL_NO_PROXY prevents infinite recursion through the exec below.
func proxyToGoTool() {
	if os.Getenv("SNGL_NO_PROXY") != "" || (len(os.Args) > 1 && os.Args[1] == "completion") {
		return
	}

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

	toolPath := exec.Command(goPath, "tool", "-n", "sngl")
	toolPath.Env = append(os.Environ(), "SNGL_NO_PROXY=1")
	toolOut, err := toolPath.Output()
	if err != nil {
		return
	}
	toolBin := strings.TrimSpace(string(toolOut))

	fmt.Fprintf(os.Stderr, "sngl: proxying %s -> %s\n", selfPath, toolBin)

	// Exec the resolved tool binary directly rather than going back
	// through `go tool sngl`. `go tool -n` already built it and printed
	// an up-to-date path, so a second `go` invocation would redo that
	// work for nothing — about 90ms and 0.28s of CPU per call.
	args := append([]string{toolBin}, os.Args[1:]...)
	env := append(os.Environ(), "SNGL_NO_PROXY=1")
	if err := syscall.Exec(toolBin, args, env); err != nil {
		fmt.Fprintf(os.Stderr, "sngl: proxy exec failed: %v\n", err)
	}
}

// The returned func must run before the process exits. Profiling is
// env-driven (SNGL_CPUPROFILE, SNGL_MEMPROFILE) rather than a flag so another
// tool driving a build (docsgen, go tool sngl) need not thread one through.
func startProfiling() func() {
	var stop []func()
	if path := os.Getenv("SNGL_CPUPROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sngl: cpuprofile: %v\n", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "sngl: cpuprofile: %v\n", err)
			f.Close()
		} else {
			stop = append(stop, func() {
				pprof.StopCPUProfile()
				f.Close()
			})
		}
	}
	if path := os.Getenv("SNGL_MEMPROFILE"); path != "" {
		stop = append(stop, func() {
			f, err := os.Create(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sngl: memprofile: %v\n", err)
				return
			}
			defer f.Close()
			runtime.GC()
			if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
				fmt.Fprintf(os.Stderr, "sngl: memprofile: %v\n", err)
			}
		})
	}
	return func() {
		for _, fn := range stop {
			fn()
		}
	}
}

func main() {
	proxyToGoTool()

	stopProfiling := startProfiling()
	defer stopProfiling()

	if err := rootCmd.Execute(); err != nil {
		stopProfiling()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
