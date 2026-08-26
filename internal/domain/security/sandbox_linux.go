//go:build linux

package security

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	bwrapPath    = "bwrap"
	landlockPath = "landlock"
)

type LandlockConfig struct {
	ReadOnlyPaths  []string
	ReadWritePaths []string
	DenyPaths      []string
	NetworkBlock   bool
}

type linuxPlatformSandbox struct {
	sandbox       *OSLevelSandbox
	bwrapArgs     []string
	usingBwrap    bool
	landlockCfg   *LandlockConfig
	usingLandlock bool
}

func newPlatformSandbox(platform string, s *OSLevelSandbox) platformSandbox {
	return &linuxPlatformSandbox{sandbox: s}
}

func (l *linuxPlatformSandbox) apply(profile ProfileConfig, workspace string) error {
	hasBwrap := commandExists(bwrapPath)
	hasLandlock := commandExists(landlockPath)

	if !hasBwrap && !hasLandlock {
		if l.sandbox.audit != nil {
			l.sandbox.audit.Warn(CategorySandbox, "sandbox", "neither bwrap nor landlock found, using in-process enforcement")
		}
		return nil
	}

	if hasLandlock {
		return l.applyLandlock(profile, workspace)
	}

	return l.applyBwrap(profile, workspace)
}

func (l *linuxPlatformSandbox) applyBwrap(profile ProfileConfig, workspace string) error {
	args := []string{
		"--dev",
		"--proc", "/proc",
		"--clearenv",
		"--setenv", "PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--setenv", "HOME", "/root",
	}

	args = append(args, "--ro-bind", "/etc", "/etc")
	args = append(args, "--ro-bind", "/usr", "/usr")
	args = append(args, "--ro-bind", "/bin", "/bin")
	args = append(args, "--ro-bind", "/lib", "/lib")
	args = append(args, "--ro-bind", "/lib64", "/lib64")
	args = append(args, "--ro-bind", "/opt", "/opt")

	if workspace != "" {
		ws := filepath.Clean(workspace)
		args = append(args, "--bind", ws, ws)
	}

	for _, dir := range profile.ReadOnly {
		expanded := expandPath(dir, workspace)
		if expanded == "" {
			continue
		}
		args = append(args, "--ro-bind", expanded, expanded)
	}

	for _, dir := range profile.ReadWrite {
		expanded := expandPath(dir, workspace)
		if expanded == "" {
			continue
		}
		args = append(args, "--bind", expanded, expanded)
	}

	for _, deny := range profile.Deny {
		expanded := expandPath(deny, workspace)
		if expanded == "" {
			continue
		}
		args = append(args, "--ro-bind", expanded, "/dev/null")
	}

	if profile.NetworkBlock {
		args = append(args, "--unshare", "net")
	}

	l.bwrapArgs = args
	l.usingBwrap = true

	if l.sandbox.audit != nil {
		l.sandbox.audit.Info(CategorySandbox, "sandbox", fmt.Sprintf("Linux bwrap sandbox configured: %d args", len(args)))
	}

	return nil
}

func (l *linuxPlatformSandbox) applyLandlock(profile ProfileConfig, workspace string) error {
	if !isLandlockAvailable() {
		return l.applyBwrap(profile, workspace)
	}

	workspacePath := filepath.Clean(workspace)
	roPaths := []string{"/etc", "/usr", "/bin", "/lib", "/lib64", "/opt"}
	rwPaths := []string{}
	denyPaths := []string{}

	if workspacePath != "" {
		rwPaths = append(rwPaths, workspacePath)
	}

	for _, dir := range profile.ReadOnly {
		expanded := expandPath(dir, workspace)
		if expanded != "" {
			roPaths = append(roPaths, expanded)
		}
	}

	for _, dir := range profile.ReadWrite {
		expanded := expandPath(dir, workspace)
		if expanded != "" {
			rwPaths = append(rwPaths, expanded)
		}
	}

	for _, deny := range profile.Deny {
		expanded := expandPath(deny, workspace)
		if expanded != "" {
			denyPaths = append(denyPaths, expanded)
		}
	}

	l.landlockCfg = &LandlockConfig{
		ReadOnlyPaths:  roPaths,
		ReadWritePaths: rwPaths,
		DenyPaths:      denyPaths,
		NetworkBlock:   profile.NetworkBlock,
	}
	l.usingLandlock = true

	if l.sandbox.audit != nil {
		l.sandbox.audit.Info(CategorySandbox, "sandbox", fmt.Sprintf("Linux landlock sandbox configured: ro=%d rw=%d deny=%d", len(roPaths), len(rwPaths), len(denyPaths)))
	}

	return nil
}

func (l *linuxPlatformSandbox) execute(cmd *exec.Cmd) error {
	if l.usingBwrap && len(l.bwrapArgs) > 0 {
		return l.executeWithBwrap(cmd)
	}
	if l.usingLandlock && l.landlockCfg != nil {
		return l.executeWithLandlock(cmd)
	}
	return l.sandbox.executeInProcess(cmd)
}

func (l *linuxPlatformSandbox) executeWithBwrap(cmd *exec.Cmd) error {
	bwrapCmd := exec.Command(bwrapPath, l.bwrapArgs...)
	bwrapCmd.Args = append(bwrapCmd.Args, cmd.Path)
	bwrapCmd.Args = append(bwrapCmd.Args, cmd.Args...)
	bwrapCmd.Dir = cmd.Dir
	bwrapCmd.Env = cmd.Env
	bwrapCmd.Stdin = cmd.Stdin
	bwrapCmd.Stdout = cmd.Stdout
	bwrapCmd.Stderr = cmd.Stderr
	return bwrapCmd.Run()
}

func (l *linuxPlatformSandbox) executeWithLandlock(cmd *exec.Cmd) error {
	llCmd := exec.Command(landlockPath)
	llCmd.Args = append(llCmd.Args,
		"--ro-prefix=/etc",
		"--ro-prefix=/usr",
		"--ro-prefix=/bin",
		"--ro-prefix=/lib",
		"--ro-prefix=/lib64",
		"--ro-prefix=/opt",
	)
	for _, p := range l.landlockCfg.ReadOnlyPaths {
		llCmd.Args = append(llCmd.Args, "--ro-prefix="+p)
	}
	for _, p := range l.landlockCfg.ReadWritePaths {
		llCmd.Args = append(llCmd.Args, "--rw-prefix="+p)
	}
	for _, p := range l.landlockCfg.DenyPaths {
		llCmd.Args = append(llCmd.Args, "--deny-prefix="+p)
	}
	if l.landlockCfg.NetworkBlock {
		llCmd.Args = append(llCmd.Args, "--no-net")
	}
	llCmd.Args = append(llCmd.Args, cmd.Path)
	llCmd.Args = append(llCmd.Args, cmd.Args...)
	llCmd.Dir = cmd.Dir
	llCmd.Env = cmd.Env
	llCmd.Stdin = cmd.Stdin
	llCmd.Stdout = cmd.Stdout
	llCmd.Stderr = cmd.Stderr
	return llCmd.Run()
}

func commandExists(cmd string) bool {
	path, err := exec.LookPath(cmd)
	if err != nil {
		return false
	}
	return path != ""
}

func isLandlockAvailable() bool {
	return commandExists(landlockPath)
}

func expandPath(path, workspace string) string {
	if strings.HasPrefix(path, "${workspace}") {
		return strings.ReplaceAll(path, "${workspace}", workspace)
	}
	return path
}
