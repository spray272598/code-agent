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

// Guard implements 5-layer defense with command normalization, extended with
// Glob-based deny rules, secret sanitization, audit logging, and network policy.
type Guard struct {
	mu           sync.RWMutex
	sessionAllow map[string]map[string]bool
	pending      map[string]*PendingConfirm
	awaiting     map[string]*AwaitingResume
	denyStreak   map[string]int
	circuitLimit int
	workspace    string
	sessionWS    map[string]string
	pathSandbox  bool
	confirmWrite bool
	denyRules    []rule
	confirmRules []rule
	readTools    map[string]bool
	writeTools   map[string]bool
	mcpConfirm   bool
	mode         SandboxMode
	// Extended security components
	denyEngine   *DenyEngine
	sanitizer    *Sanitizer
	audit        *AuditLogger
	netEnforcer  *NetworkEnforcer
	sandboxMgr   *SandboxManager
	configLoader *ConfigLoader
	// Advanced security: prompt injection detection
	injectionDetector *PromptInjectionDetector
	// Advanced security: behavior analysis & anomaly detection
	behaviorTracker *BehaviorTracker
	// Advanced security: tamper-evident integrity chain
	integrityChain *IntegrityChain
	// Advanced security: adaptive circuit breaker
	adaptiveBreaker *AdaptiveCircuitBreaker
	// External sandbox enforcer (injected by bootstrap for dependency inversion)
	externalSandboxEnforcer SandboxEnforcer
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
	// ModeDevbox allows network and full read/write within workspace.
	ModeDevbox
)

func (m SandboxMode) String() string {
	switch m {
	case ModeReadonly:
		return "readonly"
	case ModeStrict:
		return "strict"
	case ModeDevbox:
		return "devbox"
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
	case "devbox":
		return ModeDevbox
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

// NewGuardMode builds a Guard with an explicit sandbox tier and all extended
// security components (deny engine, sanitizer, audit, network policy, sandbox mgr).
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
	g.initExtendedSecurity(workspace)
	return g
}

func (g *Guard) initExtendedSecurity(workspace string) {
	g.sanitizer = DefaultSanitizer()
	g.audit = DefaultAuditLogger()
	g.netEnforcer = NewNetworkEnforcer(DefaultNetworkPolicy())
	g.configLoader = NewConfigLoader(workspace)
	if denyCfg, err := g.configLoader.Load(); err == nil {
		if engine, err := NewDenyEngine(denyCfg.Deny); err == nil {
			g.denyEngine = engine
		}
	} else if engine, err := DefaultDenyEngine(); err == nil {
		g.denyEngine = engine
	}

	// Use external sandbox enforcer if provided (dependency injection from bootstrap)
	// Otherwise create the default SandboxManager
	if g.externalSandboxEnforcer != nil {
		// Create a minimal SandboxManager that wraps the external enforcer
		g.sandboxMgr = NewSandboxManagerWithEnforcer(workspace, g.configLoader, g.audit, g.externalSandboxEnforcer)
	} else {
		g.sandboxMgr = NewSandboxManager(workspace, g.configLoader, g.audit)
	}

	// Advanced security components
	g.injectionDetector = NewPromptInjectionDetector()
	g.behaviorTracker = NewBehaviorTracker()
	g.behaviorTracker.SetDeletionBurstThreshold(10*time.Minute, 3)
	g.behaviorTracker.SetRapidAccessThreshold(5*time.Minute, 3)
	g.integrityChain = NewIntegrityChain()
	g.adaptiveBreaker = NewAdaptiveCircuitBreaker()

	// Wire risk data sources for adaptive circuit breaker
	g.adaptiveBreaker.SetRiskSources(
		g.Mode,
		g.behaviorTracker.GetSessionRisk,
		g.injectionDetector.GetTotalDetectionsForAdaptive,
	)
}

// SetExternalSandboxEnforcer sets an external sandbox enforcer (for dependency injection)
// This should be called before initExtendedSecurity, typically by bootstrap
func (g *Guard) SetExternalSandboxEnforcer(enforcer SandboxEnforcer) {
	g.externalSandboxEnforcer = enforcer
}

func (g *Guard) DenyEngine() *DenyEngine                     { return g.denyEngine }
func (g *Guard) Sanitizer() *Sanitizer                       { return g.sanitizer }
func (g *Guard) Audit() *AuditLogger                         { return g.audit }
func (g *Guard) NetworkEnforcer() *NetworkEnforcer           { return g.netEnforcer }
func (g *Guard) SandboxManager() *SandboxManager             { return g.sandboxMgr }
func (g *Guard) ConfigLoader() *ConfigLoader                 { return g.configLoader }
func (g *Guard) InjectionDetector() *PromptInjectionDetector { return g.injectionDetector }
func (g *Guard) BehaviorTracker() *BehaviorTracker           { return g.behaviorTracker }
func (g *Guard) IntegrityChain() *IntegrityChain             { return g.integrityChain }
func (g *Guard) AdaptiveBreaker() *AdaptiveCircuitBreaker    { return g.adaptiveBreaker }

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
		{"git_protocol_leak", "git protocol data exfiltration", []string{
			`(?i)\bgit\s+clone\s+.*--bare`,
			`(?i)\bgit\s+fetch\s+.*refs`,
			`(?i)\bgit\s+archive\b`,
			`(?i)\bgit\s+bundle\b`,
		}},
		{"git_object_exfil", "direct git object access", []string{
			`(?i)\bgit\s+cat-file\b`,
			`(?i)\bgit\s+rev-list\b`,
			`(?i)\.git/objects/`,
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
			regexp.MustCompile(`(?i)\brclone\b`), regexp.MustCompile(`(?i)\brsync\b.*://`),
		}},
		{id: "ssh", reason: "remote shell", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bssh\s`), regexp.MustCompile(`(?i)\bscp\b`),
			regexp.MustCompile(`(?i)\bsftp\b`), regexp.MustCompile(`(?i)\bsshpass\b`),
		}},
		{id: "nc", reason: "netcat", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bnc\s`), regexp.MustCompile(`(?i)\bncat\b`),
			regexp.MustCompile(`(?i)\bsocat\b`),
		}},
		{id: "dns", reason: "dns exfiltration", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bnslookup\b`), regexp.MustCompile(`(?i)\bdig\s`),
			regexp.MustCompile(`(?i)\bhost\s`),
		}},
		{id: "script_net", reason: "script network access", patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(import\s+socket|import\s+urllib|import\s+http)`),
			regexp.MustCompile(`(?i)(require\s*\(\s*["']http|net\.Socket|net\.connect)`),
			regexp.MustCompile(`(?i)dev/tcp/`),
		}},
	}
}

