package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"rsc.io/script"
)

// gobuildCmd is `go build` of the generated packages a script names, answered
// from the script's testrun/ record as a `sngl build` is: verify compiles
// nothing a record says built, and -update and -verify compile it. The
// binaries go to a directory of their own rather than the script's, whose
// tree is what the record's digest names and must not differ between a
// replay and a build.
func gobuildCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "go build packages, answered from the script's record",
			Args:    "pkgs...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			goCmd := func(args ...string) ([]byte, error) {
				c := exec.Command("go", args...)
				c.Dir = s.Getwd()
				c.Env = s.Environ()
				b, err := c.CombinedOutput()
				if err != nil {
					return nil, fmt.Errorf("go %s: %w\n%s", args[0], err, b)
				}
				return b, nil
			}
			build := func() (string, error) {
				// go build discards what it compiles for several packages,
				// and writes one package's binary unless -o says where.
				pkgs, err := goCmd(append([]string{"list"}, args...)...)
				if err != nil {
					return "", err
				}
				flags := []string{"build"}
				if len(strings.Fields(string(pkgs))) == 1 {
					flags = append(flags, "-o", os.DevNull)
				}
				_, err = goCmd(append(flags, args...)...)
				return "", err
			}
			_, err := testRuns.AnswerTool(path.Join("gobuild", strings.Join(args, "+")), s.Getwd(), args, build)
			return nil, err
		},
	)
}
