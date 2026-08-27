# 插件化架构与内核级沙箱实施计划

## 1. 项目概述

### 1.1 目标
为code-agent项目实现：
1. **插件化架构**: 支持热插拔、隔离性、可组合的插件系统
2. **内核级沙箱**: 使用Linux内核特性提供真正的安全隔离

### 1.2 参考项目
- **Grok Build**: Rust实现的AI编码代理，具有内核级沙箱
- **DeepSeek Harness**: TypeScript实现的插件化代理框架

---

## 2. 实施阶段

### 阶段1: 插件化架构核心（3周）

#### 第1周: 接口定义与基础结构
**任务:**
- [ ] 创建 `internal/domain/plugin/` 目录
- [ ] 定义 `Plugin` 接口和 `PluginContext`
- [ ] 定义 `ToolPlugin` 和 `LLMPlugin` 接口
- [ ] 实现 `PluginBase` 基础结构
- [ ] 编写接口测试

**产出:**
```go
// internal/domain/plugin/port.go
package plugin

type Plugin interface {
    ID() string
    Version() string
    Init(ctx *PluginContext) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Dependencies() []string
    Capabilities() []Capability
}

type PluginContext struct {
    Tools    *tool.MapRegistry
    LLM      LLMAdapter
    Storage  StorageProvider
    Security SecurityPolicy
    Config   ConfigManager
    Logger   Logger
    Events   EventBus
    Sandbox  Sandbox
}
```

#### 第2周: 插件加载器
**任务:**
- [ ] 实现 `FileLoader` 加载器
- [ ] 实现插件清单解析（JSON/YAML）
- [ ] 实现依赖检查
- [ ] 实现热加载/卸载
- [ ] 编写加载器测试

**产出:**
```go
// internal/infrastructure/plugin/loader.go
package plugin

type FileLoader struct {
    mu       sync.RWMutex
    plugins  map[string]Plugin
    manifests map[string]*PluginManifest
    ctx      *PluginContext
}

func (l *FileLoader) Load(ctx context.Context, path string) (Plugin, error) {
    // 1. 读取清单文件
    // 2. 检查依赖
    // 3. 加载插件
    // 4. 初始化插件
    // 5. 启动插件
    // 6. 注册到管理器
}
```

#### 第3周: 插件管理器
**任务:**
- [ ] 实现 `DefaultManager` 管理器
- [ ] 实现插件状态管理
- [ ] 实现事件回调
- [ ] 实现按能力查询
- [ ] 编写管理器测试

**产出:**
```go
// internal/domain/plugin/manager.go
package plugin

type DefaultManager struct {
    mu          sync.RWMutex
    loader      Loader
    plugins     map[string]Plugin
    info        map[string]*PluginInfo
    ctx         *PluginContext
    onLoaded    []func(Plugin)
    onUnloaded  []func(string)
}
```

---

### 阶段2: 现有工具迁移（2周）

#### 第4周: 工具插件适配器
**任务:**
- [ ] 实现 `ToolPluginAdapter` 适配器
- [ ] 创建 `CodingToolsPlugin`
- [ ] 创建 `MemoryPlugin`
- [ ] 创建 `SecurityPlugin`
- [ ] 编写适配器测试

**产出:**
```go
// internal/infrastructure/plugin/tool_adapter.go
package plugin

type ToolPluginAdapter struct {
    PluginBase
    tools    []tool.ITool
    metadata []tool.ToolMetadata
    registry *tool.MapRegistry
}

func (a *ToolPluginAdapter) Start(ctx context.Context) error {
    for i, t := range a.tools {
        var meta tool.ToolMetadata
        if i < len(a.metadata) {
            meta = a.metadata[i]
        } else {
            meta = tool.DefaultMeta(t, tool.CategoryExec)
        }
        a.registry.RegisterWithMeta(t, meta)
    }
    return nil
}
```

#### 第5周: 集成到Bootstrap
**任务:**
- [ ] 修改 `internal/bootstrap/app.go`
- [ ] 集成插件管理器
- [ ] 实现插件配置加载
- [ ] 实现插件发现
- [ ] 编写集成测试

