# 插件化架构设计文档

## 1. 设计目标

### 1.1 核心目标
- **热插拔**: 运行时动态加载/卸载插件，无需重启
- **隔离性**: 插件间互不影响，崩溃不扩散
- **可组合**: 插件可依赖其他插件，支持组合使用
- **向后兼容**: 现有工具可无缝迁移为插件

### 1.2 设计原则
- **依赖反转**: 插件依赖抽象接口，不依赖具体实现
- **单一职责**: 每个插件只做一件事
- **显式声明**: 插件必须声明依赖和能力
- **安全优先**: 插件运行在沙箱中，受权限控制

---

## 2. 核心接口设计

### 2.1 插件接口
```go
// internal/domain/plugin/port.go

package plugin

import (
    "context"
    "github.com/spray272598/code-agent/internal/domain/tool"
)

// Plugin 是所有插件必须实现的核心接口
type Plugin interface {
    // ID 返回插件唯一标识
    ID() string
    
    // Version 返回插件版本
    Version() string
    
    // Init 初始化插件，注入依赖
    Init(ctx *PluginContext) error
    
    // Start 启动插件
    Start(ctx context.Context) error
    
    // Stop 停止插件
    Stop(ctx context.Context) error
    
    // Dependencies 返回插件依赖的其他插件ID
    Dependencies() []string
    
    // Capabilities 返回插件提供的能力
    Capabilities() []Capability
}

// Capability 插件提供的能力类型
type Capability string

const (
    CapabilityTool     Capability = "tool"     // 工具能力
    CapabilityLLM      Capability = "llm"      // LLM适配器
    CapabilityStorage  Capability = "storage"  // 存储后端
    CapabilitySecurity Capability = "security" // 安全策略
    CapabilityUI       Capability = "ui"       // UI组件
)

// PluginContext 插件运行时上下文
type PluginContext struct {
    // 工具注册表
    Tools *tool.MapRegistry
    
    // LLM适配器
    LLM LLMAdapter
    
    // 存储接口
    Storage StorageProvider
    
    // 安全策略
    Security SecurityPolicy
    
    // 配置管理
    Config ConfigManager
    
    // 日志接口
    Logger Logger
    
    // 事件总线
    Events EventBus
    
    // 沙箱接口
    Sandbox Sandbox
}

// LLMAdapter LLM适配器接口
type LLMAdapter interface {
    Generate(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
    Stream(ctx context.Context, req *LLMRequest) (<-chan *LLMChunk, error)
}

// StorageProvider 存储提供者接口
type StorageProvider interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]string, error)
}

// SecurityPolicy 安全策略接口
type SecurityPolicy interface {
    CheckPermission(ctx context.Context, action string, resource string) (bool, error)
    EnforceSandbox(ctx context.Context, config SandboxConfig) error
}

// ConfigManager 配置管理器接口
type ConfigManager interface {
    Get(key string) (any, error)
    Set(key string, value any) error
    Watch(key string, callback func(any)) error
}

// Logger 日志接口
type Logger interface {
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
}

// EventBus 事件总线接口
type EventBus interface {
    Emit(event string, data any) error
    On(event string, handler func(any)) error
    Off(event string, handler func(any)) error
}

// Sandbox 沙箱接口
type Sandbox interface {
    Execute(ctx context.Context, cmd string, args []string) ([]byte, error)
    Restrict(config SandboxConfig) error
}

// SandboxConfig 沙箱配置
type SandboxConfig struct {
    ReadOnly      bool     `json:"readOnly"`
    NetworkBlock  bool     `json:"networkBlock"`
    AllowedPaths  []string `json:"allowedPaths"`
    DeniedPaths   []string `json:"deniedPaths"`
    MaxMemoryMB   int      `json:"maxMemoryMB"`
    MaxCPUPercent int      `json:"maxCPUPercent"`
}
```

