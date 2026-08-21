package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/tools/txtar"
	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func TestScript(t *testing.T) {
	// Pre-detect the host go.mod from the test process's starting cwd (inside
	// the sngl repo) so that codegen.DetectHostGoMod can still resolve a
	// replace directive after the script test chdir's into its sandbox.
	if cwd, err := os.Getwd(); err == nil {
		if mod := findGoModAncestor(cwd); mod != "" {
			t.Setenv("SNGL_HOST_GO_MOD", mod)
		}
	}
	conds := scripttest.DefaultConds()
	// `display` is true when an X11/Wayland display is reachable. gtk4 renders
	// through real GDK, which calls the X/Wayland server; headless CI has
	// neither, so those snapshot scripts guard with `[!display] skip`. (fyne
	// renders in-memory via fyne/test and needs no guard.)
	conds["display"] = script.BoolCondition(
		"an X11/Wayland display is available",
		os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "",
	)
	// `gtk4` is true when the gtk4 development libraries are installed, so the
	// generated cgo can be compiled. The gtk4 build script uses it to compile
	// generated gtk4 code in CI (no display needed) even though the render-based
	// snapshot script skips there.
	// The GIR check matters independently of pkg-config: distributions can ship
	// the libraries without the introspection data, and without the .gir file
	// the platform reports itself unavailable and refuses to generate.
	conds["gtk4"] = script.BoolCondition(
		"gtk4 development libraries and introspection data are available",
		exec.Command("pkg-config", "--exists", "gtk4").Run() == nil &&
			codegen.PlatformUnavailable("gtk4") == nil,
	)
	// `ci` is true under GitLab CI. Used to quarantine a script that fails only
	// in the CI environment while it's being investigated, without losing the
	// coverage everywhere else.
	conds["ci"] = script.BoolCondition(
		"running under GitLab CI",
		os.Getenv("GITLAB_CI") == "true" || os.Getenv("CI") == "true",
	)
	// `short` is true under `go test -short`. Slow scripts (e.g. the Robolectric
	// round-trips, ~2.5min combined) guard with `[short] skip` so the fast local
	// loop stays quick; the full run (and `go tool verify`) still exercises them.
	conds["short"] = script.BoolCondition(
		"go test -short is set",
		testing.Short(),
	)
	engine := &script.Engine{
		Cmds:  scriptCmds(),
		Conds: conds,
	}
	files, err := filepath.Glob("testdata/*.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		t.Run(name, func(t *testing.T) {
			a, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			s, err := script.NewState(context.Background(), t.TempDir(), os.Environ())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ExtractFiles(a); err != nil {
				t.Fatal(err)
			}
			scripttest.Run(t, engine, s, file, bytes.NewReader(a.Comment))
		})
	}
}

func scriptCmds() map[string]script.Cmd {
	cmds := scripttest.DefaultCmds()
	cmds["sngl"] = snglCmd()
	return cmds
}

func snglCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "run sngl subcommand in-process",
			Args:    "args...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			// Capture stdout.
			oldOut := os.Stdout
			rOut, wOut, err := os.Pipe()
			if err != nil {
				return nil, err
			}
			os.Stdout = wOut

			// Capture stderr.
			oldErr := os.Stderr
			rErr, wErr, err := os.Pipe()
			if err != nil {
				wOut.Close()
				rOut.Close()
				os.Stdout = oldOut
				return nil, err
			}
			os.Stderr = wErr

			// Read pipes in background to avoid deadlock.
			var stdoutBuf, stderrBuf strings.Builder
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); io.Copy(&stdoutBuf, rOut) }()
			go func() { defer wg.Done(); io.Copy(&stderrBuf, rErr) }()

			// Switch to the script's working directory.
			oldDir, _ := os.Getwd()
			os.Chdir(s.Getwd())

			// Propagate the script state's environment into the process
			// so in-process sngl reads see vars set via `env VAR=value`.
			// Diff against the current process env, applying only the
			// keys that changed, and remember the originals to restore.
			origEnv := map[string]string{}
			origUnset := map[string]bool{}
			scriptEnv := map[string]string{}
			for _, kv := range s.Environ() {
				if before, after, ok := strings.Cut(kv, "="); ok {
					scriptEnv[before] = after
				}
			}
			for k, v := range scriptEnv {
				if cur, ok := os.LookupEnv(k); ok {
					if cur == v {
						continue
					}
					origEnv[k] = cur
				} else {
					origUnset[k] = true
				}
				os.Setenv(k, v)
			}

			// Reset flags to defaults so prior invocations don't leak state.
			resetFlags(rootCmd)
			rootCmd.SetArgs(args)
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			cmdErr := rootCmd.Execute()

			// Restore state.
			os.Chdir(oldDir)
			for k, v := range origEnv {
				os.Setenv(k, v)
			}
			for k := range origUnset {
				os.Unsetenv(k)
			}
			// Mirror main(): print the command error to stderr so script
			// assertions like `stderr some-text` can match it.
			if cmdErr != nil {
				fmt.Fprintln(wErr, cmdErr)
			}
			wOut.Close()
			wErr.Close()
			os.Stdout = oldOut
			os.Stderr = oldErr
			wg.Wait()
			rOut.Close()
			rErr.Close()

			return func(*script.State) (string, string, error) {
				return stdoutBuf.String(), stderrBuf.String(), cmdErr
			}, nil
		},
	)
}

// findGoModAncestor walks upward from start looking for a go.mod file.
// Returns the absolute path to the file, or "" if none found.
func findGoModAncestor(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// resetFlags resets all flags on cmd and its subcommands to their default values.
// pflag's slice values append on Set, so use the SliceValue.Replace path when
// available; otherwise fall through to a plain Set against DefValue.
func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			sv.Replace(nil)
		} else {
			f.Value.Set(f.DefValue)
		}
		f.Changed = false
	})
	for _, sub := range cmd.Commands() {
		resetFlags(sub)
	}
}
