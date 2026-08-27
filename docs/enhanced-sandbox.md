# 内核级沙箱增强设计文档

## 1. 设计目标

### 1.1 核心目标
- **内核级隔离**: 使用Linux内核特性（Landlock LSM、seccomp、 namespaces）提供真正的隔离
- **不可逆执行**: 一旦应用沙箱，无法绕过或移除
- **失败即关闭**: 任何沙箱错误都导致操作失败，而非降级
- **资源限制**: 实现CPU、内存、IO、进程数等资源限制
- **网络隔离**: 精细的网络访问控制，支持域名/IP/端口级别

### 1.2 设计原则
- **深度防御**: 多层安全机制叠加
- **最小权限**: 只授予必要的最小权限
- **显式允许**: 默认拒绝所有，只允许明确指定的操作
- **审计追踪**: 所有沙箱操作都可审计

---

## 2. Linux内核级沙箱架构

### 2.1 多层沙箱架构
```
┌─────────────────────────────────────────────────────────┐
│                    应用层 (Agent)                        │
├─────────────────────────────────────────────────────────┤
│                    工具层 (Tools)                        │
├─────────────────────────────────────────────────────────┤
│                    沙箱管理器 (SandboxManager)            │
│  ┌─────────────┬─────────────┬─────────────┬───────────┐ │
│  │   Landlock  │   seccomp   │  Namespaces │  Cgroups  │ │
│  │   (文件系统) │   (系统调用) │   (进程/网络) │  (资源)   │ │
│  └─────────────┴─────────────┴─────────────┴───────────┘ │
├─────────────────────────────────────────────────────────┤
│                    内核层 (Linux Kernel)                  │
└─────────────────────────────────────────────────────────┘
```

### 2.2 沙箱组件职责

| 组件 | 职责 | 保护目标 |
|------|------|----------|
| **Landlock LSM** | 文件系统访问控制 | 防止未授权文件读写 |
| **seccomp-bpf** | 系统调用过滤 | 防止危险系统调用 |
| **Namespaces** | 进程/网络隔离 | 防止进程逃逸和网络攻击 |
| **Cgroups** | 资源限制 | 防止资源耗尽攻击 |

---

## 3. Landlock LSM增强

### 3.1 直接使用Landlock syscall
```go
// internal/infrastructure/sandbox/linux/landlock.go

package sandbox

import (
    "fmt"
    "os"
    "runtime"
    "syscall"
    "unsafe"
)

const (
    // Landlock syscall numbers
    landlockCreateRuleset = 444
    landlockAddRule       = 445
    landlockRestrictSelf  = 446
    
    // Landlock flags
    landlockCreateVersion1 = 1 << 0
    landlockCreateVersion2 = 1 << 1
    
    // Landlock access types
    landlockAccessFSRead    = 1 << 0
    landlockAccessFSWrite   = 1 << 1
    landlockAccessFSTruncate = 1 << 2
    
    // Landlock rule types
    landlockRulePathBeneath = 1 << 0
    landlockRuleFD          = 1 << 1
)

// LandlockAttr Landlock属性结构
type LandlockAttr struct {
    handledAccessFS uint64
}

// LandlockPathBeneathAttr 路径规则属性
type LandlockPathBeneathAttr struct {
    allowedAccessFS uint64
    parentFD         int32
}

// LandlockSandbox Landlock沙箱实现
type LandlockSandbox struct {
    rulesetFD    int
    readOnly     []string
    readWrite    []string
    deny         []string
    networkBlock bool
}

// NewLandlockSandbox 创建Landlock沙箱
func NewLandlockSandbox() *LandlockSandbox {
    return &LandlockSandbox{}
}

// IsAvailable 检查Landlock是否可用
func IsAvailable() bool {
    // 检查内核版本（需要5.13+）
    var uname syscall.Utsname
    if err := syscall.Uname(&uname); err != nil {
        return false
    }
    
    // 解析内核版本
    major, minor, patch := 0, 0, 0
    fmt.Sscanf(string(uname.Release[:]), "%d.%d.%d", &major, &minor, &patch)
    
    // Landlock需要5.13+
    if major < 5 || (major == 5 && minor < 13) {
        return false
    }
    
    // 测试Landlock syscall是否可用
    _, _, errno := syscall.Syscall6(
        uintptr(landlockCreateRuleset),
        0,
        0,
        0,
        0, 0, 0,
    )
    
    return errno == 0 || errno == syscall.ENOSYS
}

// Apply 应用Landlock沙箱
func (l *LandlockSandbox) Apply() error {
    if !IsAvailable() {
        return fmt.Errorf("landlock not available")
    }
    
    // 1. 创建ruleset
    var attr LandlockAttr
    attr.handledAccessFS = landlockAccessFSRead | landlockAccessFSWrite | landlockAccessFSTruncate
    
    rulesetFD, _, errno := syscall.Syscall(
        uintptr(landlockCreateRuleset),
        uintptr(unsafe.Pointer(&attr)),
        unsafe.Sizeof(attr),
        0,
    )
    if errno != 0 {
        return fmt.Errorf("create ruleset: %v", errno)
    }
    l.rulesetFD = int(rulesetFD)
    defer syscall.Close(l.rulesetFD)
    
    // 2. 添加只读路径规则
    for _, path := range l.readOnly {
        if err := l.addPathRule(path, landlockAccessFSRead); err != nil {
            return fmt.Errorf("add read rule for %s: %w", path, err)
        }
    }
    
    // 3. 添加读写路径规则
    for _, path := range l.readWrite {
        if err := l.addPathRule(path, landlockAccessFSRead|landlockAccessFSWrite); err != nil {
            return fmt.Errorf("add readwrite rule for %s: %w", path, err)
        }
    }
    
    // 4. 添加拒绝路径规则（通过不授予任何权限）
    // Landlock通过不添加规则来拒绝访问
    
    // 5. 限制当前进程
    _, _, errno = syscall.Syscall(
        uintptr(landlockRestrictSelf),
        uintptr(l.rulesetFD),
        0,
        0,
    )
    if errno != 0 {
        return fmt.Errorf("restrict self: %v", errno)
    }
    
    // 6. 关闭ruleset fd（权限已固定）
    // 注意：不能关闭，因为需要保持权限
    
    return nil
}

// addPathRule 添加路径规则
func (l *LandlockSandbox) addPathRule(path string, access uint64) error {
    // 打开路径
    fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
    if err != nil {
        return err
    }
    defer syscall.Close(fd)
    
    // 创建路径规则
    var attr LandlockPathBeneathAttr
    attr.allowedAccessFS = access
    attr.parentFD = int32(fd)
    
    _, _, errno := syscall.Syscall(
        uintptr(landlockAddRule),
        uintptr(l.rulesetFD),
        uintptr(landlockRulePathBeneath),
        uintptr(unsafe.Pointer(&attr)),
        0,
    )
    if errno != 0 {
        return errno
    }
    
    return nil
}
```

