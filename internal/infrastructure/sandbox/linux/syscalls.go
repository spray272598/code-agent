//go:build linux

package sandbox

import "syscall"

// =============================================================================
// 系统调用白名单和黑名单
// =============================================================================

// SafeSyscalls 安全的系统调用白名单
// 这些是编程语言运行时和常见操作所需的基本系统调用
var SafeSyscalls = []int{
	// 基本进程控制
	syscall.SYS_EXIT,
	syscall.SYS_EXIT_GROUP,
	syscall.SYS_GETPID,
	syscall.SYS_GETPPID,
	syscall.SYS_GETUID,
	syscall.SYS_GETGID,
	syscall.SYS_GETEUID,
	syscall.SYS_GETEGID,

	// 基本内存管理
	syscall.SYS_BRK,
	syscall.SYS_MMAP,
	syscall.SYS_MUNMAP,
	syscall.SYS_MPROTECT,
	syscall.SYS_MADVISE,
	syscall.SYS_MSYNC,
	syscall.SYS_MREMAP,

	// 基本文件 I/O
	syscall.SYS_READ,
	syscall.SYS_WRITE,
	syscall.SYS_PREAD64,
	syscall.SYS_PWRITE64,
	syscall.SYS_READV,
	syscall.SYS_WRITEV,
	syscall.SYS_PREADV,
	syscall.SYS_PWRITEV,

	// 文件系统操作
	syscall.SYS_OPEN,
	syscall.SYS_OPENAT,
	syscall.SYS_CLOSE,
	syscall.SYS_CREAT,
	syscall.SYS_STAT,
	syscall.SYS_FSTAT,
	syscall.SYS_LSTAT,
	syscall.SYS_NEWFSTATAT,
	syscall.SYS_STATX,
	syscall.SYS_ACCESS,
	syscall.SYS_FACCESSAT,
	syscall.SYS_LSEEK,
	syscall.SYS_LLSEEK,
	syscall.SYS_DUP,
	syscall.SYS_DUP2,
	syscall.SYS_DUP3,
	syscall.SYS_PIPE,
	syscall.SYS_PIPE2,

	// 目录操作
	syscall.SYS_GETDENTS,
	syscall.SYS_GETDENTS64,
	syscall.SYS_GETCWD,
	syscall.SYS_CHDIR,
	syscall.SYS_FCHDIR,
	syscall.SYS_MKDIR,
	syscall.SYS_MKDIRAT,
	syscall.SYS_RMDIR,
	syscall.SYS_UNLINK,
	syscall.SYS_UNLINKAT,
	syscall.SYS_RENAME,
	syscall.SYS_RENAMEAT,
	syscall.SYS_LINK,
	syscall.SYS_LINKAT,
	syscall.SYS_SYMLINK,
	syscall.SYS_SYMLINKAT,
	syscall.SYS_READLINK,
	syscall.SYS_READLINKAT,

	// 文件属性
	syscall.SYS_CHMOD,
	syscall.SYS_FCHMOD,
	syscall.SYS_FCHMODAT,
	syscall.SYS_CHOWN,
	syscall.SYS_FCHOWN,
	syscall.SYS_LCHOWN,
	syscall.SYS_FCHOWNAT,
	syscall.SYS_UTIME,
	syscall.SYS_UTIMENSAT,
	syscall.SYS_FUTIMESAT,

	// 文件锁
	syscall.SYS_FCNTL,
	syscall.SYS_FLOCK,
	syscall.SYS_LOCKF,

	// 同步
	syscall.SYS_FSYNC,
	syscall.SYS_FDATASYNC,
	syscall.SYS_SYNC,
	syscall.SYS_SYNCFS,

	// 进程管理
	syscall.SYS_FORK,
	syscall.SYS_VFORK,
	syscall.SYS_CLONE,
	syscall.SYS_CLONE3,
	syscall.SYS_EXECVE,
	syscall.SYS_EXECVEAT,
	syscall.SYS_WAIT4,
	syscall.SYS_WAITID,
	syscall.SYS_KILL,
	syscall.SYS_TGKILL,
	syscall.SYS_TKILL,
	syscall.SYS_SETUID,
	syscall.SYS_SETGID,
	syscall.SYS_SETREUID,
	syscall.SYS_SETREGID,
	syscall.SYS_SETRESUID,
	syscall.SYS_SETRESGID,
	syscall.SYS_SETFSUID,
	syscall.SYS_SETFSGID,
	syscall.SYS_GETSID,
	syscall.SYS_SETSID,
	syscall.SYS_SETPGID,
	syscall.SYS_GETPGID,
	syscall.SYS_GETPGRP,

	// 信号
	syscall.SYS_RT_SIGACTION,
	syscall.SYS_RT_SIGPROCMASK,
	syscall.SYS_RT_SIGPENDING,
	syscall.SYS_RT_SIGSUSPEND,
	syscall.SYS_RT_SIGTIMEDWAIT,
	syscall.SYS_RT_SIGQUEUEINFO,
	syscall.SYS_RT_TGSIGQUEUEINFO,
	syscall.SYS_SIGALTSTACK,
	syscall.SYS_SIGRETURN,
	syscall.SYS_RT_SIGRETURN,

	// 定时器
	syscall.SYS_NANOSLEEP,
	syscall.SYS_CLOCK_GETTIME,
	syscall.SYS_CLOCK_GETRES,
	syscall.SYS_CLOCK_NANOSLEEP,
	syscall.SYS_TIMER_CREATE,
	syscall.SYS_TIMER_SETTIME,
	syscall.SYS_TIMER_GETTIME,
	syscall.SYS_TIMER_GETOVERRUN,
	syscall.SYS_TIMER_DELETE,

	// 调度
	syscall.SYS_SCHED_YIELD,
	syscall.SYS_SCHED_GETAFFINITY,
	syscall.SYS_SCHED_SETAFFINITY,
	syscall.SYS_SCHED_SETPARAM,
	syscall.SYS_SCHED_GETPARAM,
	syscall.SYS_SCHED_SETSCHEDULER,
	syscall.SYS_SCHED_GETSCHEDULER,
	syscall.SYS_SCHED_GET_PRIORITY_MAX,
	syscall.SYS_SCHED_GET_PRIORITY_MIN,
	syscall.SYS_SCHED_RR_GET_INTERVAL,

	// 系统信息
	syscall.SYS_UNAME,
	syscall.SYS_SYSINFO,
	syscall.SYS_GETRUSAGE,
	syscall.SYS_GETRLIMIT,
	syscall.SYS_SETRLIMIT,
	syscall.SYS_GETTIMEOFDAY,
	syscall.SYS_TIMES,

	// 网络（基础）
	syscall.SYS_SOCKET,
	syscall.SYS_BIND,
	syscall.SYS_LISTEN,
	syscall.SYS_ACCEPT,
	syscall.SYS_ACCEPT4,
	syscall.SYS_CONNECT,
	syscall.SYS_SHUTDOWN,
	syscall.SYS_GETSOCKNAME,
	syscall.SYS_GETPEERNAME,
	syscall.SYS_SETSOCKOPT,
	syscall.SYS_GETSOCKOPT,
	syscall.SYS_SENDTO,
	syscall.SYS_RECVFROM,
	syscall.SYS_SENDMSG,
	syscall.SYS_RECVMSG,
	syscall.SYS_SENDMMSG,
	syscall.SYS_RECVMMSG,
	syscall.SYS_SOCKETPAIR,

	// 轮询/事件
	syscall.SYS_SELECT,
	syscall.SYS_POLL,
	syscall.SYS_PPOLL,
	syscall.SYS_EPOLL_CREATE,
	syscall.SYS_EPOLL_CREATE1,
	syscall.SYS_EPOLL_CTL,
	syscall.SYS_EPOLL_WAIT,
	syscall.SYS_EPOLL_PWAIT,

	// 进程间通信
	syscall.SYS_PIPE2,
	syscall.SYS_EVENTFD,
	syscall.SYS_EVENTFD2,
	syscall.SYS_SIGNEFD,
	syscall.SYS_SIGNEFD4,
	syscall.SYS_INOTIFY_INIT,
	syscall.SYS_INOTIFY_INIT1,
	syscall.SYS_INOTIFY_ADD_WATCH,
	syscall.SYS_INOTIFY_RM_WATCH,

	// 扩展属性
	syscall.SYS_SETXATTR,
	syscall.SYS_LSETXATTR,
	syscall.SYS_FSETXATTR,
	syscall.SYS_GETXATTR,
	syscall.SYS_LGETXATTR,
	syscall.SYS_FGETXATTR,
	syscall.SYS_LISTXATTR,
	syscall.SYS_LLISTXATTR,
	syscall.SYS_FLISTXATTR,
	syscall.SYS_REMOVEXATTR,
	syscall.SYS_LREMOVEXATTR,
	syscall.SYS_FREMOVEXATTR,

	// 文件描述符操作
	syscall.SYS_IOCTL,
	syscall.SYS_PREADV2,
	syscall.SYS_PWRITEV2,
	syscall.SYS_COPY_FILE_RANGE,

	// 进程内存
	syscall.SYS_PROCESS_VM_READV,
	syscall.SYS_PROCESS_VM_WRITEV,

	// 用户命名空间
	syscall.SYS_UNSHARE,
	syscall.SYS_SETNS,
	syscall.SYS_GETCPU,
	syscall.SYS_GETRANDOM,
}

