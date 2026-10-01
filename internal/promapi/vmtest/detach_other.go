//go:build !unix

package vmtest

import "os/exec"

func detach(*exec.Cmd) {}