### 3.2 Landlock配置增强
```go
// internal/infrastructure/sandbox/linux/config.go

package sandbox

// EnhancedLandlockConfig 增强的Landlock配置
type EnhancedLandlockConfig struct {
    // 文件系统规则
    ReadOnlyPaths  []PathRule `json:"readOnlyPaths"`
    ReadWritePaths []PathRule `json:"readWritePaths"`
    DenyPaths      []PathRule `json:"denyPaths"`
    
    // 网络规则
    NetworkBlock   bool       `json:"networkBlock"`
    AllowedHosts   []string   `json:"allowedHosts"`
    AllowedPorts   []int      `json:"allowedPorts"`
    
    // 资源限制
    MaxMemoryMB    int        `json:"maxMemoryMB"`
    MaxCPUPercent  int        `json:"maxCPUPercent"`
    MaxProcesses   int        `json:"maxProcesses"`
    MaxOpenFiles   int        `json:"maxOpenFiles"`
    
    // 安全选项
    NoNewPrivs     bool       `json:"noNewPrivs"`
    DropCaps       bool       `json:"dropCaps"`
    ReadOnlyTmp    bool       `json:"readOnlyTmp"`
}

// PathRule 路径规则
type PathRule struct {
    Path        string `json:"path"`
    Recursive   bool   `json:"recursive"`
    RequireParent bool `json:"requireParent"` // 需要父目录可访问
}
```

---

## 4. seccomp-bpf系统调用过滤

### 4.1 seccomp过滤器实现
```go
// internal/infrastructure/sandbox/linux/seccomp.go

package sandbox

import (
    "fmt"
    "syscall"
    "unsafe"
)

const (
    seccompModeDisabled = 0
    seccompModeStrict   = 1
    seccompModeFilter   = 2
    
    BPF.ld   = 0x00
    BPF.ja   = 0x05
    BPF.jeq  = 0x10
    BPF.jge  = 0x30
    BPF.jle  = 0x20
    BPF.ret  = 0x06
    BPF.alu  = 0x04
    BPF.ldx  = 0x01
    BPF.st   = 0x02
    BPF.stx  = 0x03
    
    SECCOMP_RET_KILL_PROCESS = 0x00000000
    SECCOMP_RET_TRAP         = 0x00020000
    SECCOMP_RET_ERRNO        = 0x00050000
    SECCOMP_RET_USER_NOTIF   = 0x00070000
    SECCOMP_RET_TRACE        = 0x00030000
    SECCOMP_RET_LOG          = 0x00060000
    SECCOMP_RET_ALLOW        = 0x7fff0000
)

// BPFInstr BPF指令
type BPFInstr struct {
    code uint16
    jt   uint8
    jf   uint8
    k    uint32
}

// SeccompFilter seccomp过滤器
type SeccompFilter struct {
    rules    []BPFInstr
    basePath []BPFInstr
}

// NewSeccompFilter 创建seccomp过滤器
func NewSeccompFilter() *SeccompFilter {
    f := &SeccompFilter{}
    f.basePath = f.createBasePath()
    return f
}

// createBasePath 创建基础路径（加载架构和系统调用号）
func (f *SeccompFilter) createBasePath() []BPFInstr {
    return []BPFInstr{
        // 加载架构
        {code: BPF.ld | BPF.w | BPF.abs, k: 0}, // AUDIT_ARCH
        // 检查是否为x86_64
        {code: BPF.jeq | BPF.j, jt: 1, jf: 0, k: 0xC000003E}, // AUDIT_ARCH_X86_64
        // 不匹配则杀死进程
        {code: BPF.ret, k: SECCOMP_RET_KILL_PROCESS},
    }
}

// AddAllowedSyscall 添加允许的系统调用
func (f *SeccompFilter) AddAllowedSyscall(syscallNum int) {
    // 加载系统调用号
    instr := []BPFInstr{
        {code: BPF.ld | BPF.w | BPF.abs, k: 4}, // 系统调用号偏移
        {code: BPF.jeq | BPF.j, jt: 0, jf: 0, k: uint32(syscallNum)},
        {code: BPF.ret, k: SECCOMP_RET_ALLOW},
    }
    f.rules = append(f.rules, instr...)
}

// AddDeniedSyscall 添加拒绝的系统调用
func (f *SeccompFilter) AddDeniedSyscall(syscallNum int, errCode int) {
    instr := []BPFInstr{
        {code: BPF.ld | BPF.w | BPF.abs, k: 4},
        {code: BPF.jeq | BPF.j, jt: 0, jf: 0, k: uint32(syscallNum)},
        {code: BPF.ret, k: uint32(SECCOMP_RET_ERRNO) | uint32(errCode)},
    }
    f.rules = append(f.rules, instr...)
}

// AddConditionalRule 添加条件规则
func (f *SeccompFilter) AddConditionalRule(syscallNum int, args []ConditionalArg, action uint32) {
    // 加载系统调用号
    instrs := []BPFInstr{
        {code: BPF.ld | BPF.w | BPF.abs, k: 4},
        {code: BPF.jeq | BPF.j, jt: 0, jf: uint8(len(args) + 1), k: uint32(syscallNum)},
    }
    
    // 检查参数
    for i, arg := range args {
        offset := 16 + uint32(i*8) // 参数偏移
        instrs = append(instrs,
            BPFInstr{code: BPF.ld | BPF.w | BPF.abs, k: offset},
            BPFInstr{code: BPF.jeq | BPF.j, jt: 0, jf: 1, k: arg.Value},
        )
    }
    
    // 允许
    instrs = append(instrs, BPFInstr{code: BPF.ret, k: action})
    
    f.rules = append(f.rules, instrs...)
}

// Apply 应用seccomp过滤器
func (f *SeccompFilter) Apply() error {
    // 构建完整程序
    program := append(f.basePath, f.rules...)
    
    // 添加默认拒绝
    program = append(program, BPFInstr{code: BPF.ret, k: SECCOMP_RET_KILL_PROCESS})
    
    // 转换为字节切片
    programBytes := (*[maxBpfSize]byte)(unsafe.Pointer(&program[0]))[:len(program)*8]
    
    // 应用seccomp
    _, _, errno := syscall.Syscall(
        syscall.SYS_SECCOMP,
        seccompModeFilter,
        0,
        uintptr(unsafe.Pointer(&programBytes[0])),
    )
    if errno != 0 {
        return fmt.Errorf("apply seccomp: %v", errno)
    }
    
    return nil
}

const maxBpfSize = 1024 * 1024 // 1MB
```