### 2.2 工具插件接口
```go
// internal/domain/plugin/tool_plugin.go

package plugin

import (
    "context"
    "github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolPlugin 工具插件接口，扩展基础Plugin
type ToolPlugin interface {
    Plugin
    
    // Tools 返回插件提供的工具列表
    Tools() []tool.ITool
    
    // ToolMetadata 返回工具元数据
    ToolMetadata() []tool.ToolMetadata
}

// ToolPluginBase 工具插件基础实现
type ToolPluginBase struct {
    PluginBase
    tools    []tool.ITool
    metadata []tool.ToolMetadata
}

func (p *ToolPluginBase) Tools() []tool.ITool {
    return p.tools
}

func (p *ToolPluginBase) ToolMetadata() []tool.ToolMetadata {
    return p.metadata
}

func (p *ToolPluginBase) RegisterTool(t tool.ITool, meta tool.ToolMetadata) {
    p.tools = append(p.tools, t)
    p.metadata = append(p.metadata, meta)
    if p.ctx != nil {
        p.ctx.Tools.RegisterWithMeta(t, meta)
    }
}
```

### 2.3 LLM插件接口
```go
// internal/domain/plugin/llm_plugin.go

package plugin

import (
    "context"
)

// LLMPlugin LLM适配器插件接口
type LLMPlugin interface {
    Plugin
    
    // Adapter 返回LLM适配器
    Adapter() LLMAdapter
    
    // Models 返回支持的模型列表
    Models() []ModelInfo
}

// ModelInfo 模型信息
type ModelInfo struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Provider    string `json:"provider"`
    MaxTokens   int    `json:"maxTokens"`
    SupportsTool bool   `json:"supportsTool"`
    SupportsStream bool `json:"supportsStream"`
}
```

---

## 3. 插件加载器设计

### 3.1 加载器接口
```go
// internal/domain/plugin/loader.go

package plugin

import (
    "context"
    "path/filepath"
)

// Loader 插件加载器接口
type Loader interface {
    // Load 从指定路径加载插件
    Load(ctx context.Context, path string) (Plugin, error)
    
    // LoadFromDir 从目录加载所有插件
    LoadFromDir(ctx context.Context, dir string) ([]Plugin, error)
    
    // Unload 卸载插件
    Unload(ctx context.Context, id string) error
    
    // Reload 重新加载插件
    Reload(ctx context.Context, id string) error
    
    // List 列出已加载的插件
    List() []Plugin
    
    // Get 获取指定插件
    Get(id string) (Plugin, bool)
}

// PluginManifest 插件清单文件
type PluginManifest struct {
    ID          string            `json:"id" yaml:"id"`
    Version     string            `json:"version" yaml:"version"`
    Name        string            `json:"name" yaml:"name"`
    Description string            `json:"description" yaml:"description"`
    Author      string            `json:"author" yaml:"author"`
    Entry       string            `json:"entry" yaml:"entry"`       // 入口文件
    Type        string            `json:"type" yaml:"type"`         // tool, llm, storage, etc.
    Dependencies []string         `json:"dependencies" yaml:"dependencies"`
    Capabilities []string         `json:"capabilities" yaml:"capabilities"`
    Config      map[string]any    `json:"config" yaml:"config"`
    Permissions []string          `json:"permissions" yaml:"permissions"` // 所需权限
}
```