// DangerousSyscalls 危险的系统调用黑名单
// 这些系统调用可能被用于逃逸沙箱或进行恶意操作
var DangerousSyscalls = []int{
	// 内核模块管理
	syscall.SYS_INIT_MODULE,
	syscall.SYS_FINIT_MODULE,
	syscall.SYS_DELETE_MODULE,

	// 系统修改
	syscall.SYS_SETHOSTNAME,
	syscall.SYS_SETDOMAINNAME,
	syscall.SYS_REBOOT,
	syscall.SYS_SWAPON,
	syscall.SYS_SWAPOFF,
	syscall.SYS_MOUNT,
	syscall.SYS_UMOUNT2,
	syscall.SYS_PIVOT_ROOT,

	// 调试和跟踪
	syscall.SYS_PTRACE,
	syscall.SYS_PROCESS_VM_READV,
	syscall.SYS_PROCESS_VM_WRITEV,

	// 直接 I/O
	syscall.SYS_IO_SETUP,
	syscall.SYS_IO_DESTROY,
	syscall.SYS_IO_GETEVENTS,
	syscall.SYS_IO_SUBMIT,
	syscall.SYS_IO_CANCEL,
	syscall.SYS_IO_PGETEVENTS,

	// 性能监控
	syscall.SYS_PERF_EVENT_OPEN,

	// BPF
	syscall.SYS_BPF,

	// kexec
	syscall.SYS_KEXEC_LOAD,
	syscall.SYS_KEXEC_FILE_LOAD,

	// 用户命名空间（如果不需要）
	// syscall.SYS_UNSHARE,
	// syscall.SYS_SETNS,

	// 容器相关
	syscall.SYS_CLONE,
	syscall.SYS_CLONE3,
	// 注意：如果需要 fork/exec，则需要 CLONE
	// 但在严格沙箱中可能需要限制

	// 文件系统高级操作
	syscall.SYS_FANOTIFY_INIT,
	syscall.SYS_FANOTIFY_MARK,
	syscall.SYS_NAME_TO_HANDLE_AT,
	syscall.SYS_OPEN_BY_HANDLE_AT,

	// 内存锁定
	syscall.SYS_MLOCK,
	syscall.SYS_MUNLOCK,
	syscall.SYS_MLOCKALL,
	syscall.SYS_MUNLOCKALL,
	syscall.SYS_MLOCK2,

	// 资源限制修改
	syscall.SYS_SETRLIMIT,
	syscall.SYS_PRIOITY_SET,
	syscall.SYS_PRIOITY_GET,

	// 系统时间修改
	syscall.SYS_SETTIMEOFDAY,
	syscall.SYS_CLOCK_SETTIME,
	syscall.SYS_ADJTIMEX,

	// 审计
	syscall.SYS_AUDIT,
	syscall.SYS_AUDITON,

	// 安全
	syscall.SYS_SECCOMP,
}