func (g *Guard) Check(sessionID, tool string, args map[string]any) Decision {
	summary := tool + ": " + fmt.Sprint(args)
	tool = strings.TrimSpace(tool)

	// L0 Prompt Injection Detection: analyze command content for injection patterns
	if g.injectionDetector != nil {
		cmd := extract(tool, args)
		if cmd != "" {
			report := g.injectionDetector.CheckWithSession(sessionID, cmd)
			if report.Detected && report.ShouldBlock(InjectionHigh) {
				g.auditDeny(CategoryDenied, "prompt_injection", tool,
					fmt.Sprintf("injection detected: %d matches, score=%.2f", len(report.Matches), report.Score),
					sessionID)
				if g.integrityChain != nil {
					g.integrityChain.Append("prompt_injection_blocked", sessionID, "",
						fmt.Sprintf("tool=%s matches=%d", tool, len(report.Matches)),
						map[string]any{"score": report.Score, "matches": len(report.Matches)})
				}
				return Decision{
					Action:  ActionDeny,
					Layer:   "L0-injection",
					RuleID:  "prompt_injection",
					Reason:  "prompt injection detected in tool arguments",
					Tool:    tool,
					Summary: summary,
				}
			}
		}
	}

	// L0 Behavior Tracking: record event and detect anomalies
	if g.behaviorTracker != nil {
		target := ""
		if p, ok := args["path"].(string); ok {
			target = p
		} else if c, ok := args["command"].(string); ok {
			target = truncateStr(c, 100)
		} else {
			target = extract(tool, args)
		}
		anomalies := g.behaviorTracker.Track(BehaviorEvent{
			Time:      time.Now(),
			SessionID: sessionID,
			Tool:      tool,
			Target:    target,
			Category:  toolCategory(tool),
		})
		if len(anomalies) > 0 {
			severeAnomalies := 0
			for _, a := range anomalies {
				if a.Severity >= BehaviorHigh {
					severeAnomalies++
				}
			}
			if severeAnomalies > 0 {
				g.auditWarn(CategoryDenied, tool,
					fmt.Sprintf("%d behavioral anomalies detected (severity>=high)", severeAnomalies),
					sessionID)
			}
		}
	}

	// L5 circuit (adaptive threshold)
	g.mu.RLock()
	streak := g.denyStreak[sessionID]
	threshold := g.circuitLimit
	if g.adaptiveBreaker != nil {
		adaptiveThreshold := g.adaptiveBreaker.GetThreshold(sessionID)
		if adaptiveThreshold < threshold {
			threshold = adaptiveThreshold
		}
	}
	if g.sessionAllow[sessionID] != nil {
		if g.sessionAllow[sessionID]["*"] {
			g.mu.RUnlock()
			g.auditAllow(CategoryTool, tool, "session approved", sessionID)
			return Decision{Action: ActionAllow, Layer: "L4", Tool: tool, Summary: summary, Reason: "session approve all"}
		}
		if g.sessionAllow[sessionID][sig(tool, args)] {
			g.mu.RUnlock()
			g.auditAllow(CategoryTool, tool, "session approved specific", sessionID)
			return Decision{Action: ActionAllow, Layer: "L4", Tool: tool, Summary: summary}
		}
	}
	g.mu.RUnlock()
	if streak >= threshold {
		g.auditDeny(CategoryTool, "circuit_breaker", tool,
			fmt.Sprintf("too many denials (streak=%d threshold=%d)", streak, threshold),
			sessionID)
		if g.integrityChain != nil {
			g.integrityChain.Append("circuit_breaker", sessionID, "",
				fmt.Sprintf("tool=%s streak=%d threshold=%d", tool, streak, threshold),
				map[string]any{"streak": streak, "threshold": threshold})
		}
		return Decision{Action: ActionDeny, Layer: "L5", RuleID: "circuit", Reason: "too many denials", Tool: tool, Summary: summary}
	}

	// L1 deny — all command variants
	cmd := extract(tool, args)
	variants := CommandVariants(cmd)
	if r := matchAny(g.denyRules, variants); r != nil {
		g.incDeny(sessionID)
		g.auditDeny(CategoryTool, r.id, tool, r.reason, sessionID)
		return Decision{Action: ActionDeny, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
	}

	// Sandbox tier
	if g.mode == ModeReadonly && g.isMutating(tool) {
		g.auditDeny(CategorySandbox, "readonly", tool, "read-only sandbox: mutating tool denied", sessionID)
		return Decision{Action: ActionDeny, Layer: "L1", RuleID: "readonly", Reason: "read-only sandbox: mutating tool denied", Tool: tool, Summary: summary}
	}
	if g.mode == ModeStrict {
		base := toolBaseName(tool)
		if tool == "bash" || base == "bash" || tool == "run_command" {
			if r := matchAny(g.networkEgressRules(), variants); r != nil {
				g.auditDeny(CategoryNetwork, "strict_egress", tool, "strict sandbox: network egress blocked", sessionID)
				return Decision{Action: ActionDeny, Layer: "L1", RuleID: "strict_egress", Reason: "strict sandbox: network egress blocked", Tool: tool, Summary: summary}
			}
		}
	}

	// L2 path sandbox + Glob deny engine
	if g.pathSandbox && tool != "switch_workspace" {
		if p := pathArg(tool, args); p != "" {
			norm := NormalizePathArg(p)
			if !g.underWorkspace(sessionID, norm) {
				g.incDeny(sessionID)
				g.auditDeny(CategorySandbox, "path_sandbox", norm, "path outside workspace", sessionID)
				return Decision{Action: ActionDeny, Layer: "L2", RuleID: "path_sandbox", Reason: "path outside workspace", Tool: tool, Summary: summary}
			}
			if g.denyEngine != nil && g.denyEngine.IsDenied(norm) {
				g.incDeny(sessionID)
				g.auditDeny(CategorySandbox, "glob_deny", norm, "glob pattern denied path", sessionID)
				return Decision{Action: ActionDeny, Layer: "L2", RuleID: "glob_deny", Reason: "path matches deny glob pattern", Tool: tool, Summary: summary}
			}
			if sensitivePath(norm) || sensitivePath(p) {
				g.auditConfirm(CategorySandbox, "sensitive_path", norm, "sensitive path access requires confirm", sessionID)
				return Decision{Action: ActionConfirm, Layer: "L2", RuleID: "sensitive_path", Reason: "sensitive path", Tool: tool, Summary: summary}
			}
		}
	}

	// L2.5 network policy enforcement
	if g.netEnforcer != nil {
		if cmd != "" {
			lower := strings.ToLower(cmd)
			for _, netPattern := range []string{"http://", "https://", "ssh://", "ftp://"} {
				if strings.Contains(lower, netPattern) {
					decision := g.netEnforcer.FilterURL(extractURL(cmd))
					if decision.Action == ActionDeny {
						g.incDeny(sessionID)
						g.auditDeny(CategoryNetwork, decision.RuleID, cmd, decision.Reason, sessionID)
						return Decision{Action: ActionDeny, Layer: "L2", RuleID: decision.RuleID, Reason: decision.Reason, Tool: tool, Summary: summary}
					}
					break
				}
			}
		}
	}

	// L3 tool class
	base := toolBaseName(tool)
	if g.readTools[tool] || g.readTools[base] {
		sanitizedSummary := g.sanitizeSummary(summary)
		g.auditAllow(CategoryTool, tool, "read tool allowed", sessionID)
		return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: sanitizedSummary}
	}
	if g.writeTools[tool] || g.writeTools[base] {
		if g.confirmWrite {
			g.auditConfirm(CategoryTool, "write", tool, "write/edit requires confirm", sessionID)
			return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "write", Reason: "write/edit requires confirm", Tool: tool, Summary: summary}
		}
	}
	if tool == "bash" || base == "bash" || tool == "run_command" {
		if r := matchAny(g.confirmRules, variants); r != nil {
			g.auditConfirm(CategoryTool, r.id, tool, r.reason, sessionID)
			return Decision{Action: ActionConfirm, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
		}
		g.auditConfirm(CategoryTool, "bash", tool, "shell requires confirm", sessionID)
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "bash", Reason: "shell requires confirm", Tool: tool, Summary: summary}
	}
	if strings.HasPrefix(tool, "ssh_") || strings.HasPrefix(base, "ssh_") {
		if r := matchAny(g.denyRules, variants); r != nil {
			g.incDeny(sessionID)
			g.auditDeny(CategoryTool, r.id, tool, r.reason, sessionID)
			return Decision{Action: ActionDeny, Layer: r.layer, RuleID: r.id, Reason: r.reason, Tool: tool, Summary: summary}
		}
		g.auditConfirm(CategoryTool, "ssh", tool, "SSH remote operation requires confirm", sessionID)
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "ssh", Reason: "SSH remote operation requires confirm", Tool: tool, Summary: summary}
	}
	if tool == "delegate" {
		g.auditConfirm(CategoryTool, "delegate", tool, "subagent delegation requires confirm", sessionID)
		return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "delegate", Reason: "subagent delegation requires confirm", Tool: tool, Summary: summary}
	}

	if g.mcpConfirm || isMCPTool(tool) {
		if r := matchAny(g.denyRules, variants); r != nil {
			g.incDeny(sessionID)
			g.auditDeny(CategoryTool, "mcp_"+r.id, tool, "mcp args matched deny: "+r.reason, sessionID)
			return Decision{Action: ActionDeny, Layer: "L1", RuleID: "mcp_" + r.id, Reason: "mcp args matched deny: " + r.reason, Tool: tool, Summary: summary}
		}
		if looksReadOnlyMCP(tool) && !looksDangerousArgs(args) {
			g.auditAllow(CategoryTool, tool, "mcp read-like allowed", sessionID)
			return Decision{Action: ActionAllow, Layer: "L3", Tool: tool, Summary: summary, Reason: "mcp read-like"}
		}
		g.auditConfirm(CategoryTool, "mcp_or_unknown", tool, "MCP/unknown tool requires confirm", sessionID)
		return Decision{
			Action: ActionConfirm, Layer: "L3", RuleID: "mcp_or_unknown",
			Reason: "MCP/unknown tool requires confirm", Tool: tool, Summary: summary,
		}
	}
	g.auditConfirm(CategoryTool, "unknown_tool", tool, "unknown tool requires confirm", sessionID)
	return Decision{Action: ActionConfirm, Layer: "L3", RuleID: "unknown_tool", Reason: "unknown tool requires confirm", Tool: tool, Summary: summary}
}

