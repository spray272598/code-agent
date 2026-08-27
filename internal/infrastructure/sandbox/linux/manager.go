package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/security"
)

// =============================================================================
// 综合沙箱管理器 (简化版)
// =============================================================================

// SandboxConfig 综合沙箱配置
type SandboxConfig struct {
	// 沙箱模式
	Mode security.SandboxMode

	// Landlock 配置
	Landlock *LandlockConfig

	// seccomp 配置
	Seccomp *SeccompConfig

	// Namespace 配置
	Namespace *NamespaceConfig

	// Cgroups 配置
	Cgroup *CgroupConfig

	// 工作区路径
	Workspace string
}

// LandlockConfig Landlock 配置
type LandlockConfig struct {
	ReadOnlyPaths  []string
	ReadWritePaths []string
	DenyPaths      []string
	NetworkBlock   bool
}

// SeccompConfig seccomp 配置
type SeccompConfig struct {
	Enabled       bool
	AllowedSyscalls []int
	DeniedSyscalls  []int
	DefaultAction string // "allow" or "deny"
}

// ManagedSandbox 综合沙箱管理器 (简化版)
type ManagedSandbox struct {
	mu       sync.RWMutex
	config   *SandboxConfig
	landlock *LandlockSandbox
	seccomp  *SeccompFilter
	namespace *NamespaceSandbox
	cgroup   *CgroupSandbox
	active   bool
	applied  bool
}

// NewManagedSandbox 创建综合沙箱
func NewManagedSandbox(config *SandboxConfig) *ManagedSandbox {
	if config == nil {
		config = DefaultSandboxConfig()
	}

	// 创建组件
	landlock := NewLandlockSandbox()
	seccomp := NewSeccompFilter()
	namespace := NewNamespaceSandbox(config.Namespace)
	cgroup := NewCgroupSandbox("agent-sandbox", config.Cgroup)

	return &ManagedSandbox{
		config:    config,
		landlock:  landlock,
		seccomp:   seccomp,
		namespace: namespace,
		cgroup:    cgroup,
	}
}

// DefaultSandboxConfig 默认沙箱配置
func DefaultSandboxConfig() *SandboxConfig {
	return &SandboxConfig{
		Mode:      security.ModeWorkspace,
		Landlock:  &LandlockConfig{},
		Seccomp:   &SeccompConfig{Enabled: true},
		Namespace: DefaultNamespaceConfig(),
		Cgroup:    DefaultCgroupConfig(),
	}
}

// WorkspaceSandboxConfig 工作区沙箱配置
func WorkspaceSandboxConfig(workspace string) *SandboxConfig {
	return &SandboxConfig{
		Mode:      security.ModeWorkspace,
		Landlock: &LandlockConfig{
			ReadWritePaths: []string{workspace},
			ReadOnlyPaths:  []string{"/usr", "/bin", "/lib", "/lib64", "/etc"},
		},
		Seccomp:   &SeccompConfig{Enabled: true},
		Namespace: DefaultNamespaceConfig(),
		Cgroup:    DefaultCgroupConfig(),
		Workspace: workspace,
	}
}

// ReadonlySandboxConfig 只读沙箱配置
func ReadonlySandboxConfig(workspace string) *SandboxConfig {
	return &SandboxConfig{
		Mode:      security.ModeReadonly,
		Landlock: &LandlockConfig{
			ReadOnlyPaths:  []string{workspace, "/usr", "/bin", "/lib", "/lib64", "/etc"},
			ReadWritePaths: []string{},
		},
		Seccomp:   &SeccompConfig{Enabled: true},
		Namespace: StrictNamespaceConfig(),
		Cgroup:    StrictCgroupConfig(),
		Workspace: workspace,
	}
}

// StrictSandboxConfig 严格沙箱配置
func StrictSandboxConfig(workspace string) *SandboxConfig {
	return &SandboxConfig{
		Mode:      security.ModeStrict,
		Landlock: &LandlockConfig{
			ReadOnlyPaths:  []string{"/usr", "/bin", "/lib", "/lib64", "/etc"},
			ReadWritePaths: []string{workspace},
			NetworkBlock:   true,
		},
		Seccomp:   &SeccompConfig{Enabled: true},
		Namespace: StrictNamespaceConfig(),
		Cgroup:    StrictCgroupConfig(),
		Workspace: workspace,
	}
}

// DevboxSandboxConfig 开发环境沙箱配置
func DevboxSandboxConfig(workspace string) *SandboxConfig {
	return &SandboxConfig{
		Mode:      security.ModeDevbox,
		Landlock: &LandlockConfig{
			ReadOnlyPaths:  []string{"/usr", "/bin", "/lib", "/lib64", "/etc"},
			ReadWritePaths: []string{workspace},
		},
		Seccomp:   &SeccompConfig{Enabled: true},
		Namespace: MinimalNamespaceConfig(),
		Cgroup:    DevCgroupConfig(),
		Workspace: workspace,
	}
}

