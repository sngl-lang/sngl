package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/interprun"
	"duckfam.us/sngl/ir"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] <file.sngl> [-- args...]",
	Short: "Compile and run an SNGL app",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

func init() {
	runCmd.Flags().String("lang", "", "target language")
	runCmd.Flags().String("platform", "", "target platform")
	runCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
	addReleaseFlag(runCmd)
}

func runRun(cmd *cobra.Command, args []string) error {
	cliLang, _ := cmd.Flags().GetString("lang")
	cliPlat, _ := cmd.Flags().GetString("platform")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")
	isRelease, _ := cmd.Flags().GetBool("release")

	file := args[0]
	// Cobra takes the `--` out of args, and says where it stood.
	var progArgs []string
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		progArgs = args[at:]
	}

	// Stable, key-derived build directory so repeated invocations reuse
	// the Go build cache instead of relinking from scratch every time.
	// See codegen.BuildDir. A release build is a key of its own.
	key := []string{cliLang, cliPlat, strings.Join(optSlice, ","), file}
	if isRelease {
		key = append(key, "--release")
	}
	tmpDir, release, err := codegen.BuildDir("run", key...)
	if err != nil {
		return fmt.Errorf("creating build directory: %w", err)
	}
	defer release()

	return runPipeline(cmd, []string{file}, pipelineOpts{
		cliLang: cliLang,
		cliPlat: cliPlat,
		cliOpts: build.ParseCLIOpts(optSlice),
		outDir:  tmpDir,
		main:    true,
		quiet:   true,
		onTarget: func(target build.Target, pkg *ir.Package, _, outDir string) error {
			target.Release = isRelease
			if build.IsInterpreted(target) {
				dir, err := os.Getwd()
				if err != nil {
					return err
				}
				return interprun.Run(pkg, interprun.Options{
					Dir: dir, Args: progArgs, Platform: target.Platform,
					Headless: codegen.OptionString(target.Options, "host") == "mem",
				})
			}
			if !build.HasCommand(target, ir.BuiltinGenRun) {
				return fmt.Errorf("platform %q with language %q does not support direct execution", target.Platform, target.Lang)
			}
			args := make([]any, len(progArgs))
			for i, a := range progArgs {
				args[i] = a
			}
			if testRuns == nil {
				return build.RunCommand(target, ir.BuiltinGenRun, outDir, args)
			}
			out, err := testRuns.AnswerTool(toolKey("run", target, []string{file}), outDir, append(releaseArgs(optSlice, isRelease), progArgs...), func() (string, error) {
				return captureStdout(func() error {
					return build.RunCommand(target, ir.BuiltinGenRun, outDir, args)
				})
			})
			if testRuns.Replays() {
				fmt.Print(out)
			}
			return err
		},
	})
}

// captureStdout runs fn, passing what it prints on and returning it too.
func captureStdout(fn func() error) (string, error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(io.MultiWriter(&buf, orig), r)
		close(done)
	}()
	err = fn()
	w.Close()
	<-done
	os.Stdout = orig
	return buf.String(), err
}