**产出:**
```go
// internal/bootstrap/app.go 修改
func Build(cfg *config.Config) (*App, error) {
    // ... 现有代码 ...
    
    // 初始化插件管理器
    pluginCtx := &plugin.PluginContext{
        Tools:    reg,
        LLM:      llmAdapter,
        Storage:  storageProvider,
        Security: securityPolicy,
        Config:   configManager,
        Logger:   logger,
        Events:   eventBus,
        Sandbox:  sandbox,
    }
    
    loader := plugin.NewFileLoader(pluginCtx)
    pluginMgr := plugin.NewManager(loader, pluginCtx)
    
    // 加载插件
    if cfg.Plugins.Enabled {
        if err := pluginMgr.LoadFromDir(ctx, cfg.Plugins.Directory); err != nil {
            log.Printf("[bootstrap] load plugins: %v", err)
        }
    }
    
    // ... 现有代码 ...
}
```

---

### 阶段3: 内核级沙箱（4周）

#### 第6周: Landlock直接syscall
**任务:**
- [ ] 创建 `internal/infrastructure/sandbox/linux/` 目录
- [ ] 实现Landlock syscall封装
- [ ] 实现路径规则管理
- [ ] 实现访问控制逻辑
- [ ] 编写单元测试

**产出:**
```go
// internal/infrastructure/sandbox/linux/landlock.go
package sandbox

type LandlockSandbox struct {
    rulesetFD    int
    readOnly     []string
    readWrite    []string
    deny         []string
    networkBlock bool
}

func (l *LandlockSandbox) Apply() error {
    // 1. 创建ruleset
    // 2. 添加只读路径规则
    // 3. 添加读写路径规则
    // 4. 限制当前进程
    // 5. 关闭ruleset fd
}
```

#### 第7周: seccomp-bpf过滤器
**任务:**
- [ ] 实现BPF指令生成
- [ ] 实现系统调用白名单
- [ ] 实现条件规则
- [ ] 编写安全测试

**产出:**
```go
// internal/infrastructure/sandbox/linux/seccomp.go
package sandbox

type SeccompFilter struct {
    rules    []BPFInstr
    basePath []BPFInstr
}

func (f *SeccompFilter) Apply() error {
    // 1. 构建完整程序
    // 2. 转换为字节切片
    // 3. 应用seccomp
}
```

#### 第8周: Namespace隔离
**任务:**
- [ ] 实现Mount namespace
- [ ] 实现PID namespace
- [ ] 实现Network namespace
- [ ] 实现User namespace

**产出:**
```go
// internal/infrastructure/sandbox/linux/namespace.go
package sandbox

type NamespaceSandbox struct {
    mountNS  bool
    pidNS    bool
    ipcNS    bool
    utsNS    bool
    netNS    bool
    userNS   bool
}

func (n *NamespaceSandbox) Apply() error {
    // 1. 设置clone flags
    // 2. 使用unshare系统调用
    // 3. 设置用户namespace
    // 4. 设置挂载namespace
    // 5. 设置网络namespace
}
```

#### 第9周: Cgroups资源限制
**任务:**
- [ ] 实现内存限制
- [ ] 实现CPU限制
- [ ] 实现进程数限制
- [ ] 实现文件描述符限制

**产出:**
```go
// internal/infrastructure/sandbox/linux/cgroup.go
package sandbox

type CgroupSandbox struct {
    name         string
    path         string
    maxMemoryMB  int
    maxCPUPercent int
    maxProcesses  int
    maxOpenFiles  int
}

func (c *CgroupSandbox) Apply() error {
    // 1. 创建cgroup目录
    // 2. 设置内存限制
    // 3. 设置CPU限制
    // 4. 设置进程数限制
    // 5. 设置文件描述符限制
    // 6. 将当前进程加入cgroup
}
```

---

### 阶段4: 集成与测试（3周）

#### 第10周: 综合沙箱管理器
**任务:**
- [ ] 实现 `ManagedSandbox` 管理器
- [ ] 集成所有沙箱组件
- [ ] 实现配置管理
- [ ] 实现状态监控