### 3.2 加载器实现
```go
// internal/infrastructure/plugin/loader.go

package plugin

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "plugin"
    "sync"
)

// FileLoader 基于文件系统的插件加载器
type FileLoader struct {
    mu       sync.RWMutex
    plugins  map[string]Plugin
    manifests map[string]*PluginManifest
    ctx      *PluginContext
}

func NewFileLoader(ctx *PluginContext) *FileLoader {
    return &FileLoader{
        plugins:  make(map[string]Plugin),
        manifests: make(map[string]*PluginManifest),
        ctx:      ctx,
    }
}

func (l *FileLoader) Load(ctx context.Context, path string) (Plugin, error) {
    // 1. 读取清单文件
    manifest, err := l.loadManifest(path)
    if err != nil {
        return nil, fmt.Errorf("load manifest: %w", err)
    }
    
    // 2. 检查依赖
    if err := l.checkDependencies(manifest); err != nil {
        return nil, fmt.Errorf("check dependencies: %w", err)
    }
    
    // 3. 加载插件
    p, err := l.loadPlugin(ctx, manifest)
    if err != nil {
        return nil, fmt.Errorf("load plugin: %w", err)
    }
    
    // 4. 初始化插件
    if err := p.Init(l.ctx); err != nil {
        return nil, fmt.Errorf("init plugin: %w", err)
    }
    
    // 5. 启动插件
    if err := p.Start(ctx); err != nil {
        return nil, fmt.Errorf("start plugin: %w", err)
    }
    
    // 6. 注册到管理器
    l.mu.Lock()
    l.plugins[p.ID()] = p
    l.manifests[p.ID()] = manifest
    l.mu.Unlock()
    
    return p, nil
}

func (l *FileLoader) loadManifest(path string) (*PluginManifest, error) {
    // 支持 .json 和 .yaml 格式
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    var manifest PluginManifest
    ext := filepath.Ext(path)
    switch ext {
    case ".json":
        if err := json.Unmarshal(data, &manifest); err != nil {
            return nil, err
        }
    case ".yaml", ".yml":
        // 使用YAML解析器
        if err := yaml.Unmarshal(data, &manifest); err != nil {
            return nil, err
        }
    }
    
    return &manifest, nil
}

func (l *FileLoader) checkDependencies(manifest *PluginManifest) error {
    for _, dep := range manifest.Dependencies {
        if _, ok := l.plugins[dep]; !ok {
            return fmt.Errorf("missing dependency: %s", dep)
        }
    }
    return nil
}

func (l *FileLoader) loadPlugin(ctx context.Context, manifest *PluginManifest) (Plugin, error) {
    // 使用Go plugin包加载.so/.dylib/.dll
    plug, err := plugin.Open(manifest.Entry)
    if err != nil {
        return nil, err
    }
    
    // 查找New函数
    sym, err := plug.Lookup("New")
    if err != nil {
        return nil, err
    }
    
    // 调用New函数创建插件实例
    newFunc, ok := sym.(func() Plugin)
    if !ok {
        return nil, fmt.Errorf("invalid New function signature")
    }
    
    return newFunc(), nil
}
```

---

## 4. 插件管理器设计

### 4.1 管理器接口
```go
// internal/domain/plugin/manager.go

package plugin

import (
    "context"
    "sync"
)

// Manager 插件管理器接口
type Manager interface {
    // Load 加载插件
    Load(ctx context.Context, path string) error
    
    // Unload 卸载插件
    Unload(ctx context.Context, id string) error
    
    // Reload 重新加载插件
    Reload(ctx context.Context, id string) error
    
    // Enable 启用插件
    Enable(ctx context.Context, id string) error
    
    // Disable 禁用插件
    Disable(ctx context.Context, id string) error
    
    // List 列出所有插件
    List() []PluginInfo
    
    // Get 获取插件信息
    Get(id string) (*PluginInfo, error)
    
    // GetPluginsByCapability 按能力查询插件
    GetPluginsByCapability(capability Capability) []Plugin
    
    // OnPluginLoaded 插件加载事件回调
    OnPluginLoaded(callback func(Plugin))
    
    // OnPluginUnloaded 插件卸载事件回调
    OnPluginUnloaded(callback func(string))
}

// PluginInfo 插件信息
type PluginInfo struct {
    ID           string            `json:"id"`
    Version      string            `json:"version"`
    Name         string            `json:"name"`
    Description  string            `json:"description"`
    Author       string            `json:"author"`
    Type         string            `json:"type"`
    Status       PluginStatus      `json:"status"`
    Capabilities []Capability      `json:"capabilities"`
    Dependencies []string          `json:"dependencies"`
    Config       map[string]any    `json:"config"`
}

// PluginStatus 插件状态
type PluginStatus string

const (
    PluginStatusLoaded    PluginStatus = "loaded"
    PluginStatusStarted   PluginStatus = "started"
    PluginStatusStopped   PluginStatus = "stopped"
    PluginStatusError     PluginStatus = "error"
    PluginStatusDisabled  PluginStatus = "disabled"
)

// DefaultManager 默认插件管理器实现
type DefaultManager struct {
    mu          sync.RWMutex
    loader      Loader
    plugins     map[string]Plugin
    info        map[string]*PluginInfo
    ctx         *PluginContext
    
    // 事件回调
    onLoaded    []func(Plugin)
    onUnloaded  []func(string)
}

func NewManager(loader Loader, ctx *PluginContext) *DefaultManager {
    return &DefaultManager{
        loader:  loader,
        plugins: make(map[string]Plugin),
        info:    make(map[string]*PluginInfo),
        ctx:     ctx,
    }
}

func (m *DefaultManager) Load(ctx context.Context, path string) error {
    p, err := m.loader.Load(ctx, path)
    if err != nil {
        return err
    }
    
    m.mu.Lock()
    m.plugins[p.ID()] = p
    m.info[p.ID()] = &PluginInfo{
        ID:           p.ID(),
        Version:      p.Version(),
        Name:         p.ID(), // 从清单获取
        Status:       PluginStatusStarted,
        Capabilities: p.Capabilities(),
        Dependencies: p.Dependencies(),
    }
    m.mu.Unlock()
    
    // 触发回调
    for _, cb := range m.onLoaded {
        cb(p)
    }
    
    return nil
}

func (m *DefaultManager) Unload(ctx context.Context, id string) error {
    m.mu.RLock()
    p, ok := m.plugins[id]
    m.mu.RUnlock()
    
    if !ok {
        return fmt.Errorf("plugin not found: %s", id)
    }
    
    if err := p.Stop(ctx); err != nil {
        return err
    }
    
    m.mu.Lock()
    delete(m.plugins, id)
    delete(m.info, id)
    m.mu.Unlock()
    
    // 触发回调
    for _, cb := range m.onUnloaded {
        cb(id)
    }
    
    return nil
}

func (m *DefaultManager) GetPluginsByCapability(capability Capability) []Plugin {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    var result []Plugin
    for _, p := range m.plugins {
        for _, c := range p.Capabilities() {
            if c == capability {
                result = append(result, p)
                break
            }
        }
    }
    return result
}
```

