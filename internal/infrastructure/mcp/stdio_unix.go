//go:build unix

package mcp

import "os/exec"

func hideMCPWindow(cmd *exec.Cmd) {
	// no-op on unix
	_ = cmd
}
