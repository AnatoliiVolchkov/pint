//go:build unix

package vmtest

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = new(syscall.SysProcAttr)
	cmd.SysProcAttr.Setpgid = true
}