---

## 5. 插件发现与注册

### 5.1 插件目录结构
```
~/.code-agent/plugins/
├── builtin/                    # 内置插件
│   ├── coding-tools/
│   │   ├── plugin.json
│   │   └── coding-tools.so
│   ├── memory/
│   │   ├── plugin.json
│   │   └── memory.so
│   └── security/
│       ├── plugin.json
│       └── security.so
├── community/                  # 社区插件
│   ├── python-tools/
│   │   ├── plugin.json
│   │   └── python-tools.so
│   └── docker-tools/
│       ├── plugin.json
│       └── docker-tools.so
└── local/                      # 本地开发插件
    └── my-plugin/
        ├── plugin.json
        └── my-plugin.so
```

### 5.2 插件清单示例
```json
{
  "id": "coding-tools",
  "version": "1.0.0",
  "name": "Coding Tools",
  "description": "Core coding tools for file operations and shell execution",
  "author": "code-agent",
  "entry": "./coding-tools.so",
  "type": "tool",
  "dependencies": [],
  "capabilities": ["tool"],
  "config": {
    "timeout": 60,
    "maxOutputSize": 10240
  },
  "permissions": [
    "filesystem:read",
    "filesystem:write",
    "exec:shell"
  ]
}
```

---

## 6. 迁移现有工具到插件模式

### 6.1 工具插件包装器
```go
// internal/infrastructure/plugin/tool_adapter.go

package plugin

import (
    "context"
    "github.com/spray272598/code-agent/internal/domain/tool"
)

// ToolPluginAdapter 将现有ITool包装为ToolPlugin
type ToolPluginAdapter struct {
    PluginBase
    tools    []tool.ITool
    metadata []tool.ToolMetadata
    registry *tool.MapRegistry
}

func NewToolPluginAdapter(id string, tools []tool.ITool, metadata []tool.ToolMetadata) *ToolPluginAdapter {
    return &ToolPluginAdapter{
        PluginBase: PluginBase{id: id},
        tools:      tools,
        metadata:   metadata,
    }
}

func (a *ToolPluginAdapter) Init(ctx *PluginContext) error {
    if err := a.PluginBase.Init(ctx); err != nil {
        return err
    }
    a.registry = ctx.Tools
    return nil
}

func (a *ToolPluginAdapter) Start(ctx context.Context) error {
    // 注册所有工具到注册表
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

func (a *ToolPluginAdapter) Stop(ctx context.Context) error {
    // 从注册表注销所有工具
    for _, t := range a.tools {
        a.registry.Unregister(t.Name())
    }
    return nil
}

func (a *ToolPluginAdapter) Tools() []tool.ITool {
    return a.tools
}

func (a *ToolPluginAdapter) ToolMetadata() []tool.ToolMetadata {
    return a.metadata
}
```