### 4.2 系统调用白名单
```go
// internal/infrastructure/sandbox/linux/syscalls.go

package sandbox

// SafeSyscalls 安全的系统调用白名单
var SafeSyscalls = []int{
    // 基本IO
    syscall.SYS_READ,
    syscall.SYS_WRITE,
    syscall.SYS_OPEN,
    syscall.SYS_CLOSE,
    syscall.SYS_STAT,
    syscall.SYS_FSTAT,
    syscall.SYS_LSTAT,
    syscall.SYS_POLL,
    syscall.SYS_LSEEK,
    syscall.SYS_MMAP,
    syscall.SYS_MPROTECT,
    syscall.SYS_MUNMAP,
    syscall.SYS_MSYNC,
    
    // 进程控制
    syscall.SYS_FORK,
    syscall.SYS_VFORK,
    syscall.SYS_EXECVE,
    syscall.SYS_EXIT,
    syscall.SYS_WAIT4,
    syscall.SYS_KILL,
    syscall.SYS_UNAME,
    
    // 文件系统
    syscall.SYS_ACCESS,
    syscall.SYS_PIPE,
    syscall.SYS_SELECT,
    syscall.SYS_SCHED_YIELD,
    syscall.SYS_MREMAP,
    syscall.SYS_MSYNC,
    syscall.SYS_MINCORE,
    syscall.SYS_MADVISE,
    
    // 网络
    syscall.SYS_SOCKET,
    syscall.SYS_CONNECT,
    syscall.SYS_ACCEPT,
    syscall.SYS_SENDTO,
    syscall.SYS_RECVFROM,
    syscall.SYS_SENDMSG,
    syscall.SYS_RECVMSG,
    syscall.SYS_SHUTDOWN,
    syscall.SYS_BIND,
    syscall.SYS_LISTEN,
    syscall.SYS_GETSOCKNAME,
    syscall.SYS_GETPEERNAME,
    syscall.SYS_SOCKETPAIR,
    syscall.SYS_SETSOCKOPT,
    syscall.SYS_GETSOCKOPT,
    
    // 进程间通信
    syscall.SYS_PIPE2,
    syscall.SYS_EPOLL_CREATE1,
    syscall.SYS_EPOLL_CTL,
    syscall.SYS_EPOLL_WAIT,
    
    // 文件描述符操作
    syscall.SYS_DUP,
    syscall.SYS_DUP2,
    syscall.SYS_DUP3,
    syscall.SYS_FCNTL,
    syscall.SYS_IOCTL,
    
    // 文件系统扩展
    syscall.SYS_READLINKAT,
    syscall.SYS_FCHMODAT,
    syscall.SYS_FCHMOD,
    syscall.SYS_FCHOWNAT,
    syscall.SYS_FCHOWN,
    syscall.SYS_ACCESSAT,
    syscall.SYS_OPENAT,
}

// DangerousSyscalls 危险的系统调用黑名单
var DangerousSyscalls = []int{
    // 内核模块
    syscall.SYS_INIT_MODULE,
    syscall.SYS_FINIT_MODULE,
    syscall.SYS_DELETE_MODULE,
    
    // 系统修改
    syscall.SYS_SETHOSTNAME,
    syscall.SYS_SETDOMAINNAME,
    syscall.SYS_REBOOT,
    
    // 调试
    syscall.SYS_PTRACE,
    syscall.SYS_PROCESS_VM_READV,
    syscall.SYS_PROCESS_VM_WRITEV,
    
    // 直接IO
    syscall.SYS_IO_SETUP,
    syscall.SYS_IO_DESTROY,
    syscall.SYS_IO_GETEVENTS,
    syscall.SYS_IO_SUBMIT,
    
    // 性能
    syscall.SYS_MPROTECT,
    syscall.SYS_MLOCK,
    syscall.SYS_MUNLOCK,
    syscall.SYS_MLOCKALL,
    syscall.SYS_MUNLOCKALL,
    
    // 时间
    syscall.SYS_CLOCK_SETTIME,
    syscall.SYS_SETTIMEOFDAY,
    syscall.SYS_ADJTIMEX,
}
```

