package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/tools/txtar"
	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func TestScript(t *testing.T) {
	engine := &script.Engine{
		Cmds:  scriptCmds(),
		Conds: scripttest.DefaultConds(),
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

			// Reset flags to defaults so prior invocations don't leak state.
			resetFlags(rootCmd)
			rootCmd.SetArgs(args)
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			cmdErr := rootCmd.Execute()

			// Restore state.
			os.Chdir(oldDir)
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