func (g *Guard) auditAllow(category AuditCategory, target, detail string, sessionID ...string) {
	if g.audit != nil {
		g.audit.Allow(category, target, detail, sessionID...)
	}
}

func (g *Guard) auditDeny(category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	if g.audit != nil {
		g.audit.Deny(category, ruleID, target, detail, sessionID...)
	}
}

func (g *Guard) auditConfirm(category AuditCategory, ruleID, target, detail string, sessionID ...string) {
	if g.audit != nil {
		g.audit.Confirm(category, ruleID, target, detail, sessionID...)
	}
}

func (g *Guard) sanitizeSummary(s string) string {
	if g.sanitizer != nil && g.sanitizer.HasAnyMatch(s) {
		return g.sanitizer.RedactAll(s)
	}
	return s
}

func extractURL(cmd string) string {
	for _, prefix := range []string{"http://", "https://", "ssh://", "ftp://"} {
		if i := strings.Index(cmd, prefix); i >= 0 {
			end := len(cmd)
			for _, delim := range []string{" ", "\t", "\n", "\"", "'", ";", "|", "&", ")", "(", "[", "]"} {
				if j := strings.Index(cmd[i:], delim); j >= 0 && i+j < end {
					end = i + j
				}
			}
			return cmd[i:end]
		}
	}
	return ""
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
	// resolve symlinks in workspace root once
	if resolved, err := filepath.EvalSymlinks(absW); err == nil {
		absW = resolved
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
		// resolve symlinks to prevent symlink-based escapes
		if resolved, err := filepath.EvalSymlinks(absP); err == nil {
			absP = resolved
		}
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

func (g *Guard) sensitivePathEnhanced(p string) bool {
	if g.denyEngine != nil {
		return g.denyEngine.IsDeniedLegacy(p)
	}
	return sensitivePath(p)
}

func sensitivePath(p string) bool {
	lp := strings.ToLower(filepath.ToSlash(p))
	if strings.Contains(lp, ".ssh") || strings.Contains(lp, ".env") ||
		strings.Contains(lp, "id_rsa") || strings.Contains(lp, "credentials") ||
		strings.Contains(lp, "secret") || strings.Contains(lp, "wallet") {
		return true
	}
	if strings.Contains(lp, "/.git/") || strings.HasSuffix(lp, "/.git") {
		return true
	}
	if strings.Contains(lp, ".git/objects") || strings.Contains(lp, ".git/config") {
		return true
	}
	if strings.HasSuffix(lp, ".pem") || strings.HasSuffix(lp, ".key") ||
		strings.HasSuffix(lp, ".p12") || strings.HasSuffix(lp, ".pfx") ||
		strings.HasSuffix(lp, ".pub") {
		return true
	}
	return false
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
	count := g.denyStreak[sessionID]
	g.mu.Unlock()

	// Record in adaptive circuit breaker
	if g.adaptiveBreaker != nil {
		g.adaptiveBreaker.RecordDenial(sessionID, "", "security_denied")
	}

	// Record in integrity chain
	if g.integrityChain != nil {
		g.integrityChain.Append("deny_incremented", sessionID, "",
			fmt.Sprintf("deny_count=%d", count),
			map[string]any{"count": count})
	}
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func toolCategory(tool string) string {
	base := toolBaseName(tool)
	switch {
	case strings.Contains(base, "read") || strings.Contains(base, "search") || strings.Contains(base, "glob") || strings.Contains(base, "grep"):
		return "read"
	case strings.Contains(base, "write") || strings.Contains(base, "edit") || strings.Contains(base, "delete") || strings.Contains(base, "remove"):
		return "write"
	case strings.Contains(base, "bash") || strings.Contains(base, "command") || strings.Contains(base, "run"):
		return "execute"
	case strings.Contains(base, "curl") || strings.Contains(base, "ssh") || strings.Contains(base, "http"):
		return "network"
	case strings.Contains(base, "mcp") || strings.Contains(base, "server") || strings.Contains(base, "__"):
		return "mcp"
	default:
		return "other"
	}
}

func (g *Guard) auditWarn(category AuditCategory, target, detail string, sessionID ...string) {
	if g.audit != nil {
		g.audit.Warn(category, target, detail)
	}
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
