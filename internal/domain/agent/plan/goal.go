package plan

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Goal represents a durable objective with lifecycle management.
type Goal struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	State       GoalState      `json:"state"`
	BlockReason *GoalBlockReason `json:"block_reason,omitempty"`
	Revision    int            `json:"revision"` // compare-and-set identity
	Round       int            `json:"round"`   // continuation rounds used
	MaxRounds   int            `json:"max_rounds"` // max allowed rounds (0 = unlimited)
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// GoalManager tracks multiple goals with lifecycle management.
type GoalManager struct {
	mu    sync.RWMutex
	goals map[string]*Goal
	order []string // insertion order
}

// NewGoalManager creates a new goal manager.
func NewGoalManager() *GoalManager {
	return &GoalManager{
		goals: make(map[string]*Goal),
	}
}

// CreateGoal adds a new goal with active state.
func (gm *GoalManager) CreateGoal(id, title, description string, maxRounds int) *Goal {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	now := time.Now()
	goal := &Goal{
		ID:          id,
		Title:       title,
		Description: description,
		State:       GoalActive,
		Revision:    1,
		Round:       0,
		MaxRounds:   maxRounds,
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    make(map[string]string),
	}
	gm.goals[id] = goal
	gm.order = append(gm.order, id)
	return goal
}

// GetGoal returns a goal by ID.
func (gm *GoalManager) GetGoal(id string) (*Goal, bool) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	g, ok := gm.goals[id]
	return g, ok
}

// UpdateGoal modifies a goal's title, description, or metadata.
func (gm *GoalManager) UpdateGoal(id, title, description string) (*Goal, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return nil, fmt.Errorf("goal %s not found", id)
	}
	if title != "" {
		g.Title = title
	}
	if description != "" {
		g.Description = description
	}
	g.Revision++
	g.UpdatedAt = time.Now()
	return g, nil
}

// PauseGoal sets a goal to paused state.
func (gm *GoalManager) PauseGoal(id string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	if g.State != GoalActive {
		return fmt.Errorf("goal %s is not active (state: %s)", id, g.State)
	}
	g.State = GoalPaused
	g.Revision++
	g.UpdatedAt = time.Now()
	return nil
}

// ResumeGoal resumes a paused goal.
func (gm *GoalManager) ResumeGoal(id string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	if g.State != GoalPaused {
		return fmt.Errorf("goal %s is not paused (state: %s)", id, g.State)
	}
	g.State = GoalActive
	g.Revision++
	g.UpdatedAt = time.Now()
	return nil
}

// BlockGoal sets a goal to blocked state with a reason.
func (gm *GoalManager) BlockGoal(id string, reason GoalBlockReason) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	g.State = GoalBlocked
	g.BlockReason = &reason
	g.Revision++
	g.UpdatedAt = time.Now()
	return nil
}

// CompleteGoal marks a goal as complete.
func (gm *GoalManager) CompleteGoal(id string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	g.State = GoalComplete
	g.Revision++
	g.UpdatedAt = time.Now()
	return nil
}

// ConsumeRound increments the round counter and checks budget.
// Returns false if the goal has exceeded its round budget.
func (gm *GoalManager) ConsumeRound(id string) (bool, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	g, ok := gm.goals[id]
	if !ok {
		return false, fmt.Errorf("goal %s not found", id)
	}
	if g.State != GoalActive {
		return false, fmt.Errorf("goal %s is not active (state: %s)", id, g.State)
	}
	if g.MaxRounds > 0 && g.Round >= g.MaxRounds {
		return false, nil // budget exceeded
	}
	g.Round++
	g.Revision++
	g.UpdatedAt = time.Now()
	return true, nil
}

// ActiveGoals returns all goals in active state.
func (gm *GoalManager) ActiveGoals() []*Goal {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	var active []*Goal
	for _, id := range gm.order {
		g := gm.goals[id]
		if g.State == GoalActive {
			active = append(active, g)
		}
	}
	return active
}

// AllGoals returns all goals in insertion order.
func (gm *GoalManager) AllGoals() []*Goal {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	all := make([]*Goal, 0, len(gm.order))
	for _, id := range gm.order {
		all = append(all, gm.goals[id])
	}
	return all
}

// RemoveGoal removes a goal by ID.
func (gm *GoalManager) RemoveGoal(id string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if _, ok := gm.goals[id]; !ok {
		return fmt.Errorf("goal %s not found", id)
	}
	delete(gm.goals, id)
	// Remove from order
	for i, oid := range gm.order {
		if oid == id {
			gm.order = append(gm.order[:i], gm.order[i+1:]...)
			break
		}
	}
	return nil
}

// HasBlockingGoals returns true if any goals are blocked.
func (gm *GoalManager) HasBlockingGoals() bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	for _, id := range gm.order {
		if gm.goals[id].State == GoalBlocked {
			return true
		}
	}
	return false
}

// GoalSummary returns a summary string for prompt injection.
func (gm *GoalManager) GoalSummary() string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	if len(gm.goals) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Goals\n")
	for _, id := range gm.order {
		g := gm.goals[id]
		marker := "[active]"
		switch g.State {
		case GoalPaused:
			marker = "[paused]"
		case GoalBlocked:
			marker = "[blocked]"
		case GoalComplete:
			marker = "[complete]"
		}
		b.WriteString(fmt.Sprintf("- %s %s (round %d", marker, g.Title, g.Round))
		if g.MaxRounds > 0 {
			b.WriteString(fmt.Sprintf("/%d", g.MaxRounds))
		}
		b.WriteString(")\n")
		if g.BlockReason != nil {
			b.WriteString(fmt.Sprintf("  reason: %s - %s\n", g.BlockReason.Code, g.BlockReason.Message))
		}
	}
	return b.String()
}
