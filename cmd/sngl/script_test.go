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
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/internal/jdk"
	"github.com/go-rod/rod/lib/launcher"
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
	// `node` is true when a node binary is on PATH. Compile-time evaluation of
	// a pure js: function shells out to it; the CI image ships chromium and
	// GTK but no node, so those scripts guard with `[!node] skip`.
	conds["node"] = script.BoolCondition(
		"a node binary is available",
		func() bool { _, err := exec.LookPath("node"); return err == nil }(),
	)
	// `ci` is true under GitLab CI. Used to quarantine a script that fails only
	// in the CI environment while it's being investigated, without losing the
	// coverage everywhere else.
	conds["ci"] = script.BoolCondition(
		"running under GitLab CI",
		os.Getenv("GITLAB_CI") == "true" || os.Getenv("CI") == "true",
	)
	// `chromium` is true when go-rod can find a Chrome/Chromium binary — the
	// same lookup the html launcher does before it emits a SkipError. Scripts
	// that assert on a real browser run guard with `[!chromium] skip`.
	conds["chromium"] = script.BoolCondition(
		"a Chrome/Chromium binary go-rod can drive is available",
		func() bool { _, found := launcher.LookPath(); return found }(),
	)
	// `android-jdk` / `android-sdk` mirror the two prerequisites the android
	// launcher checks before skipping. `[!exec:java]` is not enough: a JDK
	// outside the toolchain's supported window is on PATH but unusable, and
	// the SDK root is an env var rather than a binary.
	conds["android-jdk"] = script.BoolCondition(
		"a JDK the Android toolchain supports is available",
		func() bool {
			combo := androidtc.Default()
			_, reason := jdk.CompatibleHome(combo.JDKMin, combo.JDKMax)
			return reason == ""
		}(),
	)
	conds["android-sdk"] = script.BoolCondition(
		"ANDROID_HOME or ANDROID_SDK_ROOT points at an Android SDK",
		os.Getenv("ANDROID_HOME") != "" || os.Getenv("ANDROID_SDK_ROOT") != "",
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
	cmds["cmpgen"] = cmpGenCmd()
	return cmds
}

// cmpGenCmd is `cmp` for two generated files, ignoring the header line naming
// the source they came from. Two spellings of one program -- a top-level body
// and the window it is shorthand for -- differ in nothing else, and asserting
// that directly says more than a handful of greps chosen after the fact.
func cmpGenCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "compare two generated files, ignoring the generated-from header",
			Args:    "file1 file2",
		},
		func(st *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			read := func(name string) (string, error) {
				b, err := os.ReadFile(st.Path(name))
				if err != nil {
					return "", err
				}
				var keep []string
				for _, line := range strings.Split(string(b), "\n") {
					if strings.Contains(line, "Code generated by sngl") {
						continue
					}
					keep = append(keep, line)
				}
				return strings.Join(keep, "\n"), nil
			}
			a, err := read(args[0])
			if err != nil {
				return nil, err
			}
			b, err := read(args[1])
			if err != nil {
				return nil, err
			}
			if a != b {
				return nil, fmt.Errorf("%s and %s differ:\n%s", args[0], args[1], lineDiff(a, b))
			}
			return nil, nil
		},
	)
}

// lineDiff reports the first differing line, which is enough to identify what
// diverged between two spellings of one program.
func lineDiff(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) || i < len(lb); i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n  -%s\n  +%s", i+1, x, y)
		}
	}
	return ""
}

func snglCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "run sngl subcommand in-process",
			Args:    "args...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			oldOut := os.Stdout
			rOut, wOut, err := os.Pipe()
			if err != nil {
				return nil, err
			}
			os.Stdout = wOut

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

			oldDir, _ := os.Getwd()
			os.Chdir(s.Getwd())

			// In-process sngl reads the process environment, so a var set
			// with `env VAR=value` has to be applied to it — only the keys
			// that differ, with the originals kept for the restore below.
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

// pflag's slice values append on Set, so a reset goes through
// SliceValue.Replace where available and a plain Set against DefValue otherwise.
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