**产出:**
```go
// internal/infrastructure/sandbox/linux/manager.go
package sandbox

type ManagedSandbox struct {
    mu          sync.RWMutex
    config      *EnhancedLandlockConfig
    landlock    *LandlockSandbox
    seccomp     *SeccompFilter
    namespace   *NamespaceSandbox
    cgroup      *CgroupSandbox
    active      bool
    applied     bool
}

func (m *ManagedSandbox) Apply() error {
    // 1. 应用Landlock
    // 2. 应用seccomp
    // 3. 应用Namespace
    // 4. 应用Cgroups
}
```

#### 第11周: 集成到Guard系统
**任务:**
- [ ] 修改 `internal/domain/security/permission.go`
- [ ] 集成增强沙箱管理器
- [ ] 实现配置转换
- [ ] 实现降级策略

**产出:**
```go
// internal/domain/security/sandbox_enhanced.go
package security

type EnhancedSandboxManager struct {
    config    *linux.EnhancedLandlockConfig
    sandbox   *linux.ManagedSandbox
    audit     *AuditLogger
}

func (m *EnhancedSandboxManager) ApplyProfile(profile ProfileConfig, workspace string) error {
    // 转换配置
    // 创建沙箱
    // 应用沙箱
}
```

#### 第12周: 测试与文档
**任务:**
- [ ] 编写单元测试
- [ ] 编写集成测试
- [ ] 进行安全审计
- [ ] 编写使用文档

**产出:**
- 单元测试覆盖率 > 80%
- 集成测试用例
- 安全审计报告
- 使用文档

---

## 3. 技术风险与缓解

### 3.1 风险: 内核版本不支持
**缓解:**
- 实现多级降级: Landlock → bwrap → 进程隔离
- 添加内核版本检测
- 提供详细的错误信息

### 3.2 风险: 性能影响
**缓解:**
- Landlock: 几乎无性能影响
- seccomp: 轻微性能影响，可优化BPF规则
- Namespace: 轻微性能影响
- Cgroups: 资源限制带来的性能约束

### 3.3 风险: 兼容性问题
**缓解:**
- 只在Linux上启用内核级沙箱
- 其他平台使用现有沙箱
- 添加平台检测和适配

---

## 4. 资源需求

### 4.1 开发资源
- **Go开发工程师**: 2人 × 12周
- **安全专家**: 1人 × 4周（安全审计）
- **测试工程师**: 1人 × 3周（测试）

### 4.2 基础设施
- **开发环境**: Linux虚拟机（内核5.13+）
- **测试环境**: 多平台测试（Linux/macOS/Windows）
- **CI/CD**: GitHub Actions

### 4.3 依赖库
- **Go标准库**: syscall, os, exec
- **第三方库**: 无（使用标准库实现）

---

## 5. 成功标准

### 5.1 插件化架构
- [ ] 支持热插拔插件
- [ ] 插件间隔离，崩溃不扩散
- [ ] 现有工具无缝迁移
- [ ] 插件加载时间 < 100ms
- [ ] 内存占用增加 < 10%

### 5.2 内核级沙箱
- [ ] Landlock文件系统访问控制正常工作
- [ ] seccomp系统调用过滤正常工作
- [ ] Namespace进程/网络隔离正常工作
- [ ] Cgroups资源限制正常工作
- [ ] 沙箱不可逆，无法绕过
- [ ] 降级策略正常工作

### 5.3 安全性
- [ ] 通过安全审计
- [ ] 无已知安全漏洞
- [ ] 符合最小权限原则
- [ ] 所有操作可审计

---

## 6. 后续规划

### 6.1 短期（1-3个月）
- 实现插件市场
- 实现插件签名验证
- 增强Windows沙箱（使用AppContainer）

### 6.2 中期（3-6个月）
- 实现插件热更新
- 实现插件版本管理
- 增强macOS沙箱（使用EndpointSecurity）

### 6.3 长期（6-12个月）
- 实现分布式插件系统
- 实现插件性能监控
- 实现插件自动修复