---

## 5. Namespace隔离

### 5.1 Namespace实现
```go
// internal/infrastructure/sandbox/linux/namespace.go

package sandbox

import (
    "fmt"
    "os"
    "runtime"
    "syscall"
)

const (
    // Clone flags
    cloneNewNS   = 0x00020000 // CLONE_NEWNS
    cloneNewPID  = 0x20000000 // CLONE_NEWPID
    cloneNewIPC  = 0x08000000 // CLONE_NEWIPC
    cloneNewUTS  = 0x04000000 // CLONE_NEWUTS
    cloneNewNET  = 0x40000000 // CLONE_NEWNET
    cloneNewUser = 0x10000000 // CLONE_NEWUSER
)

// NamespaceSandbox Namespace沙箱
type NamespaceSandbox struct {
    mountNS  bool
    pidNS    bool
    ipcNS    bool
    utsNS    bool
    netNS    bool
    userNS   bool
}

// NewNamespaceSandbox 创建Namespace沙箱
func NewNamespaceSandbox() *NamespaceSandbox {
    return &NamespaceSandbox{
        mountNS: true,
        pidNS:   true,
        ipcNS:   true,
        utsNS:   true,
        netNS:   true,
        userNS:  true,
    }
}

// Apply 应用Namespace隔离
func (n *NamespaceSandbox) Apply() error {
    // 锁定goroutine到当前线程
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    
    // 设置clone flags
    flags := 0
    if n.mountNS {
        flags |= cloneNewNS
    }
    if n.pidNS {
        flags |= cloneNewPID
    }
    if n.ipcNS {
        flags |= cloneNewIPC
    }
    if n.utsNS {
        flags |= cloneNewUTS
    }
    if n.netNS {
        flags |= cloneNewNET
    }
    if n.userNS {
        flags |= cloneNewUser
    }
    
    // 使用unshare系统调用
    _, _, errno := syscall.Syscall(
        syscall.SYS_UNSHARE,
        uintptr(flags),
        0,
        0,
    )
    if errno != 0 {
        return fmt.Errorf("unshare: %v", errno)
    }
    
    // 如果创建了用户namespace，需要设置UID/GID映射
    if n.userNS {
        if err := n.setupUserNS(); err != nil {
            return fmt.Errorf("setup user namespace: %w", err)
        }
    }
    
    // 如果创建了挂载namespace，需要挂载必要的文件系统
    if n.mountNS {
        if err := n.setupMountNS(); err != nil {
            return fmt.Errorf("setup mount namespace: %w", err)
        }
    }
    
    // 如果创建了网络namespace，需要配置网络
    if n.netNS {
        if err := n.setupNetNS(); err != nil {
            return fmt.Errorf("setup network namespace: %w", err)
        }
    }
    
    return nil
}

// setupUserNS 设置用户namespace
func (n *NamespaceSandbox) setupUserNS() error {
    // 写入UID映射
    // 这里简化处理，实际需要更复杂的映射逻辑
    
    // 设置no_new_privs
    _, _, errno := syscall.Syscall6(
        syscall.SYS_PRCTL,
        38, // PR_SET_NO_NEW_PRIVS
        1,
        0,
        0,
        0,
        0,
    )
    if errno != 0 {
        return fmt.Errorf("set no_new_privs: %v", errno)
    }
    
    return nil
}

// setupMountNS 设置挂载namespace
func (n *NamespaceSandbox) setupMountNS() error {
    // 挂载proc（如果创建了PID namespace）
    if n.pidNS {
        if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
            return fmt.Errorf("mount proc: %w", err)
        }
    }
    
    // 挂载tmpfs到/tmp
    if err := syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, "size=100M,mode=1777"); err != nil {
        return fmt.Errorf("mount tmpfs: %w", err)
    }
    
    // 挂载devpts（如果有终端需求）
    if err := os.MkdirAll("/dev/pts", 0755); err == nil {
        syscall.Mount("devpts", "/dev/pts", "devpts", 0, "gid=5,mode=620")
    }
    
    return nil
}

// setupNetNS 设置网络namespace
func (n *NamespaceSandbox) setupNetNS() error {
    // 创建loopback接口
    if err := n.createLoopback(); err != nil {
        return fmt.Errorf("create loopback: %w", err)
    }
    
    return nil
}

// createLoopback 创建loopback网络接口
func (n *NamespaceSandbox) createLoopback() error {
    // 使用netlink创建loopback接口
    // 这里简化处理，实际需要使用netlink库
    
    // 启动loopback
    // ip link set lo up
    
    return nil
}
```

---

## 6. Cgroups资源限制

