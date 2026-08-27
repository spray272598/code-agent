package sandbox

import (
	"fmt"
	"path/filepath"
)

// =============================================================================
// Cgroups 资源限制实现 (简化版)
// =============================================================================

const (
	// cgroup v2 路径
	cgroupV2Path = "/sys/fs/cgroup"
)

// CgroupConfig Cgroups 配置
type CgroupConfig struct {
	// 内存限制（字节）
	MaxMemoryBytes int64

	// CPU 配额（微秒/周期）
	CPUQuotaUs int64

	// CPU 周期（微秒）
	CPUPeriodUs int64

	// CPU 核心限制
	CPUS string // 例如 "0-3" 或 "0,1"

	// 内存交换限制
	MaxSwapBytes int64

	// 进程数限制
	MaxProcesses int

	// 文件描述符限制
	MaxOpenFiles uint64

	// I/O 限制
	IOReadBps  uint64 // 读取字节/秒
	IOWriteBps uint64 // 写入字节/秒
	IOReadIops uint64 // 读取 IOPS
	IOWriteIops uint64 // 写入 IOPS
}

// DefaultCgroupConfig 默认 Cgroups 配置
func DefaultCgroupConfig() *CgroupConfig {
	return &CgroupConfig{
		MaxMemoryBytes:  512 * 1024 * 1024, // 512MB
		CPUQuotaUs:      50000,             // 50% CPU (period=100000)
		CPUPeriodUs:     100000,            // 100ms
		MaxProcesses:    32,
		MaxOpenFiles:    1024,
	}
}

// StrictCgroupConfig 严格 Cgroups 配置
func StrictCgroupConfig() *CgroupConfig {
	return &CgroupConfig{
		MaxMemoryBytes:  256 * 1024 * 1024, // 256MB
		CPUQuotaUs:      25000,             // 25% CPU
		CPUPeriodUs:     100000,
		MaxProcesses:    16,
		MaxOpenFiles:    512,
	}
}

// DevCgroupConfig 开发环境 Cgroups 配置
func DevCgroupConfig() *CgroupConfig {
	return &CgroupConfig{
		MaxMemoryBytes:  2 * 1024 * 1024 * 1024, // 2GB
		CPUQuotaUs:      100000,                  // 100% CPU
		CPUPeriodUs:     100000,
		MaxProcesses:    128,
		MaxOpenFiles:    4096,
	}
}

// CgroupSandbox Cgroups 沙箱
type CgroupSandbox struct {
	name   string
	path   string
	config *CgroupConfig
	applied bool
}

// NewCgroupSandbox 创建 Cgroups 沙箱
func NewCgroupSandbox(name string, config *CgroupConfig) *CgroupSandbox {
	if config == nil {
		config = DefaultCgroupConfig()
	}
	return &CgroupSandbox{
		name:   name,
		path:   filepath.Join(cgroupV2Path, "code-agent", name),
		config: config,
	}
}

// Apply 应用 Cgroups 限制 (存根实现)
func (c *CgroupSandbox) Apply() error {
	if c.applied {
		return fmt.Errorf("cgroup already applied")
	}

	// 存根实现：实际应用中需要创建 cgroup 目录并写入配置
	c.applied = true
	return nil
}

// IsApplied 检查 cgroup 是否已应用
func (c *CgroupSandbox) IsApplied() bool {
	return c.applied
}

// GetPath 获取 cgroup 路径
func (c *CgroupSandbox) GetPath() string {
	return c.path
}

// GetConfig 获取配置
func (c *CgroupSandbox) GetConfig() *CgroupConfig {
	return c.config
}

// UpdateConfig 更新配置
func (c *CgroupSandbox) UpdateConfig(config *CgroupConfig) error {
	if !c.applied {
		c.config = config
		return nil
	}
	c.config = config
	return nil
}

// Cleanup 清理 cgroup
func (c *CgroupSandbox) Cleanup() error {
	if !c.applied {
		return nil
	}
	c.applied = false
	return nil
}

// SetMemoryLimit 设置内存限制
func (c *CgroupSandbox) SetMemoryLimit(bytes int64) error {
	c.config.MaxMemoryBytes = bytes
	return nil
}

// SetCPULimit 设置 CPU 限制
func (c *CgroupSandbox) SetCPULimit(quotaUs, periodUs int64) error {
	c.config.CPUQuotaUs = quotaUs
	c.config.CPUPeriodUs = periodUs
	return nil
}

// SetProcessLimit 设置进程数限制
func (c *CgroupSandbox) SetProcessLimit(max int) error {
	c.config.MaxProcesses = max
	return nil
}

// GetMemoryUsage 获取内存使用量 (存根)
func (c *CgroupSandbox) GetMemoryUsage() (int64, error) {
	return 0, nil
}

// GetCPUUsage 获取 CPU 使用统计 (存根)
func (c *CgroupSandbox) GetCPUUsage() (user, system uint64, err error) {
	return 0, 0, nil
}

// GetProcessCount 获取进程数 (存根)
func (c *CgroupSandbox) GetProcessCount() (int, error) {
	return 0, nil
}