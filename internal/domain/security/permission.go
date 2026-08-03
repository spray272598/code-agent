package security

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Action string

const (
	ActionAllow   Action = "allow"
	ActionDeny    Action = "deny"
	ActionConfirm Action = "confirm"
)

type Decision struct {
	Action  Action `json:"action"`
	Layer   string `json:"layer"`
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

// Guard implements 5-layer defense with command normalization.
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
	// tools that are always allow / confirm / deny by name class
	readTools  map[string]bool
	writeTools map[string]bool
	// MCP/unknown tools: confirm by default
	mcpConfirm bool
}

type rule struct {
	id, reason string
	patterns   []*regexp.Regexp // multi-pattern
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
		mcpConfirm:   true,
		readTools: map[string]bool{
			"read_file": true, "glob": true, "grep": true, "memory_search": true,
		},
		writeTools: map[string]bool{
			"write_file": true, "edit_file": true, "memory_save": true,
		},
	}
	g.initRules()
	return g
}

func (g *Guard) initRules() {
	// Multiple patterns per rule to catch spacing / flag-order variants after normalize
	denies := []struct {
		id, reason string
		pats       []string
	}{
		{"rm_rf_root", "recursive delete root", []string{
			`(?i)\brm\s+(-[a-z]*f[a-z]*r[a-z]*|-[a-z]*r[a-z]*f[a-z]*)\s+/?(\s|$)`,
			`(?i)\brm\s+-rf?\s+/?(\s|$)`,
			`(?i)\brm\s+/s\s+/q\s+\\?\s*$`, // windows-ish
			`rm-rf/`, `rm-fr/`,
			// jammed / no-space / semicolon forms (matched on nospace variant)
			`(?i)rm-rf/?`, `(?i)rm-fr/?`, `(?i)rmrf/`,
		}},
		{"rm_rf_star", "recursive delete wildcard", []string{
			`(?i)\brm\s+(-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*)\s+\*`,
			`(?i)\brm\s+-rf?\s+\*`,
			`rm-rf\*`, `(?i)rm-rf\*`, `(?i)rmrf\*`,
		}},
		{"format", "disk format", []string{`(?i)\b(format|mkfs(\.\w+)?)\b`}},
		{"shutdown", "power control", []string{`(?i)\b(shutdown|poweroff|reboot|halt)\b`}},
		{"dd", "dd disk write", []string{`(?i)\bdd\s+.*\bof=`, `(?i)\bdd\s+if=`}},
		{"fork_bomb", "fork bomb", []string{`:\(\)\s*\{\s*:|:&`, `:\(\)\{:`}},
		{"force_push_main", "force push main", []string{
			`(?i)\bgit\s+push\s+(-f|--force).*(main|master)`,
			`(?i)\bgit\s+push\s+.*(main|master).*(-f|--force)`,
		}},
		{"curl_pipe_sh", "pipe remote script", []string{
			`(?i)\b(curl|wget).*\|\s*(ba)?sh\b`,
			`(?i)\b(curl|wget).*\|\s*bash\b`,
		}},
	}
	for _, d := range denies {
		var res []*regexp.Regexp
		for _, p := range d.pats {
			res = append(res, regexp.MustCompile(p))
		}
		g.denyRules = append(g.denyRules, rule{id: d.id, reason: d.reason, patterns: res, layer: "L1"})
	}
	confirms := []struct {
		id, reason string
		pats       []string
	}{
		{"rm", "delete files", []string{`(?i)\brm\s+`, `(?i)\bdel\s+`, `(?i)\bRemove-Item\b`}},
		{"git_push", "git push", []string{`(?i)\bgit\s+push\b`}},
		{"pip", "pip install", []string{`(?i)\bpip3?\s+install\b`, `(?i)\bpython\s+-m\s+pip\s+install\b`}},
		{"npm_g", "global npm", []string{`(?i)\bnpm\s+i(nstall)?\s+-g\b`}},
		{"chmod", "chmod", []string{`(?i)\bchmod\s+`}},
		{"sudo", "sudo", []string{`(?i)\bsudo\s+`}},
	}
	for _, c := range confirms {
		var res []*regexp.Regexp
		for _, p := range c.pats {
			res = append(res, regexp.MustCompile(p))
		}
		g.confirmRules = append(g.confirmRules, rule{id: c.id, reason: c.reason, patterns: res, layer: "L3"})
	}
}

func matchAny(rules []rule, variants []string) *rule {
	for i := range rules {
		r := &rules[i]
		for _, v := range variants {
			for _, re := range r.patterns {
				if re.MatchString(v) {
					return r
				}
			}
		}
	}
	return nil
}