### 6.1 Cgroups实现
```go
// internal/infrastructure/sandbox/linux/cgroup.go

package sandbox

import (
    "fmt"
    "os"
    "path/filepath"
    "strconv"
    "syscall"
)

const (
    cgroupPath = "/sys/fs/cgroup"
)

// CgroupSandbox Cgroups资源限制
type CgroupSandbox struct {
    name         string
    path         string
    maxMemoryMB  int
    maxCPUPercent int
    maxProcesses  int
    maxOpenFiles  int
}

// NewCgroupSandbox 创建Cgroups沙箱
func NewCgroupSandbox(name string) *CgroupSandbox {
    return &CgroupSandbox{
        name:        name,
        path:        filepath.Join(cgroupPath, "code-agent", name),
        maxMemoryMB: 512,
        maxCPUPercent: 50,
        maxProcesses: 32,
        maxOpenFiles: 1024,
    }
}

// Apply 应用Cgroups限制
func (c *CgroupSandbox) Apply() error {
    // 1. 创建cgroup目录
    if err := os.MkdirAll(c.path, 0755); err != nil {
        return fmt.Errorf("create cgroup: %w", err)
    }
    
    // 2. 设置内存限制
    if err := c.setMemoryLimit(); err != nil {
        return fmt.Errorf("set memory limit: %w", err)
    }
    
    // 3. 设置CPU限制
    if err := c.setCPULimit(); err != nil {
        return fmt.Errorf("set cpu limit: %w", err)
    }
    
    // 4. 设置进程数限制
    if err := c.setProcessLimit(); err != nil {
        return fmt.Errorf("set process limit: %w", err)
    }
    
    // 5. 设置文件描述符限制
    if err := c.setFileLimit(); err != nil {
        return fmt.Errorf("set file limit: %w", err)
    }
    
    // 6. 将当前进程加入cgroup
    if err := c.addCurrentProcess(); err != nil {
        return fmt.Errorf("add current process: %w", err)
    }
    
    return nil
}

// setMemoryLimit 设置内存限制
func (c *CgroupSandbox) setMemoryLimit() error {
    // 内存限制（字节）
    limit := int64(c.maxMemoryMB) * 1024 * 1024
    return c.writeFile("memory.max", strconv.FormatInt(limit, 10))
}

// setCPULimit 设置CPU限制
func (c *CgroupSandbox) setCPULimit() error {
    // CPU配额（微秒）
    period := 100000 // 100ms
    quota := int64(period) * int64(c.maxCPUPercent) / 100
    return c.writeFile("cpu.max", fmt.Sprintf("%d %d", quota, period))
}

// setProcessLimit 设置进程数限制
func (c *CgroupSandbox) setProcessLimit() error {
    return c.writeFile("pids.max", strconv.Itoa(c.maxProcesses))
}

// setFileLimit 设置文件描述符限制
func (c *CgroupSandbox) setFileLimit() error {
    // 通过rlimit设置
    return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{
        Cur: uint64(c.maxOpenFiles),
        Max: uint64(c.maxOpenFiles),
    })
}

// addCurrentProcess 将当前进程加入cgroup
func (c *CgroupSandbox) addCurrentProcess() error {
    pid := os.Getpid()
    return c.writeFile("cgroup.procs", strconv.Itoa(pid))
}

// writeFile 写入cgroup文件
func (c *CgroupSandbox) writeFile(filename, value string) error {
    path := filepath.Join(c.path, filename)
    return os.WriteFile(path, []byte(value), 0644)
}

// Cleanup 清理cgroup
func (c *CgroupSandbox) Cleanup() error {
    // 杀死cgroup中的所有进程
    procs, err := c.readFile("cgroup.procs")
    if err != nil {
        return err
    }
    
    // 解析进程ID并杀死
    for _, line := range splitLines(procs) {
        if pid, err := strconv.Atoi(line); err == nil {
            syscall.Kill(pid, syscall.SIGKILL)
        }
    }
    
    // 删除cgroup目录
    return os.RemoveAll(c.path)
}

// readFile 读取cgroup文件
func (c *CgroupSandbox) readFile(filename string) (string, error) {
    path := filepath.Join(c.path, filename)
    data, err := os.ReadFile(path)
    if err != nil {
        return "", err
    }
    return string(data), nil
}

// splitLines 分割行
func splitLines(s string) []string {
    var lines []string
    for _, line := range splitByNewline(s) {
        if line != "" {
            lines = append(lines, line)
        }
    }
    return lines
}

// splitByNewline 按换行分割
func splitByNewline(s string) []string {
    var result []string
    start := 0
    for i := 0; i < len(s); i++ {
        if s[i] == '\n' {
            result = append(result, s[start:i])
            start = i + 1
        }
    }
    if start < len(s) {
        result = append(result, s[start:])
    }
    return result
}
```

---

## 7. 综合沙箱管理器

