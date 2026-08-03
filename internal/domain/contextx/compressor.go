package contextx

import (
	"github.com/spray272598/code-agent/internal/types/common"
)

// Compressor multi-level history reduction (walicode-inspired).
type Compressor struct {
	TokenBudget    int
	MessageBudget  int
	KeepRecent     int
}

func NewCompressor(tokenBudget int) *Compressor {
	if tokenBudget <= 0 {
		tokenBudget = 16000
	}
	return &Compressor{TokenBudget: tokenBudget, MessageBudget: 50, KeepRecent: 8}
}

func (c *Compressor) Needs(history []map[string]any) bool {
	if len(history) > c.MessageBudget {
		return true
	}
	return estimateHistory(history) > c.TokenBudget*4/5
}

// Compress L0 truncate contents + sliding keep recent + priority for tool errors.
func (c *Compressor) Compress(history []map[string]any) (out []map[string]any, saved int) {
	if len(history) == 0 {
		return history, 0
	}
	before := estimateHistory(history)
	// L0: truncate long contents
	trimmed := make([]map[string]any, len(history))
	for i, m := range history {
		cp := copyMap(m)
		if content, ok := cp["content"].(string); ok && len([]rune(content)) > 2000 {
			cp["content"] = common.TruncateRunes(content, 2000)
		}
		trimmed[i] = cp
	}
	// L1 sliding: keep system-like high priority + recent
	if len(trimmed) <= c.KeepRecent {
		return trimmed, before - estimateHistory(trimmed)
	}
	// keep first user+assistant pairs that look critical + last N
	keep := map[int]bool{}
	for i := len(trimmed) - c.KeepRecent; i < len(trimmed); i++ {
		if i >= 0 {
			keep[i] = true
		}
	}
	// keep high priority
	for i, m := range trimmed {
		if pri, ok := m["priority"].(int); ok && pri >= 5 {
			keep[i] = true
		}
		if role, _ := m["role"].(string); role == "tool" {
			if content, _ := m["content"].(string); containsErr(content) {
				keep[i] = true
			}
		}
	}
	out = make([]map[string]any, 0, len(keep))
	for i, m := range trimmed {
		if keep[i] {
			out = append(out, m)
		}
	}
	// if still over budget, hard keep last KeepRecent
	if estimateHistory(out) > c.TokenBudget && len(trimmed) > c.KeepRecent {
		out = trimmed[len(trimmed)-c.KeepRecent:]
	}
	return out, before - estimateHistory(out)
}

func estimateHistory(msgs []map[string]any) int {
	n := 0
	for _, m := range msgs {
		if c, ok := m["content"].(string); ok {
			n += common.EstimateTokens(c)
		}
	}
	return n
}

func copyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func containsErr(s string) bool {
	ls := s
	return len(ls) > 0 && (containsFold(ls, "error") || containsFold(ls, "失败") || containsFold(ls, "failed"))
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			// simple case-insensitive for ascii
			for i := 0; i+len(sub) <= len(s); i++ {
				match := true
				for j := 0; j < len(sub); j++ {
					a, b := s[i+j], sub[j]
					if a >= 'A' && a <= 'Z' {
						a += 32
					}
					if b >= 'A' && b <= 'Z' {
						b += 32
					}
					if a != b {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
			return false
		})())
}
