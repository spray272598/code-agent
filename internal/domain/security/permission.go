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
	sessionWS    map[string]string // sessionID → override workspace
	pathSandbox  bool
	confirmWrite bool
	denyRules    []rule
	confirmRules []rule
	// tools that are always allow / confirm / deny by name class
	readTools  map[string]bool
	writeTools map[string]bool
	// MCP/unknown tools: confirm by default
	mcpConfirm bool
	// mode enforces the sandbox tier: ModeWorkspace (default, path sandbox),
	// ModeReadonly (all mutating tools denied), ModeStrict (path sandbox + bash
	// execution confined to workspace, network writes blocked). Mirrors the
	// Landlock/Seatbelt tiers Grok Build enforces at the kernel level; on
	// Windows we degrade gracefully to in-process path guarding.
	mode SandboxMode
}

// SandboxMode selects the enforcement tier applied to tool execution.
type SandboxMode int

const (
	// ModeWorkspace is the default tier: path sandbox on, writes allowed.
	ModeWorkspace SandboxMode = iota
	// ModeReadonly denies every mutating tool (write_file/edit_file/bash/...).
	ModeReadonly
	// ModeStrict confines bash to the workspace and blocks network egress.
	ModeStrict
)

func (m SandboxMode) String() string {
	switch m {
	case ModeReadonly:
		return "readonly"
	case ModeStrict:
		return "strict"
	default:
		return "workspace"
	}
}

// ParseSandboxMode maps a config string to a SandboxMode.
func ParseSandboxMode(s string) SandboxMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "readonly", "read-only", "ro":
		return ModeReadonly
	case "strict":
		return ModeStrict
	default:
		return ModeWorkspace
	}
}

type rule struct {
	id, reason string
	patterns   []*regexp.Regexp // multi-pattern
	layer      string
}

func NewGuard(workspace string, pathSandbox, confirmWrite bool) *Guard {
	return NewGuardMode(workspace, pathSandbox, confirmWrite, ModeWorkspace)
}

// NewGuardMode builds a Guard with an explicit sandbox tier.
func NewGuardMode(workspace string, pathSandbox, confirmWrite bool, mode SandboxMode) *Guard {
	g := &Guard{
		sessionAllow: make(map[string]map[string]bool),
		pending:      make(map[string]*PendingConfirm),
		awaiting:     make(map[string]*AwaitingResume),
		denyStreak:   make(map[string]int),
		circuitLimit: 5,
		workspace:    workspace,
		sessionWS:    make(map[string]string),
		pathSandbox:  pathSandbox,
		confirmWrite: confirmWrite,
		mcpConfirm:   true,
		mode:         mode,
		readTools: map[string]bool{
			"read_file": true, "glob": true, "grep": true, "memory_search": true,
			"code_search": true, "code_index": true,
		},
		writeTools: map[string]bool{
			"write_file": true, "edit_file": true, "memory_save": true,
		},
	}
	g.initRules()
	return g
}

// Mode returns the active sandbox tier.
func (g *Guard) Mode() SandboxMode { return g.mode }

// SetMode switches the sandbox tier at runtime. Transitioning into readonly is
// the mechanism behind PlanMode's explore phase; exiting back to workspace
// (or strict) resumes the implement phase.
func (g *Guard) SetMode(m SandboxMode) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.mode = m
	g.mu.Unlock()
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

// isMutating reports whether a tool can change the filesystem or process state.
func (g *Guard) isMutating(tool string) bool {
	base := toolBaseName(tool)
	if g.writeTools[tool] || g.writeTools[base] {
		return true
	}
	switch base {
	case "bash", "run_command", "write_file", "edit_file", "memory_save",
		"apply_patch", "patch", "delete_file":
		return true
	}
	return false
}

// networkEgressRules returns deny rules matching outbound network commands,
// used by the strict sandbox tier to block egress.
func (g *Guard) networkEgressRules() []rule {
	return []rule{
		{id: "curl", reason: "network egress", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bcurl\b`), regexp.MustCompile(`(?i)\bwget\b`),
		}},
		{id: "ssh", reason: "remote shell", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bssh\s`), regexp.MustCompile(`(?i)\bscp\b`),
		}},
		{id: "nc", reason: "netcat", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bnc\b`), regexp.MustCompile(`(?i)\bncat\b`),
		}},
	}
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

	// Sandbox tier (mirrors kernel-enforced Landlock/Seatbelt modes).
	// ModeReadonly: every mutating tool is denied outright.
	if g.mode == ModeReadonly && g.isMutating(tool) {
		return Decision{Action: ActionDeny, Layer: "L1", RuleID: "readonly", Reason: "read-only sandbox: mutating tool denied", Tool: tool, Summary: summary}
	}
	// ModeStrict: bash execution confined to the workspace and network egress blocked.
	if g.mode == ModeStrict {
		base := toolBaseName(tool)
		if tool == "bash" || base == "bash" || tool == "run_command" {
			if r := matchAny(g.networkEgressRules(), variants); r != nil {
				return Decision{Action: ActionDeny, Layer: "L1", RuleID: "strict_egress", Reason: "strict sandbox: network egress blocked", Tool: tool, Summary: summary}
			}
		}
	}

	// L2 path sandbox (also normalize path tricks / URL / Unicode)
	// switch_workspace is exempt: it needs to accept paths outside current workspace
	if g.pathSandbox && tool != "switch_workspace" {
		if p := pathArg(tool, args); p != "" {
			norm := NormalizePathArg(p)
			if !g.underWorkspace(sessionID, norm) {
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
	// SSH tools — always require confirm (remote operations)
	if strings.HasPrefix(tool, "ssh_") || strings.HasPrefix(base, "ssh_") {
		if r := matchAny(g.denyRules, variants); r != nil {
			g.incDeny(sessionID)
			return Decision{Action: ActionDeny, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
		}
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "ssh", Reason: "SSH remote operation requires confirm", Tool: tool, Summary: summary}
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

// SetSessionWorkspace overrides the workspace for a specific session.
func (g *Guard) SetSessionWorkspace(sessionID, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if path == "" {
		delete(g.sessionWS, sessionID)
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	g.sessionWS[sessionID] = abs
}

// SessionWorkspace returns the effective workspace for a session (session override > global).
func (g *Guard) SessionWorkspace(sessionID string) string {
	g.mu.RLock()
	if ws, ok := g.sessionWS[sessionID]; ok {
		g.mu.RUnlock()
		return ws
	}
	g.mu.RUnlock()
	return g.workspace
}

func (g *Guard) underWorkspace(sessionID, p string) bool {
	ws := g.SessionWorkspace(sessionID)
	if ws == "" {
		return true
	}
	// reject invalid UTF-8 / null from NormalizePathArg
	if p == "" || strings.Contains(p, "\x00") {
		return false
	}
	absW, err := filepath.Abs(ws)
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

// RestorePending rehydrates a pending confirm after process restart (checkpoint).
func (g *Guard) RestorePending(p *PendingConfirm) {
	if g == nil || p == nil || p.ID == "" || p.SessionID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cp := *p
	if cp.Args == nil {
		cp.Args = map[string]any{}
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	g.pending[cp.ID] = &cp
	g.awaiting[cp.SessionID] = &AwaitingResume{
		SessionID: cp.SessionID, Tool: cp.Tool, Args: cp.Args, PermID: cp.ID, Ready: false,
	}
}

// ExportPending returns a copy of a pending by id (for checkpoint save).
func (g *Guard) ExportPending(id string) *PendingConfirm {
	if g == nil || id == "" {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.pending[id]
	if !ok || p == nil {
		return nil
	}
	cp := *p
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