### 7.1 沙箱管理器实现
```go
// internal/infrastructure/sandbox/linux/manager.go

package sandbox

import (
    "context"
    "fmt"
    "sync"
)

// ManagedSandbox 综合沙箱管理器
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

// NewManagedSandbox 创建综合沙箱
func NewManagedSandbox(config *EnhancedLandlockConfig) *ManagedSandbox {
    return &ManagedSandbox{
        config:    config,
        landlock:  NewLandlockSandbox(),
        seccomp:   NewSeccompFilter(),
        namespace: NewNamespaceSandbox(),
        cgroup:    NewCgroupSandbox("agent-sandbox"),
    }
}

// Apply 应用所有沙箱限制
func (m *ManagedSandbox) Apply() error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    if m.applied {
        return fmt.Errorf("sandbox already applied")
    }
    
    // 1. 应用Landlock（文件系统限制）
    if err := m.applyLandlock(); err != nil {
        return fmt.Errorf("apply landlock: %w", err)
    }
    
    // 2. 应用seccomp（系统调用过滤）
    if err := m.applySeccomp(); err != nil {
        return fmt.Errorf("apply seccomp: %w", err)
    }
    
    // 3. 应用Namespace（进程/网络隔离）
    if err := m.applyNamespace(); err != nil {
        return fmt.Errorf("apply namespace: %w", err)
    }
    
    // 4. 应用Cgroups（资源限制）
    if err := m.applyCgroup(); err != nil {
        return fmt.Errorf("apply cgroup: %w", err)
    }
    
    m.applied = true
    m.active = true
    
    return nil
}

// applyLandlock 应用Landlock限制
func (m *ManagedSandbox) applyLandlock() error {
    // 转换配置
    for _, rule := range m.config.ReadWritePaths {
        m.landlock.readWrite = append(m.landlock.readWrite, rule.Path)
    }
    for _, rule := range m.config.ReadOnlyPaths {
        m.landlock.readOnly = append(m.landlock.readOnly, rule.Path)
    }
    for _, rule := range m.config.DenyPaths {
        m.landlock.deny = append(m.landlock.deny, rule.Path)
    }
    m.landlock.networkBlock = m.config.NetworkBlock
    
    return m.landlock.Apply()
}

// applySeccomp 应用seccomp过滤
func (m *ManagedSandbox) applySeccomp() error {
    // 添加安全的系统调用
    for _, syscall := range SafeSyscalls {
        m.seccomp.AddAllowedSyscall(syscall)
    }
    
    // 拒绝危险的系统调用
    for _, syscall := range DangerousSyscalls {
        m.seccomp.AddDeniedSyscall(syscall, 0)
    }
    
    return m.seccomp.Apply()
}

// applyNamespace 应用Namespace隔离
func (m *ManagedSandbox) applyNamespace() error {
    return m.namespace.Apply()
}

// applyCgroup 应用Cgroups限制
func (func (m *ManagedSandbox) applyCgroup() error {
    m.cgroup.maxMemoryMB = m.config.MaxMemoryMB
    m.cgroup.maxCPUPercent = m.config.MaxCPUPercent
    m.cgroup.maxProcesses = m.config.MaxProcesses
    m.cgroup.maxOpenFiles = m.config.MaxOpenFiles
    
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
    
    // 应用Namespace
    command.SysProcAttr = &syscall.SysProcAttr{
        Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNET,
    }
    
    // 执行命令
    return command.CombinedOutput()
}

// IsActive 检查沙箱是否激活
func (m *ManagedSandbox) IsActive() bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.active
}

// Deactivate 停用沙箱
func (m *ManagedSandbox) Deactivate() {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    // 注意：Landlock是不可逆的，无法真正停用
    // 这里只是标记状态
    
    m.active = false
}

// Cleanup 清理资源
func (m *ManagedSandbox) Cleanup() error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    // 清理cgroup
    if m.cgroup != nil {
        if err := m.cgroup.Cleanup(); err != nil {
            return fmt.Errorf("cleanup cgroup: %w", err)
        }
    }
    
    return nil
}
```

---

## 8. 集成到现有系统

### 8.1 沙箱管理器集成
```go
// internal/domain/security/sandbox_enhanced.go

package security

import (
    "context"
    "fmt"
    "runtime"
    
    "github.com/spray272598/code-agent/internal/infrastructure/sandbox/linux"
)

// EnhancedSandboxManager 增强的沙箱管理器
type EnhancedSandboxManager struct {
    config    *linux.EnhancedLandlockConfig
    sandbox   *linux.ManagedSandbox
    audit     *AuditLogger
}

// NewEnhancedSandboxManager 创建增强的沙箱管理器
func NewEnhancedSandboxManager(audit *AuditLogger) *EnhancedSandboxManager {
    return &EnhancedSandboxManager{
        audit: audit,
    }
}

// ApplyProfile 应用沙箱配置
func (m *EnhancedSandboxManager) ApplyProfile(profile ProfileConfig, workspace string) error {
    // 转换配置
    config := &linux.EnhancedLandlockConfig{
        ReadOnlyPaths: []linux.PathRule{
            {Path: "/usr", Recursive: true},
            {Path: "/bin", Recursive: true},
            {Path: "/lib", Recursive: true},
            {Path: "/lib64", Recursive: true},
        },
        ReadWritePaths: []linux.PathRule{
            {Path: workspace, Recursive: true},
        },
        DenyPaths: []linux.PathRule{
            {Path: "/root", Recursive: true},
            {Path: "/home", Recursive: true},
        },
        NetworkBlock: profile.NetworkBlock,
        MaxMemoryMB: 512,
        MaxCPUPercent: 50,
        MaxProcesses: 32,
        MaxOpenFiles: 1024,
    }
    
    // 创建沙箱
    m.sandbox = linux.NewManagedSandbox(config)
    
    // 应用沙箱
    if err := m.sandbox.Apply(); err != nil {
        m.audit.Warn(CategorySandbox, "sandbox", fmt.Sprintf("apply enhanced sandbox: %v", err))
        return err
    }
    
    m.audit.Info(CategorySandbox, "sandbox", "Enhanced sandbox applied successfully")
    return nil
}

// ExecuteInSandbox 在沙箱中执行命令
func (m *EnhancedSandboxManager) ExecuteInSandbox(ctx context.Context, cmd string, args []string) ([]byte, error) {
    if m.sandbox == nil || !m.sandbox.IsActive() {
        return nil, fmt.Errorf("sandbox not active")
    }
    
    return m.sandbox.ExecuteInSandbox(ctx, cmd, args)
}

// IsAvailable 检查增强沙箱是否可用
func IsEnhancedSandboxAvailable() bool {
    // 只在Linux上可用
    if runtime.GOOS != "linux" {
        return false
    }
    
    // 检查Landlock是否可用
    return linux.IsAvailable()
}
```

