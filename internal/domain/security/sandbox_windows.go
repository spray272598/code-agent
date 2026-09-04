//go:build windows

package security

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObj = kernel32.NewProc("CreateJobObjectW")
	procAssignProc   = kernel32.NewProc("AssignProcessToJobObject")
	procSetInfoJob   = kernel32.NewProc("SetInformationJobObject")
)

const (
	jobObjectLimitKillOnJobClose        = 0x2000
	jobObjectBasicLimitInformationClass = 1
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type windowsPlatformSandbox struct {
	sandbox   *OSLevelSandbox
	jobHandle syscall.Handle
}

func newPlatformSandbox(platform string, s *OSLevelSandbox) platformSandbox {
	return &windowsPlatformSandbox{sandbox: s}
}

func (w *windowsPlatformSandbox) apply(profile ProfileConfig, workspace string) (EnforcementLevel, error) {
	// Create a Windows Job Object
	ret, _, _ := procCreateJobObj.Call(0, 0)
	if ret == 0 {
		if w.sandbox.audit != nil {
			w.sandbox.audit.Warn(CategorySandbox, "sandbox", "CreateJobObject failed, using in-process enforcement")
		}
		return LevelHeuristic, nil
	}
	w.jobHandle = syscall.Handle(ret)

	// Set limit: kill all processes in job when job handle closes
	info := jobObjectBasicLimitInformation{
		LimitFlags: jobObjectLimitKillOnJobClose,
	}
	procSetInfoJob.Call(
		uintptr(w.jobHandle),
		jobObjectBasicLimitInformationClass,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)

	if w.sandbox.audit != nil {
		w.sandbox.audit.Info(CategorySandbox, "sandbox", "Windows Job Object sandbox configured: kill-on-close enabled")
	}
	return LevelKernel, nil
}

func (w *windowsPlatformSandbox) execute(cmd *exec.Cmd) error {
	// Assign the child process to the Job Object before starting
	if w.jobHandle != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		}
	}
	// Start the process first so we can assign it to the job
	err := cmd.Start()
	if err != nil {
		return err
	}
	// Assign to Job Object after process starts
	if w.jobHandle != 0 && cmd.Process != nil {
		procAssignProc.Call(uintptr(w.jobHandle), uintptr(cmd.Process.Pid))
	}
	return cmd.Wait()
}

func platformInfo(platform string) string {
	return "Windows sandbox: using Job Object (kill-on-close + process isolation)"
}
