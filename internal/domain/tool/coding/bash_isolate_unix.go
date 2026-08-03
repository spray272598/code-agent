//go:build unix

package coding

import (
	"os/exec"
	"syscall"
)

func setProcessIsolate(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// new session / process group so agent can kill the group without dying
	cmd.SysProcAttr.Setpgid = true
}