### 8.2 配置示例
```yaml
# ~/.code-agent/config.yaml

security:
  sandbox:
    # 增强沙箱配置
    enhanced:
      enabled: true
      
      # Landlock配置
      landlock:
        readOnlyPaths:
          - path: "/usr"
            recursive: true
          - path: "/bin"
            recursive: true
          - path: "/lib"
            recursive: true
        readWritePaths:
          - path: "${workspace}"
            recursive: true
        denyPaths:
          - path: "/root"
            recursive: true
          - path: "/home"
            recursive: true
        
        # 网络配置
        networkBlock: true
        allowedHosts:
          - "api.openai.com"
          - "api.siliconflow.cn"
        allowedPorts:
          - 443
          - 80
      
      # seccomp配置
      seccomp:
        enabled: true
        mode: "filter"
        allowedSyscalls:
          - "read"
          - "write"
          - "open"
          - "close"
          - "stat"
          - "fstat"
          - "lstat"
          - "poll"
          - "lseek"
          - "mmap"
          - "mprotect"
          - "munmap"
          - "brk"
          - "ioctl"
          - "access"
          - "pipe"
          - "select"
          - "sched_yield"
          - "mremap"
          - "msync"
          - "mincore"
          - "madvise"
          - "shmget"
          - "shmat"
          - "shmctl"
          - "dup"
          - "dup2"
          - "pause"
          - "nanosleep"
          - "getitimer"
          - "alarm"
          - "setitimer"
          - "getpid"
          - "sendfile"
          - "socket"
          - "connect"
          - "accept"
          - "sendto"
          - "recvfrom"
          - "sendmsg"
          - "recvmsg"
          - "shutdown"
          - "bind"
          - "listen"
          - "getsockname"
          - "getpeername"
          - "socketpair"
          - "setsockopt"
          - "getsockopt"
          - "clone"
          - "fork"
          - "vfork"
          - "execve"
          - "exit"
          - "wait4"
          - "kill"
          - "uname"
          - "semget"
          - "semop"
          - "semctl"
          - "shmdt"
          - "msgget"
          - "msgsnd"
          - "msgrcv"
          - "msgctl"
          - "fcntl"
          - "flock"
          - "fsync"
          - "fdatasync"
          - "truncate"
          - "ftruncate"
          - "getdents"
          - "getcwd"
          - "chdir"
          - "fchdir"
          - "rename"
          - "mkdir"
          - "rmdir"
          - "creat"
          - "link"
          - "unlink"
          - "symlink"
          - "readlink"
          - "chmod"
          - "fchmod"
          - "chown"
          - "fchown"
          - "lchown"
          - "umask"
          - "gettimeofday"
          - "getrlimit"
          - "getrusage"
          - "sysinfo"
          - "times"
          - "ptrace"
          - "getuid"
          - "syslog"
          - "getgid"
          - "setuid"
          - "setgid"
          - "geteuid"
          - "getegid"
          - "setpgid"
          - "getppid"
          - "getpgrp"
          - "setsid"
          - "setreuid"
          - "setregid"
          - "getgroups"
          - "setgroups"
          - "setresuid"
          - "getresuid"
          - "setresgid"
          - "getresgid"
          - "getpgid"
          - "setfsuid"
          - "setfsgid"
          - "getsid"
          - "capget"
          - "capset"
          - "rt_sigpending"
          - "rt_sigtimedwait"
          - "rt_sigqueueinfo"
          - "rt_sigsuspend"
          - "sigaltstack"
          - "utime"
          - "mknod"
          - "uselib"
          - "personality"
          - "ustat"
          - "statfs"
          - "fstatfs"
          - "sysfs"
          - "getpriority"
          - "setpriority"
          - "sched_setparam"
          - "sched_getparam"
          - "sched_setscheduler"
          - "sched_getscheduler"
          - "sched_get_priority_max"
          - "sched_get_priority_min"
          - "sched_rr_get_interval"
          - "mlock"
          - "munlock"
          - "mlockall"
          - "munlockall"
          - "vhangup"
          - "modify_ldt"
          - "pivot_root"
          - "_sysctl"
          - "prctl"
          - "arch_prctl"
          - "adjtimex"
          - "setrlimit"
          - "chroot"
          - "sync"
          - "acct"
          - "settimeofday"
          - "mount"
          - "umount2"
          - "swapon"
          - "swapoff"
          - "reboot"
          - "sethostname"
          - "setdomainname"
          - "iopl"
          - "ioperm"
          - "create_module"
          - "init_module"
          - "delete_module"
          - "get_kernel_syms"
          - "query_module"
          - "quotactl"
          - "nfsservctl"
          - "getpmsg"
          - "putpmsg"
          - "afs_syscall"
          - "tuxcall"
          - "security"
          - "gettid"
          - "readahead"
          - "setxattr"
          - "lsetxattr"
          - "fsetxattr"
          - "getxattr"
          - "lgetxattr"
          - "fgetxattr"
          - "listxattr"
          - "llistxattr"
          - "flistxattr"
          - "removexattr"
          - "lremovexattr"
          - "fremovexattr"
          - "tkill"
          - "time"
          - "futex"
          - "sched_setaffinity"
          - "sched_getaffinity"
          - "set_thread_area"
          - "io_setup"
          - "io_destroy"
          - "io_getevents"
          - "io_submit"
          - "io_cancel"
          - "get_thread_area"
          - "lookup_dcookie"
          - "epoll_create"
          - "epoll_ctl_old"
          - "epoll_wait_old"
          - "remap_file_pages"
          - "getdents64"
          - "set_tid_address"
          - "restart_syscall"
          - "semtimedop"
          - "fadvise64"
          - "timer_create"
          - "timer_settime"
          - "timer_gettime"
          - "timer_getoverrun"
          - "timer_delete"
          - "clock_settime"
          - "clock_gettime"
          - "clock_getres"
          - "clock_nanosleep"
          - "exit_group"
          - "epoll_wait"
          - "epoll_ctl"
          - "tgkill"
          - "utimes"
          - "vserver"
          - "mbind"
          - "set_mempolicy"
          - "get_mempolicy"
          - "mq_open"
          - "mq_unlink"
          - "mq_timedsend"
          - "mq_timedreceive"
          - "mq_notify"
          - "mq_getsetattr"
          - "kexec_load"
          - "waitid"
          - "add_key"
          - "request_key"
          - "keyctl"
          - "ioprio_set"
          - "ioprio_get"
          - "inotify_init"
          - "inotify_add_watch"
          - "inotify_rm_watch"
          - "migrate_pages"
          - "openat"
          - "mkdirat"
          - "mknodat"
          - "fchownat"
          - "futimesat"
          - "newfstatat"
          - "unlinkat"
          - "renameat"
          - "linkat"
          - "symlinkat"
          - "readlinkat"
          - "fchmodat"
          - "faccessat"
          - "pselect6"
          - "ppoll"
          - "unshare"
          - "set_robust_list"
          - "get_robust_list"
          - "splice"
          - "tee"
          - "sync_file_range"
          - "vmsplice"
          - "move_pages"
          - "utimensat"
          - "epoll_pwait"
          - "signalfd"
          - "timerfd_create"
          - "eventfd"
          - "fallocate"
          - "timerfd_settime"
          - "timerfd_gettime"
          - "accept4"
          - "signalfd4"
          - "eventfd2"
          - "epoll_create1"
          - "dup3"
          - "pipe2"
          - "inotify_init1"
          - "preadv"
          - "pwritev"
          - "rt_tgsigqueueinfo"
          - "perf_event_open"
          - "recvmmsg"
          - "fanotify_init"
          - "fanotify_mark"
          - "prlimit64"
          - "name_to_handle_at"
          - "open_by_handle_at"
          - "clock_adjtime"
          - "syncfs"
          - "sendmmsg"
          - "setns"
          - "getcpu"
          - "process_vm_readv"
          - "process_vm_writev"
          - "kcmp"
          - "finit_module"
          - "sched_setattr"
          - "sched_getattr"
          - "renameat2"
          - "seccomp"
          - "getrandom"
          - "memfd_create"
          - "kexec_file_load"
          - "bpf"
          - "execveat"
          - "userfaultfd"
          - "membarrier"
          - "mlock2"
          - "copy_file_range"
          - "preadv2"
          - "pwritev2"
          - "pkey_mprotect"
          - "pkey_alloc"
          - "pkey_free"
          - "statx"
          - "io_pgetevents"
          - "rseq"
          - "kexec_file_load"
        
        deniedSyscalls:
          - "ptrace"
          - "process_vm_readv"
          - "process_vm_writev"
          - "init_module"
          - "finit_module"
          - "delete_module"
          - "reboot"
          - "sethostname"
          - "setdomainname"
      
      # Namespace配置
      namespace:
        mount: true
        pid: true
        ipc: true
        uts: true
        net: true
        user: true
      
      # Cgroups配置
      cgroup:
        maxMemoryMB: 512
        maxCPUPercent: 50
        maxProcesses: 32
        maxOpenFiles: 1024
```