// Apply 应用所有沙箱限制 (简化版)
func (m *ManagedSandbox) Apply() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.applied {
		return fmt.Errorf("sandbox already applied")
	}

	// 1. 应用 Landlock（文件系统限制）
	if err := m.applyLandlock(); err != nil {
		return fmt.Errorf("apply landlock: %w", err)
	}

	// 2. 应用 seccomp（系统调用过滤） - 存根
	if err := m.applySeccomp(); err != nil {
		return fmt.Errorf("apply seccomp: %w", err)
	}

	// 3. 应用 Namespace（进程/网络隔离） - 存根
	if err := m.applyNamespace(); err != nil {
		return fmt.Errorf("apply namespace: %w", err)
	}

	// 4. 应用 Cgroups（资源限制） - 存根
	if err := m.applyCgroup(); err != nil {
		return fmt.Errorf("apply cgroup: %w", err)
	}

	m.applied = true
	m.active = true

	return nil
}

// applyLandlock 应用 Landlock 限制
func (m *ManagedSandbox) applyLandlock() error {
	if m.config.Landlock == nil {
		return nil
	}

	// 设置路径
	m.landlock.SetReadOnlyPaths(m.config.Landlock.ReadOnlyPaths)
	m.landlock.SetReadWritePaths(m.config.Landlock.ReadWritePaths)
	m.landlock.SetDenyPaths(m.config.Landlock.DenyPaths)
	m.landlock.SetNetworkBlock(m.config.Landlock.NetworkBlock)

	return m.landlock.Apply()
}

// applySeccomp 应用 seccomp 过滤 (存根)
func (m *ManagedSandbox) applySeccomp() error {
	if m.config.Seccomp == nil || !m.config.Seccomp.Enabled {
		return nil
	}
	// 存根实现
	return nil
}

// applyNamespace 应用 Namespace 隔离 (存根)
func (m *ManagedSandbox) applyNamespace() error {
	if m.config.Namespace == nil {
		return nil
	}
	return m.namespace.Apply()
}

// applyCgroup 应用 Cgroups 限制 (存根)
func (m *ManagedSandbox) applyCgroup() error {
	if m.config.Cgroup == nil {
		return nil
	}
	return m.cgroup.Apply()
}

// ExecuteInSandbox 在沙箱中执行命令
func (m *ManagedSandbox) ExecuteInSandbox(ctx context.Context, cmd string, args []string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.active {
		return nil, fmt.Errorf("sandbox not active")
	}

	// 创建命令
	command := exec.CommandContext(ctx, cmd, args...)

	// 设置工作目录
	if m.config.Workspace != "" {
		command.Dir = m.config.Workspace
	}

	// 执行命令
	return command.CombinedOutput()
}

// ExecuteInSandboxWithEnv 在沙箱中执行命令（带环境变量）
func (m *ManagedSandbox) ExecuteInSandboxWithEnv(ctx context.Context, cmd string, args []string, env []string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.active {
		return nil, fmt.Errorf("sandbox not active")
	}

	command := exec.CommandContext(ctx, cmd, args...)
	if m.config.Workspace != "" {
		command.Dir = m.config.Workspace
	}
	if len(env) > 0 {
		command.Env = env
	}

	return command.CombinedOutput()
}

// IsActive 检查沙箱是否激活
func (m *ManagedSandbox) IsActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// IsApplied 检查沙箱是否已应用
func (m *ManagedSandbox) IsApplied() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.applied
}

// GetLandlock 获取 Landlock 沙箱
func (m *ManagedSandbox) GetLandlock() *LandlockSandbox {
	return m.landlock
}

// GetSeccomp 获取 seccomp 过滤器
func (m *ManagedSandbox) GetSeccomp() *SeccompFilter {
	return m.seccomp
}

// GetNamespace 获取 Namespace 沙箱
func (m *ManagedSandbox) GetNamespace() *NamespaceSandbox {
	return m.namespace
}

// GetCgroup 获取 Cgroup 沙箱
func (m *ManagedSandbox) GetCgroup() *CgroupSandbox {
	return m.cgroup
}

// GetConfig 获取配置
func (m *ManagedSandbox) GetConfig() *SandboxConfig {
	return m.config
}

// Deactivate 停用沙箱
func (m *ManagedSandbox) Deactivate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = false
}

// Cleanup 清理资源
func (m *ManagedSandbox) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error

	// 清理 cgroup
	if m.cgroup != nil && m.cgroup.IsApplied() {
		if err := m.cgroup.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("cleanup cgroup: %w", err))
		}
	}

	// 清理 Landlock
	if m.landlock != nil && m.landlock.IsApplied() {
		if err := m.landlock.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close landlock: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}

	return nil
}