//go:build !linux && !darwin && !windows

package security

import (
	"os/exec"
)

type defaultPlatformSandbox struct {
	sandbox *OSLevelSandbox
}

func newPlatformSandbox(platform string, s *OSLevelSandbox) platformSandbox {
	return &defaultPlatformSandbox{sandbox: s}
}

func (d *defaultPlatformSandbox) apply(profile ProfileConfig, workspace string) error {
	if d.sandbox.audit != nil {
		d.sandbox.audit.Info(CategorySandbox, "sandbox", platformInfo(d.sandbox.platform))
	}
	return nil
}

func (d *defaultPlatformSandbox) execute(cmd *exec.Cmd) error {
	return d.sandbox.executeInProcess(cmd)
}

func platformInfo(platform string) string {
	switch platform {
	case "windows":
		return "Windows sandbox: using in-process enforcement (Job Object support via process attributes)"
	default:
		return "sandbox: using in-process enforcement"
	}
}
