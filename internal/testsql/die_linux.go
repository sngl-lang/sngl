package testsql

import (
	"os/exec"
	"syscall"
)

// dieWithParent ends the server with the harness, so one a crashed run left
// does not hold the fixed port the next run starts its own on.
func dieWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
