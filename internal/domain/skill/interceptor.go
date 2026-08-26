package skill

import (
	"context"
	"errors"
	"sync"
)

var ErrSkillBlockedTool = errors.New("skill blocked this tool")

type ToolInterceptor interface {
	Intercept(ctx context.Context, toolName string, args map[string]any) error
	IsBlocked(toolName string) bool
	GetActiveSkill() *Skill
}

type BlockInterceptor struct {
	mu           sync.RWMutex
	skillSvc     *Service
	activeSkill  *Skill
	blockedTools map[string]bool
}

func NewBlockInterceptor(skillSvc *Service) *BlockInterceptor {
	return &BlockInterceptor{
		skillSvc:     skillSvc,
		blockedTools: make(map[string]bool),
	}
}

func (b *BlockInterceptor) SetActiveSkill(skill *Skill) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeSkill = skill
	b.rebuildBlocked()
}

func (b *BlockInterceptor) ClearActiveSkill() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeSkill = nil
	b.blockedTools = make(map[string]bool)
}

func (b *BlockInterceptor) rebuildBlocked() {
	b.blockedTools = make(map[string]bool)
	if b.activeSkill == nil {
		return
	}

	blocked := b.activeSkill.BlockTools
	if blocked == nil {
		return
	}

	for _, toolName := range blocked.Tools {
		b.blockedTools[toolName] = true
	}

	blockedPrefixes := blocked.Prefixes
	for _, prefix := range blockedPrefixes {
		b.blockedTools["prefix:"+prefix] = true
	}
}

func (b *BlockInterceptor) Intercept(ctx context.Context, toolName string, args map[string]any) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.blockedTools) == 0 {
		return nil
	}

	if blocked, ok := b.blockedTools[toolName]; ok && blocked {
		return &BlockedToolError{
			ToolName: toolName,
			SkillID:  b.activeSkill.ID,
			Reason:   "tool explicitly blocked by skill policy",
		}
	}

	for key := range b.blockedTools {
		if len(key) > 7 && key[:7] == "prefix:" {
			prefix := key[7:]
			if len(toolName) >= len(prefix) && toolName[:len(prefix)] == prefix {
				return &BlockedToolError{
					ToolName: toolName,
					SkillID:  b.activeSkill.ID,
					Reason:   "tool prefix blocked by skill policy",
				}
			}
		}
	}

	return nil
}

func (b *BlockInterceptor) IsBlocked(toolName string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if blocked, ok := b.blockedTools[toolName]; ok && blocked {
		return true
	}

	for key := range b.blockedTools {
		if len(key) > 7 && key[:7] == "prefix:" {
			prefix := key[7:]
			if len(toolName) >= len(prefix) && toolName[:len(prefix)] == prefix {
				return true
			}
		}
	}

	return false
}

func (b *BlockInterceptor) GetActiveSkill() *Skill {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.activeSkill
}

type BlockedToolError struct {
	ToolName string
	SkillID  string
	Reason   string
}

func (e *BlockedToolError) Error() string {
	return "skill " + e.SkillID + " blocked tool " + e.ToolName + ": " + e.Reason
}

func IsBlockedToolError(err error) bool {
	var b *BlockedToolError
	return errors.As(err, &b)
}

func GetBlockedToolError(err error) *BlockedToolError {
	var b *BlockedToolError
	if errors.As(err, &b) {
		return b
	}
	return nil
}

type BlockedToolsConfig struct {
	Tools    []string `json:"tools,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
	Message  string   `json:"message,omitempty"`
}
