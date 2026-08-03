package security

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Action 5-layer permission outcome
type Action string

const (
	ActionAllow   Action = "allow"
	ActionDeny    Action = "deny"
	ActionConfirm Action = "confirm"
)

type Decision struct {
	Action  Action `json:"action"`
	Layer   string `json:"layer"` // L1..L5
	RuleID  string `json:"ruleId,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type PendingConfirm struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Reason    string         `json:"reason"`
	RuleID    string         `json:"ruleId"`
	Layer     string         `json:"layer"`
	CreatedAt time.Time      `json:"createdAt"`
}

type AwaitingResume struct {
	SessionID string
	Tool      string
	Args      map[string]any
	PermID    string
	Ready     bool
}

// Guard implements 5-layer defense.
type Guard struct {
	mu           sync.RWMutex
	sessionAllow map[string]map[string]bool
	pending      map[string]*PendingConfirm
	awaiting     map[string]*AwaitingResume
	denyStreak   map[string]int
	circuitLimit int
	workspace    string
	pathSandbox  bool
	confirmWrite bool
	denyRules    []rule
	confirmRules []rule
}

type rule struct {
	id, reason string
	re         *regexp.Regexp
	layer      string
}

func NewGuard(workspace string, pathSandbox, confirmWrite bool) *Guard {
	g := &Guard{
		sessionAllow: make(map[string]map[string]bool),
		pending:      make(map[string]*PendingConfirm),
		awaiting:     make(map[string]*AwaitingResume),
		denyStreak:   make(map[string]int),
		circuitLimit: 5,
		workspace:    workspace,
		pathSandbox:  pathSandbox,
		confirmWrite: confirmWrite,
	}
	g.initRules()
	return g
}

func (g *Guard) initRules() {
	denies := []struct{ id, pat, reason string }{
		{"rm_rf_root", `(?i)\brm\s+-rf?\s+/?(\s|$)`, "recursive delete root"},
		{"rm_rf_star", `(?i)\brm\s+-rf?\s+\*`, "recursive delete wildcard"},
		{"format", `(?i)\b(format|mkfs)\b`, "disk format"},
		{"shutdown", `(?i)\b(shutdown|poweroff|reboot)\b`, "power control"},
		{"dd", `(?i)\bdd\s+if=`, "dd disk write"},
		{"fork_bomb", `:\(\)\s*\{\s*:|:&`, "fork bomb"},
		{"force_push_main", `(?i)\bgit\s+push\s+(-f|--force).*(main|master)`, "force push main"},
	}
	for _, d := range denies {
		g.denyRules = append(g.denyRules, rule{id: d.id, reason: d.reason, re: regexp.MustCompile(d.pat), layer: "L1"})
	}
	confirms := []struct{ id, pat, reason string }{
		{"rm", `(?i)\brm\s+`, "delete files"},
		{"git_push", `(?i)\bgit\s+push\b`, "git push"},
		{"pip", `(?i)\bpip3?\s+install\b`, "pip install"},
		{"curl_pipe", `(?i)\b(curl|wget).*\|\s*(sh|bash)`, "pipe remote script"},
	}
	for _, c := range confirms {
		g.confirmRules = append(g.confirmRules, rule{id: c.id, reason: c.reason, re: regexp.MustCompile(c.pat), layer: "L3"})
	}
}

func (g *Guard) Check(sessionID, tool string, args map[string]any) Decision {
	summary := tool + ": " + fmt.Sprint(args)
	// L5 circuit
	g.mu.RLock()
	streak := g.denyStreak[sessionID]
	if g.sessionAllow[sessionID] != nil {
		if g.sessionAllow[sessionID]["*"] {
			g.mu.RUnlock()
			return Decision{Action: ActionAllow, Layer: "L4", Tool: tool, Summary: summary, Reason: "session approve all"}
		}
		if g.sessionAllow[sessionID][sig(tool, args)] {
			g.mu.RUnlock()
			return Decision{Action: ActionAllow, Layer: "L4", Tool: tool, Summary: summary}
		}
	}
	g.mu.RUnlock()
	if streak >= g.circuitLimit {
		return Decision{Action: ActionDeny, Layer: "L5", RuleID: "circuit", Reason: "too many denials", Tool: tool, Summary: summary}
	}

	// L1 deny on bash content
	cmd := extract(tool, args)
	for _, r := range g.denyRules {
		if cmd != "" && r.re.MatchString(cmd) {
			g.incDeny(sessionID)
			return Decision{Action: ActionDeny, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
		}
	}

	// L2 path sandbox
	if g.pathSandbox {
		if p := pathArg(tool, args); p != "" {
			if !g.underWorkspace(p) {
				g.incDeny(sessionID)
				return Decision{Action: ActionDeny, Layer: "L2", RuleID: "path_sandbox", Reason: "path outside workspace", Tool: tool, Summary: summary}
			}
			if sensitivePath(p) {
				return Decision{Action: ActionConfirm, Layer: "L2", RuleID: "sensitive_path", Reason: "sensitive path", Tool: tool, Summary: summary}
			}
		}
	}

	// L3 tool class
	switch tool {
	case "read_file", "glob", "grep":
		return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: summary}
	case "write_file", "edit_file":
		if g.confirmWrite {
			return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "write", Reason: "write/edit requires confirm", Tool: tool, Summary: summary}
		}
	case "bash":
		for _, r := range g.confirmRules {
			if cmd != "" && r.re.MatchString(cmd) {
				return Decision{Action: ActionConfirm, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
			}
		}
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "bash", Reason: "shell requires confirm", Tool: tool, Summary: summary}
	}
	return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: summary}
}

func (g *Guard) underWorkspace(p string) bool {
	if g.workspace == "" {
		return true
	}
	absW, _ := filepath.Abs(g.workspace)
	// relative paths resolved against workspace
	candidate := p
	if !filepath.IsAbs(p) {
		candidate = filepath.Join(absW, p)
	}
	absP, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absW, absP)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sensitivePath(p string) bool {
	lp := strings.ToLower(filepath.ToSlash(p))
	return strings.Contains(lp, ".ssh") || strings.Contains(lp, ".env") ||
		strings.Contains(lp, "id_rsa") || strings.Contains(lp, "credentials")
}

func (g *Guard) CreatePending(sessionID, tool string, args map[string]any, d Decision) *PendingConfirm {
	id := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	p := &PendingConfirm{
		ID: id, SessionID: sessionID, Tool: tool, Args: args,
		Reason: d.Reason, RuleID: d.RuleID, Layer: d.Layer, CreatedAt: time.Now(),
	}
	g.mu.Lock()
	g.pending[id] = p
	g.awaiting[sessionID] = &AwaitingResume{SessionID: sessionID, Tool: tool, Args: args, PermID: id, Ready: false}
	g.mu.Unlock()
	return p
}

func (g *Guard) Approve(id, scope string) (*PendingConfirm, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.pending[id]
	if !ok {
		return nil, fmt.Errorf("pending not found: %s", id)
	}
	delete(g.pending, id)
	if g.sessionAllow[p.SessionID] == nil {
		g.sessionAllow[p.SessionID] = map[string]bool{}
	}
	if scope == "session" || scope == "always" {
		g.sessionAllow[p.SessionID]["*"] = true
	} else {
		g.sessionAllow[p.SessionID][sig(p.Tool, p.Args)] = true
	}
	g.denyStreak[p.SessionID] = 0
	if a, ok := g.awaiting[p.SessionID]; ok && a.PermID == id {
		a.Ready = true
	} else {
		g.awaiting[p.SessionID] = &AwaitingResume{SessionID: p.SessionID, Tool: p.Tool, Args: p.Args, PermID: id, Ready: true}
	}
	return p, nil
}

func (g *Guard) Reject(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.pending[id]
	if !ok {
		return fmt.Errorf("pending not found")
	}
	delete(g.pending, id)
	if a, ok := g.awaiting[p.SessionID]; ok && a.PermID == id {
		delete(g.awaiting, p.SessionID)
	}
	g.denyStreak[p.SessionID]++
	return nil
}

func (g *Guard) ListPending(sessionID string) []*PendingConfirm {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*PendingConfirm
	for _, p := range g.pending {
		if sessionID == "" || p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out
}

func (g *Guard) TakeReadyResume(sessionID string) *AwaitingResume {
	g.mu.Lock()
	defer g.mu.Unlock()
	a, ok := g.awaiting[sessionID]
	if !ok || a == nil || !a.Ready {
		return nil
	}
	delete(g.awaiting, sessionID)
	cp := *a
	return &cp
}

func (g *Guard) incDeny(sessionID string) {
	g.mu.Lock()
	g.denyStreak[sessionID]++
	g.mu.Unlock()
}

func extract(tool string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch tool {
	case "bash":
		if c, ok := args["command"].(string); ok {
			return c
		}
	case "write_file", "edit_file", "read_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	return fmt.Sprint(args)
}

func pathArg(tool string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch tool {
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "glob", "grep":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	return ""
}

func sig(tool string, args map[string]any) string {
	return tool + "|" + extract(tool, args)
}
