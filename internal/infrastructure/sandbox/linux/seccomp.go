package sandbox

// =============================================================================
// seccomp-bpf 系统调用过滤器 (简化版)
// =============================================================================

// BPFInstr BPF 指令
type BPFInstr struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

// SeccompFilter seccomp 过滤器 (简化版)
type SeccompFilter struct {
	rules []BPFInstr
}

// NewSeccompFilter 创建 seccomp 过滤器
func NewSeccompFilter() *SeccompFilter {
	return &SeccompFilter{
		rules: make([]BPFInstr, 0),
	}
}

// AddAllowedSyscall 添加允许的系统调用 (存根)
func (f *SeccompFilter) AddAllowedSyscall(syscallNum int) {
	// 存根实现
}

// AddDeniedSyscall 添加拒绝的系统调用 (存根)
func (f *SeccompFilter) AddDeniedSyscall(syscallNum int, errCode int) {
	// 存根实现
}

// Apply 应用 seccomp 过滤器 (存根)
func (f *SeccompFilter) Apply() error {
	// 存根实现
	return nil
}

// GetRules 获取规则
func (f *SeccompFilter) GetRules() []BPFInstr {
	return f.rules
}