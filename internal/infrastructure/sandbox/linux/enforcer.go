package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/security"
)

// SandboxCapabilities 沙箱能力
type SandboxCapabilities struct {
	LinuxLandlock   bool
	LinuxSeccomp    bool
	LinuxNamespaces bool
	LinuxCgroups    bool
}

// EnhancedSandboxEnforcer 增强的沙箱执行器（infrastructure 层实现）
type EnhancedSandboxEnforcer struct {
	mu       sync.RWMutex
	manager  *ManagedSandbox
	workspace string
	active    bool
	audit    *security.AuditLogger
}

// NewEnhancedSandboxEnforcer 创建增强的沙箱执行器
func NewEnhancedSandboxEnforcer(audit *security.AuditLogger) *EnhancedSandboxEnforcer {
	return &EnhancedSandboxEnforcer{
		audit: audit,
	}
}

// ApplyProfile 应用沙箱配置
func (e *EnhancedSandboxEnforcer) ApplyProfile(profile security.ProfileConfig, workspace string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 从配置中推断模式（基于 NetworkBlock 和其他特征）
	mode := security.ModeWorkspace
	if profile.NetworkBlock {
		mode = security.ModeStrict
	} else if len(profile.ReadWrite) == 0 {
		mode = security.ModeReadonly
	}

	var config *SandboxConfig
	switch mode {
	case security.ModeReadonly:
		config = ReadonlySandboxConfig(workspace)
	case security.ModeStrict:
		config = StrictSandboxConfig(workspace)
	case security.ModeDevbox:
		config = DevboxSandboxConfig(workspace)
	default:
		config = WorkspaceSandboxConfig(workspace)
	}

	// 合并网络配置
	if profile.NetworkBlock && config.Landlock != nil {
		config.Landlock.NetworkBlock = true
	}

	// 创建新的管理器
	e.manager = NewManagedSandbox(config)

	// 应用沙箱
	if err := e.manager.Apply(); err != nil {
		if e.audit != nil {
			e.audit.Warn(security.CategorySandbox, "sandbox", fmt.Sprintf("apply enhanced sandbox: %v", err))
		}
		return err
	}

	e.workspace = workspace
	e.active = true

	if e.audit != nil {
		e.audit.Info(security.CategorySandbox, "sandbox", fmt.Sprintf("Enhanced sandbox applied: mode=%s, workspace=%s", mode, workspace))
	}

	return nil
}

// IsActive 检查沙箱是否激活
func (e *EnhancedSandboxEnforcer) IsActive() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.active && e.manager != nil && e.manager.IsActive()
}

// Execute 在沙箱中执行命令
func (e *EnhancedSandboxEnforcer) Execute(cmd *exec.Cmd) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.active || e.manager == nil {
		return security.ErrSandboxDenied
	}

	ctx := context.Background()
	_, err := e.manager.ExecuteInSandboxWithEnv(
		ctx,
		cmd.Path,
		cmd.Args[1:],
		cmd.Env,
	)

	return err
}

// ExecuteWithContext 带上下文执行
func (e *EnhancedSandboxEnforcer) ExecuteWithContext(ctx context.Context, cmd *exec.Cmd) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.active || e.manager == nil {
		return security.ErrSandboxDenied
	}

	_, err := e.manager.ExecuteInSandboxWithEnv(
		ctx,
		cmd.Path,
		cmd.Args[1:],
		cmd.Env,
	)

	return err
}

// LogViolation 记录违规
func (e *EnhancedSandboxEnforcer) LogViolation(target, operation string) {
	if e.audit != nil {
		e.audit.Violation("sandbox", "access_denied", target, operation)
	}
}

// GetCapabilities 获取沙箱能力 (存根)
func (e *EnhancedSandboxEnforcer) GetCapabilities() *SandboxCapabilities {
	return &SandboxCapabilities{
		LinuxLandlock:   false,
		LinuxSeccomp:    false,
		LinuxNamespaces: false,
		LinuxCgroups:    false,
	}
}

// IsEnhancedAvailable 检查增强沙箱是否可用
func (e *EnhancedSandboxEnforcer) IsEnhancedAvailable() bool {
	return false
}

// GetSandboxMode 获取当前沙箱模式
func (e *EnhancedSandboxEnforcer) GetSandboxMode() security.SandboxMode {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.manager != nil {
		config := e.manager.GetConfig()
		if config != nil {
			return config.Mode
		}
	}
	return security.ModeWorkspace
}

// 编译时检查接口实现
var _ security.SandboxEnforcer = (*EnhancedSandboxEnforcer)(nil)