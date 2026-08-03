//go:build windows

package coding

import (
	"os/exec"
	"syscall"
)

func setProcessIsolate(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// CREATE_NEW_PROCESS_GROUP = 0x00000200
	cmd.SysProcAttr.CreationFlags |= 0x00000200
}