---

## 9. 实施计划

### 阶段1: Landlock直接syscall集成（2周）
- [ ] 实现Landlock syscall封装
- [ ] 实现路径规则管理
- [ ] 实现访问控制逻辑
- [ ] 编写单元测试

### 阶段2: seccomp-bpf过滤器（2周）
- [ ] 实现BPF指令生成
- [ ] 实现系统调用白名单
- [ ] 实现条件规则
- [ ] 编写安全测试

### 阶段3: Namespace隔离（2周）
- [ ] 实现Mount namespace
- [ ] 实现PID namespace
- [ ] 实现Network namespace
- [ ] 实现User namespace

### 阶段4: Cgroups资源限制（1周）
- [ ] 实现内存限制
- [ ] 实现CPU限制
- [ ] 实现进程数限制
- [ ] 实现文件描述符限制

### 阶段5: 综合沙箱管理器（2周）
- [ ] 集成所有沙箱组件
- [ ] 实现配置管理
- [ ] 实现状态监控
- [ ] 实现错误恢复

### 阶段6: 集成与测试（2周）
- [ ] 集成到现有Guard系统
- [ ] 编写集成测试
- [ ] 进行安全审计
- [ ] 编写使用文档

---

## 10. 安全考虑

### 10.1 不可逆性
- Landlock一旦应用，无法移除或绕过
- seccomp过滤器一旦加载，无法修改
- 这是安全特性，确保沙箱无法被绕过

### 10.2 降级策略
- 如果内核不支持Landlock，降级到bwrap
- 如果bwrap不可用，降级到进程隔离
- 如果所有沙箱都不可用，拒绝执行

### 10.3 审计追踪
- 所有沙箱操作都记录到审计日志
- 记录沙箱应用、违规、降级等事件
- 支持事后分析和合规检查

### 10.4 性能影响
- Landlock: 几乎无性能影响（内核级检查）
- seccomp: 轻微性能影响（系统调用过滤）
- Namespace: 轻微性能影响（上下文切换）
- Cgroups: 资源限制带来的性能约束