### 6.2 现有工具迁移示例
```go
// internal/infrastructure/plugin/builtin/coding_tools.go

package builtin

import (
    "github.com/spray272598/code-agent/internal/domain/tool"
    "github.com/spray272598/code-agent/internal/domain/tool/coding"
    plugin "github.com/spray272598/code-agent/internal/infrastructure/plugin"
)

// CodingToolsPlugin 编码工具插件
type CodingToolsPlugin struct {
    plugin.ToolPluginBase
}

func New() plugin.Plugin {
    return &CodingToolsPlugin{}
}

func (p *CodingToolsPlugin) Init(ctx *plugin.PluginContext) error {
    if err := p.PluginBase.Init(ctx); err != nil {
        return err
    }
    
    // 创建工作区
    ws := coding.NewWorkspace(ctx.Config.Get("workspace_root"))
    
    // 注册工具
    tools := []tool.ITool{
        coding.NewReadFile(ws),
        coding.NewWriteFile(ws),
        coding.NewEditFile(ws),
        coding.NewBash(ws, 60),
        coding.NewGlob(ws),
        coding.NewGrep(ws),
    }
    
    metadata := []tool.ToolMetadata{
        tool.DefaultMeta(tools[0], tool.CategoryRead),
        tool.DefaultMeta(tools[1], tool.CategoryWrite),
        tool.DefaultMeta(tools[2], tool.CategoryWrite),
        tool.DefaultMeta(tools[3], tool.CategoryExec),
        tool.DefaultMeta(tools[4], tool.CategoryGlob),
        tool.DefaultMeta(tools[5], tool.CategorySearch),
    }
    
    p.RegisterTools(tools, metadata)
    return nil
}

func (p *CodingToolsPlugin) Capabilities() []plugin.Capability {
    return []plugin.Capability{plugin.CapabilityTool}
}
```

---

## 7. 插件安全机制

### 7.1 权限控制
```go
// internal/domain/plugin/security.go

package plugin

import (
    "context"
)

// PermissionChecker 权限检查器
type PermissionChecker interface {
    // CheckPermission 检查插件是否有指定权限
    CheckPermission(ctx context.Context, pluginID string, permission string) (bool, error)
    
    // GrantPermission 授予插件权限
    GrantPermission(ctx context.Context, pluginID string, permission string) error
    
    // RevokePermission 撤销插件权限
    RevokePermission(ctx context.Context, pluginID string, permission string) error
    
    // ListPermissions 列出插件所有权限
    ListPermissions(ctx context.Context, pluginID string) ([]string, error)
}

// PluginPermission 插件权限定义
type PluginPermission struct {
    PluginID    string   `json:"pluginId"`
    Permissions []string `json:"permissions"`
    GrantedAt   int64    `json:"grantedAt"`
    ExpiresAt   int64    `json:"expiresAt,omitempty"`
}
```

### 7.2 沙箱隔离
```go
// internal/domain/plugin/sandbox.go

package plugin

import (
    "context"
)

// PluginSandbox 插件沙箱
type PluginSandbox interface {
    // ExecuteInSandbox 在沙箱中执行插件代码
    ExecuteInSandbox(ctx context.Context, pluginID string, fn func() error) error
    
    // RestrictFilesystem 限制插件文件系统访问
    RestrictFilesystem(ctx context.Context, pluginID string, allowedPaths []string) error
    
    // RestrictNetwork 限制插件网络访问
    RestrictNetwork(ctx context.Context, pluginID string, allowedHosts []string) error
    
    // RestrictProcess 限制插件进程创建
    RestrictProcess(ctx context.Context, pluginID string, maxProcs int) error
}
```

