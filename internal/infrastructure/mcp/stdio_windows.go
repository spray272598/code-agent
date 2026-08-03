//go:build windows

package mcp

import (
	"os/exec"
	"syscall"
)

// hideMCPWindow prevents flash of console for stdio MCP servers on Windows.
func hideMCPWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// CREATE_NO_WINDOW = 0x08000000
	cmd.SysProcAttr.CreationFlags |= 0x08000000
	cmd.SysProcAttr.HideWindow = true
}