func (g *Guard) Check(sessionID, tool string, args map[string]any) Decision {
	summary := tool + ": " + fmt.Sprint(args)
	tool = strings.TrimSpace(tool)

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

	// L1 deny — all command variants
	cmd := extract(tool, args)
	variants := CommandVariants(cmd)
	if r := matchAny(g.denyRules, variants); r != nil {
		g.incDeny(sessionID)
		return Decision{Action: ActionDeny, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
	}

	// L2 path sandbox (also normalize path tricks / URL / Unicode)
	if g.pathSandbox {
		if p := pathArg(tool, args); p != "" {
			norm := NormalizePathArg(p)
			if !g.underWorkspace(norm) {
				g.incDeny(sessionID)
				return Decision{Action: ActionDeny, Layer: "L2", RuleID: "path_sandbox", Reason: "path outside workspace", Tool: tool, Summary: summary}
			}
			if sensitivePath(norm) || sensitivePath(p) {
				return Decision{Action: ActionConfirm, Layer: "L2", RuleID: "sensitive_path", Reason: "sensitive path", Tool: tool, Summary: summary}
			}
		}
	}

	// L3 tool class
	base := toolBaseName(tool) // strip server__ prefix for MCP
	if g.readTools[tool] || g.readTools[base] {
		return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: summary}
	}
	if g.writeTools[tool] || g.writeTools[base] {
		if g.confirmWrite {
			return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "write", Reason: "write/edit requires confirm", Tool: tool, Summary: summary}
		}
	}
	if tool == "bash" || base == "bash" || tool == "run_command" {
		if r := matchAny(g.confirmRules, variants); r != nil {
			return Decision{Action: ActionConfirm, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
		}
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "bash", Reason: "shell requires confirm", Tool: tool, Summary: summary}
	}
	if tool == "delegate" {
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "delegate", Reason: "subagent delegation requires confirm", Tool: tool, Summary: summary}
	}

	// MCP / unknown tools — never silent allow; still scan string args for shell deny patterns
	if g.mcpConfirm || isMCPTool(tool) {
		if r := matchAny(g.denyRules, variants); r != nil {
			g.incDeny(sessionID)
			return Decision{Action: ActionDeny, Layer: "L1", RuleID: "mcp_" + r.id, Reason: "mcp args matched deny: " + r.reason, Tool: tool, Summary: summary}
		}
		// path sandbox already applied when path arg present
		// read-like MCP still requires confirm if args look like shell
		if looksReadOnlyMCP(tool) && !looksDangerousArgs(args) {
			return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: summary, Reason: "mcp read-like"}
		}
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "mcp_or_unknown",
			Reason: "MCP/unknown tool requires confirm", Tool: tool, Summary: summary}
	}
	return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "unknown_tool", Reason: "unknown tool requires confirm", Tool: tool, Summary: summary}
}

func toolBaseName(tool string) string {
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		return tool[i+2:]
	}
	return tool
}

func isMCPTool(name string) bool {
	// registered as server__tool or tagged in description; name heuristic
	return strings.Contains(name, "__")
}

func looksReadOnlyMCP(name string) bool {
	n := strings.ToLower(toolBaseName(name))
	// write/exec names never auto-allow
	for _, k := range []string{"write", "delete", "remove", "exec", "run", "bash", "shell", "eval", "put", "create", "drop"} {
		if strings.Contains(n, k) {
			return false
		}
	}
	for _, k := range []string{"read", "get", "list", "search", "find", "fetch", "time", "echo", "info", "stat"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

func looksDangerousArgs(args map[string]any) bool {
	if args == nil {
		return false
	}
	for _, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		low := strings.ToLower(s)
		if strings.Contains(low, "rm -") || strings.Contains(low, "curl ") ||
			strings.Contains(low, "| sh") || strings.Contains(low, "| bash") ||
			strings.Contains(low, "../") {
			return true
		}
	}
	return false
}

func (g *Guard) underWorkspace(p string) bool {
	if g.workspace == "" {
		return true
	}
	// reject invalid UTF-8 / null from NormalizePathArg
	if p == "" || strings.Contains(p, "\x00") {
		return false
	}
	absW, err := filepath.Abs(g.workspace)
	if err != nil {
		return false
	}
	// check all variants (encoding bypass)
	for _, v := range PathVariants(p) {
		if v == "" || strings.Contains(v, "\x00") {
			return false
		}
		candidate := v
		if !filepath.IsAbs(filepath.FromSlash(v)) {
			candidate = filepath.Join(absW, filepath.FromSlash(v))
		} else {
			candidate = filepath.FromSlash(v)
		}
		absP, err := filepath.Abs(candidate)
		if err != nil {
			return false
		}
		absP = filepath.Clean(absP)
		rel, err := filepath.Rel(absW, absP)
		if err != nil {
			return false
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func sensitivePath(p string) bool {
	lp := strings.ToLower(filepath.ToSlash(p))
	return strings.Contains(lp, ".ssh") || strings.Contains(lp, ".env") ||
		strings.Contains(lp, "id_rsa") || strings.Contains(lp, "credentials") ||
		strings.Contains(lp, "secret") || strings.Contains(lp, "wallet")
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
	switch toolBaseName(tool) {
	case "bash", "run_command":
		if c, ok := args["command"].(string); ok {
			return c
		}
	case "write_file", "edit_file", "read_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	// MCP tools: join string args for pattern scan
	var parts []string
	for _, v := range args {
		if s, ok := v.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func pathArg(tool string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch toolBaseName(tool) {
	case "read_file", "write_file", "edit_file", "glob", "grep":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	if p, ok := args["path"].(string); ok {
		return p
	}
	return ""
}

func sig(tool string, args map[string]any) string {
	return tool + "|" + extract(tool, args)
}