---

## 8. 配置示例

### 8.1 插件配置文件
```yaml
# ~/.code-agent/config.yaml

plugins:
  enabled: true
  directory: "~/.code-agent/plugins"
  
  # 插件加载顺序
  load_order:
    - builtin/security
    - builtin/coding-tools
    - builtin/memory
    - community/*
  
  # 插件配置
  configs:
    coding-tools:
      timeout: 60
      max_output_size: 10240
      process_isolate: true
    
    memory:
      backend: "sqlite"
      embedding_provider: "openai"
    
    security:
      sandbox_mode: "strict"
      network_block: true
  
  # 插件权限
  permissions:
    coding-tools:
      - "filesystem:read"
      - "filesystem:write"
      - "exec:shell"
    
    memory:
      - "database:read"
      - "database:write"
```

---

## 9. 实施计划

### 阶段1: 核心接口定义（1周）
- [ ] 定义Plugin接口和PluginContext
- [ ] 定义ToolPlugin和LLMPlugin接口
- [ ] 实现PluginBase基础结构
- [ ] 编写接口测试

### 阶段2: 插件加载器（2周）
- [ ] 实现FileLoader
- [ ] 实现插件清单解析
- [ ] 实现依赖检查
- [ ] 实现热加载/卸载

### 阶段3: 插件管理器（1周）
- [ ] 实现DefaultManager
- [ ] 实现插件状态管理
- [ ] 实现事件回调

### 阶段4: 现有工具迁移（2周）
- [ ] 创建CodingToolsPlugin
- [ ] 创建MemoryPlugin
- [ ] 创建SecurityPlugin
- [ ] 创建MCPPlugin

### 阶段5: 安全机制（1周）
- [ ] 实现权限检查器
- [ ] 实现插件沙箱
- [ ] 集成到Guard系统

### 阶段6: 测试与文档（1周）
- [ ] 编写单元测试
- [ ] 编写集成测试
- [ ] 编写使用文档
- [ ] 编写示例插件

---

## 10. 示例：开发自定义插件

### 10.1 创建Python工具插件
```go
// plugins/python-tools/main.go

package main

import (
    "context"
    "os/exec"
    
    "github.com/spray272598/code-agent/internal/domain/tool"
    plugin "github.com/spray272598/code-agent/internal/infrastructure/plugin"
)

type PythonPlugin struct {
    plugin.ToolPluginBase
}

func New() plugin.Plugin {
    return &PythonPlugin{}
}

func (p *PythonPlugin) Init(ctx *plugin.PluginContext) error {
    if err := p.PluginBase.Init(ctx); err != nil {
        return err
    }
    
    // 注册Python执行工具
    pythonTool := &PythonTool{}
    p.RegisterTool(pythonTool, tool.ToolMetadata{
        Name:     "python_exec",
        Version:  "1.0.0",
        Category: tool.CategoryExec,
    })
    
    return nil
}

func (p *PythonPlugin) Capabilities() []plugin.Capability {
    return []plugin.Capability{plugin.CapabilityTool}
}

// PythonTool Python执行工具
type PythonTool struct{}

func (t *PythonTool) Name() string { return "python_exec" }
func (t *PythonTool) Description() string { return "Execute Python code" }

func (t *PythonTool) InputSchema() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "code": map[string]any{"type": "string"},
        },
        "required": []string{"code"},
    }
}

func (t *PythonTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
    code := args["code"].(string)
    cmd := exec.CommandContext(ctx, "python", "-c", code)
    output, err := cmd.CombinedOutput()
    if err != nil {
        return tool.Result{Text: string(output), IsError: true}, nil
    }
    return tool.Result{Text: string(output)}, nil
}
```

### 10.2 插件清单
```json
{
  "id": "python-tools",
  "version": "1.0.0",
  "name": "Python Tools",
  "description": "Python code execution tools",
  "author": "community",
  "entry": "./python-tools.so",
  "type": "tool",
  "dependencies": [],
  "capabilities": ["tool"],
  "permissions": ["exec:python"]
}
```
