//go:build !linux

package testsql

import "os/exec"

func dieWithParent(*exec.Cmd) {}
