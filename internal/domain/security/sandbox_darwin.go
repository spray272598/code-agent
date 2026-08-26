//go:build darwin

package security

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	seatbeltPath = "sandbox-exec"
)

type darwinPlatformSandbox struct {
	sandbox         *OSLevelSandbox
	profilePath     string
	usingSeatbelt   bool
}

func newPlatformSandbox(platform string, s *OSLevelSandbox) platformSandbox {
	return &darwinPlatformSandbox{sandbox: s}
}

func (d *darwinPlatformSandbox) apply(profile ProfileConfig, workspace string) error {
	hasSeatbelt := commandExists(seatbeltPath)

	if !hasSeatbelt {
		if d.sandbox.audit != nil {
			d.sandbox.audit.Warn(CategorySandbox, "sandbox", "sandbox-exec not found, using in-process enforcement")
		}
		return nil
	}

	profilePath := filepath.Join(os.TempDir(), "code-agent-sandbox.sbpl")
	content := generateSeatbeltProfile(profile, workspace)

	if err := os.WriteFile(profilePath, []byte(content), 0600); err != nil {
		if d.sandbox.audit != nil {
			d.sandbox.audit.Warn(CategorySandbox, "sandbox", "seatbelt profile write failed, using in-process enforcement")
		}
		return nil
	}

	d.profilePath = profilePath
	d.usingSeatbelt = true

	if d.sandbox.audit != nil {
		d.sandbox.audit.Info(CategorySandbox, "sandbox", fmt.Sprintf("macOS seatbelt profile written: %s", profilePath))
	}

	return nil
}

func (d *darwinPlatformSandbox) execute(cmd *exec.Cmd) error {
	if d.usingSeatbelt && d.profilePath != "" {
		return d.executeWithSeatbelt(cmd)
	}
	return d.sandbox.executeInProcess(cmd)
}

func (d *darwinPlatformSandbox) executeWithSeatbelt(cmd *exec.Cmd) error {
	sbCmd := exec.Command(seatbeltPath, "-f", d.profilePath, cmd.Path)
	sbCmd.Args = append(sbCmd.Args, cmd.Args...)
	sbCmd.Dir = cmd.Dir
	sbCmd.Env = cmd.Env
	sbCmd.Stdin = cmd.Stdin
	sbCmd.Stdout = cmd.Stdout
	sbCmd.Stderr = cmd.Stderr
	return sbCmd.Run()
}

func generateSeatbeltProfile(profile ProfileConfig, workspace string) string {
	var sb strings.Builder
	sb.WriteString(`(version 1.0)
(allow default)
`)

	sb.WriteString(`(allow file-read* (subpath "/usr"))` + "\n")
	sb.WriteString(`(allow file-read* (subpath "/bin"))` + "\n")
	sb.WriteString(`(allow file-read* (subpath "/System"))` + "\n")
	sb.WriteString(`(allow file-read* (subpath "/Library"))` + "\n")

	if workspace != "" {
		sb.WriteString(fmt.Sprintf(`(allow file-read* (subpath "%s"))`+"\n", workspace))
		sb.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`+"\n", workspace))
	}

	for _, dir := range profile.ReadOnly {
		expanded := expandPath(dir, workspace)
		if expanded == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf(`(allow file-read* (subpath "%s"))`+"\n", expanded))
	}

	for _, dir := range profile.ReadWrite {
		expanded := expandPath(dir, workspace)
		if expanded == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf(`(allow file-read* (subpath "%s"))`+"\n", expanded))
		sb.WriteString(fmt.Sprintf(`(allow file-write* (subpath "%s"))`+"\n", expanded))
	}

	for _, deny := range profile.Deny {
		expanded := expandPath(deny, workspace)
		if expanded == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf(`(deny file-write* (subpath "%s"))`+"\n", expanded))
		sb.WriteString(fmt.Sprintf(`(deny file-read* (subpath "%s"))`+"\n", expanded))
	}

	if profile.NetworkBlock {
		sb.WriteString("(deny network-outbound)\n")
	}

	return sb.String()
}

func commandExists(cmd string) bool {
	path, err := exec.LookPath(cmd)
	if err != nil {
		return false
	}
	return path != ""
}

func expandPath(path, workspace string) string {
	if strings.HasPrefix(path, "${workspace}") {
		return strings.ReplaceAll(path, "${workspace}", workspace)
	}
	return path
}