// GoRuntimeSyscalls Go 运行时需要的系统调用
// Go 运行时在启动和运行时需要一些特定的系统调用
var GoRuntimeSyscalls = []int{
	// 线程管理
	syscall.SYS_CLONE,
	syscall.SYS_CLONE3,
	syscall.SYS_SET_TID_ADDRESS,
	syscall.SYS_GETTID,
	syscall.SYS_TKILL,
	syscall.SYS_TGKILL,
	syscall.SYS_FUTEX,
	syscall.SYS_SET_ROBUST_LIST,
	syscall.SYS_GET_ROBUST_LIST,

	// 内存管理
	syscall.SYS_MMAP,
	syscall.SYS_MUNMAP,
	syscall.SYS_MPROTECT,
	syscall.SYS_MADVISE,
	syscall.SYS_MSYNC,
	syscall.SYS_MREMAP,
	syscall.SYS_MREMAP,

	// 信号处理
	syscall.SYS_RT_SIGACTION,
	syscall.SYS_RT_SIGPROCMASK,
	syscall.SYS_RT_SIGRETURN,
	syscall.SYS_SIGALTSTACK,

	// 定时器
	syscall.SYS_CLOCK_GETTIME,
	syscall.SYS_CLOCK_GETRES,
	syscall.SYS_NANOSLEEP,
	syscall.SYS_TIMER_CREATE,
	syscall.SYS_TIMER_SETTIME,
	syscall.SYS_TIMER_GETTIME,
	syscall.SYS_TIMER_DELETE,

	// 文件 I/O
	syscall.SYS_READ,
	syscall.SYS_WRITE,
	syscall.SYS_PREAD64,
	syscall.SYS_PWRITE64,
	syscall.SYS_OPENAT,
	syscall.SYS_CLOSE,
	syscall.SYS_DUP,
	syscall.SYS_DUP2,
	syscall.SYS_DUP3,
	syscall.SYS_FCNTL,
	syscall.SYS_IOCTL,
	syscall.SYS_STATX,
	syscall.SYS_NEWFSTATAT,
	syscall.SYS_GETDENTS64,

	// 网络
	syscall.SYS_SOCKET,
	syscall.SYS_BIND,
	syscall.SYS_LISTEN,
	syscall.SYS_ACCEPT4,
	syscall.SYS_CONNECT,
	syscall.SYS_GETSOCKNAME,
	syscall.SYS_GETPEERNAME,
	syscall.SYS_SETSOCKOPT,
	syscall.SYS_GETSOCKOPT,
	syscall.SYS_SENDTO,
	syscall.SYS_RECVFROM,
	syscall.SYS_SENDMSG,
	syscall.SYS_RECVMSG,
	syscall.SYS_EPOLL_CREATE1,
	syscall.SYS_EPOLL_CTL,
	syscall.SYS_EPOLL_PWAIT,

	// 进程
	syscall.SYS_FORK,
	syscall.SYS_VFORK,
	syscall.SYS_EXECVE,
	syscall.SYS_EXECVEAT,
	syscall.SYS_WAIT4,
	syscall.SYS_EXIT_GROUP,
	syscall.SYS_GETPID,
	syscall.SYS_GETPPID,
	syscall.SYS_GETUID,
	syscall.SYS_GETGID,
	syscall.SYS_GETEUID,
	syscall.SYS_GETEGID,

	// 调度
	syscall.SYS_SCHED_YIELD,
	syscall.SYS_SCHED_GETAFFINITY,
	syscall.SYS_SCHED_SETAFFINITY,

	// 系统信息
	syscall.SYS_UNAME,
	syscall.SYS_GETRUSAGE,
	syscall.SYS_GETRLIMIT,
	syscall.SYS_GETTIMEOFDAY,

	// 其他
	syscall.SYS_GETRANDOM,
	syscall.SYS_ARCH_PRCTL,
	syscall.SYS_PRLIMIT64,
	syscall.SYS_RSEQ,
}

// MinimalGoSyscalls 最小 Go 运行时系统调用
// 用于严格沙箱模式
var MinimalGoSyscalls = []int{
	syscall.SYS_READ,
	syscall.SYS_WRITE,
	syscall.SYS_PREAD64,
	syscall.SYS_PWRITE64,
	syscall.SYS_OPENAT,
	syscall.SYS_CLOSE,
	syscall.SYS_STATX,
	syscall.SYS_NEWFSTATAT,
	syscall.SYS_GETDENTS64,
	syscall.SYS_LSEEK,
	syscall.SYS_MMAP,
	syscall.SYS_MUNMAP,
	syscall.SYS_MPROTECT,
	syscall.SYS_BRK,
	syscall.SYS_FUTEX,
	syscall.SYS_NANOSLEEP,
	syscall.SYS_CLOCK_GETTIME,
	syscall.SYS_GETPID,
	syscall.SYS_EXIT_GROUP,
	syscall.SYS_GETUID,
	syscall.SYS_GETGID,
	syscall.SYS_GETEUID,
	syscall.SYS_GETEGID,
	syscall.SYS_UNAME,
	syscall.SYS_GETRUSAGE,
	syscall.SYS_GETTIMEOFDAY,
	syscall.SYS_GETRANDOM,
}