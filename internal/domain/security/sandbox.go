package security

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Sandbox errors
var (
	ErrSandboxDenied        = errors.New("sandbox: access denied")
	ErrSandboxNetworkBlocked = errors.New("sandbox: network access blocked")
)

type SandboxEnforcer interface {
	ApplyProfile(profile ProfileConfig, workspace string) error
	IsActive() bool
	LogViolation(target, operation string)
	Execute(cmd *exec.Cmd) error
}

type platformSandbox interface {
	apply(profile ProfileConfig, workspace string) error
	execute(cmd *exec.Cmd) error
}

// OSLevelSandbox 是沙箱执行器的门面，持有具体的实现
// 具体实现由 bootstrap 层注入（依赖倒置）
type OSLevelSandbox struct {
	mu         sync.Mutex
	active     bool
	profile    *ProfileConfig
	workspace  string
	denyEngine *DenyEngine
	audit      *AuditLogger
	platform   string
	applied    bool
	impl       platformSandbox
	enhanced   SandboxEnforcer // 使用接口而非具体类型
	useEnhanced bool
}

// NewOSLevelSandbox 创建沙箱执行器
// enhancedEnforcer 由 bootstrap 层注入（可为 nil）
func NewOSLevelSandbox(audit *AuditLogger, enhancedEnforcer SandboxEnforcer) *OSLevelSandbox {
	platform := runtime.GOOS
	s := &OSLevelSandbox{
		platform: platform,
		audit:    audit,
		enhanced: enhancedEnforcer,
	}

	if enhancedEnforcer != nil {
		s.useEnhanced = true
	} else {
		s.impl = newPlatformSandbox(platform, s)
		s.useEnhanced = false
	}

	return s
}

func (s *OSLevelSandbox) ApplyProfile(profile ProfileConfig, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.profile = &profile
	s.workspace = workspace

	// 优先使用增强沙箱
	if s.useEnhanced && s.enhanced != nil {
		if err := s.enhanced.ApplyProfile(profile, workspace); err != nil {
			// 增强沙箱失败，回退到传统沙箱
			s.useEnhanced = false
			s.enhanced = nil
			s.impl = newPlatformSandbox(s.platform, s)
		} else {
			s.active = true
			s.applied = true
			return nil
		}
	}

	// 传统沙箱逻辑
	if s.impl != nil {
		if err := s.impl.apply(profile, workspace); err != nil {
			return fmt.Errorf("sandbox apply: %w", err)
		}
	}

	cfg := DenyConfig{
		GlobPatterns: profile.Deny,
	}
	engine, err := NewDenyEngine(cfg)
	if err != nil {
		return fmt.Errorf("deny engine: %w", err)
	}
	s.denyEngine = engine
	s.active = true
	s.applied = true
	return nil
}

func (s *OSLevelSandbox) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.useEnhanced && s.enhanced != nil {
		return s.active && s.enhanced.IsActive()
	}
	return s.active
}

func (s *OSLevelSandbox) LogViolation(target, operation string) {
	if s.audit != nil {
		s.audit.Violation("sandbox", "access_denied", target, operation)
	}
}

func (s *OSLevelSandbox) Execute(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 优先使用增强沙箱
	if s.useEnhanced && s.enhanced != nil {
		return s.enhanced.Execute(cmd)
	}

	// 传统执行
	if s.impl != nil {
		return s.impl.execute(cmd)
	}
	return s.executeInProcess(cmd)
}

func (s *OSLevelSandbox) executeInProcess(cmd *exec.Cmd) error {
	if s.denyEngine != nil && cmd.Dir != "" {
		if s.denyEngine.IsDenied(cmd.Dir) {
			s.LogViolation(cmd.Dir, "execute")
			return ErrSandboxDenied
		}
		if !isUnderWorkspace(cmd.Dir, s.workspace) {
			s.LogViolation(cmd.Dir, "outside_workspace")
			return ErrSandboxDenied
		}
	}
	if s.profile != nil && s.profile.NetworkBlock {
		for _, arg := range cmd.Args {
			if looksNetworkCmd(cmd.Path, arg) {
				s.LogViolation(arg, "network_blocked")
				return ErrSandboxNetworkBlocked
			}
		}
		if looksNetworkCmd(cmd.Path, cmd.Path) {
			s.LogViolation(cmd.Path, "network_blocked")
			return ErrSandboxNetworkBlocked
		}
	}
	return cmd.Run()
}

func isUnderWorkspace(path, workspace string) bool {
	if workspace == "" {
		return true
	}
	absW, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	absP, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absP, absW)
}

func looksNetworkCmd(cmdPath, arg string) bool {
	networkCmds := map[string]bool{
		"curl": true, "wget": true, "ssh": true, "scp": true, "rsync": true,
		"nc": true, "netcat": true, "telnet": true, "ftp": true, "sftp": true,
		"dig": true, "nslookup": true, "host": true, "whois": true,
		"ping": true, "traceroute": true, "tracepath": true,
	}
	base := filepath.Base(cmdPath)
	if networkCmds[base] {
		return true
	}
	// 检查参数中的网络特征
	suspicious := []string{"http://", "https://", "ftp://", "://"}
	for _, s := range suspicious {
		if strings.Contains(arg, s) {
			return true
		}
	}
	return false
}

// ... 其余函数保持不变