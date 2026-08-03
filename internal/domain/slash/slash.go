package slash

import (
	"fmt"
	"strings"
	"sync"
)

// Result of a slash command (local, no LLM).
type Result struct {
	Handled  bool
	Response string
	// Optional side effects
	ForceCompact bool
	// Message rewritten for agent (e.g. after stripping /skill)
	Rewrite string
}

type Handler func(args string, ctx Context) Result

type Context struct {
	SessionID string
	UserID    string
	// Injected helpers
	ListTools  func() []map[string]string
	ListSkills func() string
	ListMCP    func() string
	HelpExtra  string
}

type Registry struct {
	mu   sync.RWMutex
	cmds map[string]Handler
	help map[string]string
}

func NewRegistry() *Registry {
	r := &Registry{cmds: map[string]Handler{}, help: map[string]string{}}
	r.Register("help", "Show slash commands", func(args string, ctx Context) Result {
		var b strings.Builder
		b.WriteString("Slash commands:\n")
		r.mu.RLock()
		for name, h := range r.help {
			b.WriteString(fmt.Sprintf("  /%s — %s\n", name, h))
		}
		r.mu.RUnlock()
		if ctx.HelpExtra != "" {
			b.WriteString(ctx.HelpExtra)
		}
		return Result{Handled: true, Response: b.String()}
	})
	r.Register("clear", "Hint to start fresh (client may clear UI)", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "Session context kept on server; client may clear display. Use /compact to shrink history."}
	})
	r.Register("compact", "Compress conversation history", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "Context compression requested for next agent turn.", ForceCompact: true}
	})
	r.Register("tools", "List registered tools", func(args string, ctx Context) Result {
		if ctx.ListTools == nil {
			return Result{Handled: true, Response: "(tools unavailable)"}
		}
		list := ctx.ListTools()
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%d tools:\n", len(list)))
		for _, t := range list {
			b.WriteString(fmt.Sprintf("- %s: %s\n", t["name"], t["description"]))
		}
		return Result{Handled: true, Response: b.String()}
	})
	r.Register("skills", "List skills", func(args string, ctx Context) Result {
		if ctx.ListSkills == nil {
			return Result{Handled: true, Response: "(skills unavailable)"}
		}
		return Result{Handled: true, Response: ctx.ListSkills()}
	})
	r.Register("mcp", "List MCP servers / health", func(args string, ctx Context) Result {
		if ctx.ListMCP == nil {
			return Result{Handled: true, Response: "(mcp unavailable)"}
		}
		return Result{Handled: true, Response: ctx.ListMCP()}
	})
	r.Register("cost", "Show rough session token usage / metrics hint", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "See GET /api/v1/metrics and GET /api/v1/memory. Redis: token:user:{id}:{day}."}
	})
	r.Register("memory", "Hint for memory tools / API", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "Use tools memory_save / memory_search, or GET/POST /api/v1/memory. Scopes: user | project."}
	})
	r.Register("teams", "SubAgent roles / teams (help)", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "Roles: explore / verify / general / docs. Parallel run: /team <goal>. Deep sequential: /deep <goal>. Config: teams/default.yaml"}
	})
	// note: /team and /parallel are pass-through to Eino multi-agent (not local handlers)
	r.Register("index", "Hint for code_search / code_index tools", func(args string, ctx Context) Result {
		return Result{Handled: true, Response: "Tools: code_search (query), code_index (rebuild). API: GET /api/v1/index/search?q=  POST /api/v1/index/rebuild"}
	})
	return r
}

func (r *Registry) Register(name, help string, h Handler) {
	r.mu.Lock()
	r.cmds[strings.ToLower(name)] = h
	r.help[strings.ToLower(name)] = help
	r.mu.Unlock()
}

// Try parses leading /command and runs handler.
func (r *Registry) Try(input string, ctx Context) Result {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return Result{}
	}
	// /skill foo -> not a builtin, pass through as skill trigger via rewrite
	body := strings.TrimSpace(input[1:])
	if body == "" {
		return r.cmds["help"]("", ctx)
	}
	parts := strings.SplitN(body, " ", 2)
	name := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	// special: /skill <id> rest
	if name == "skill" {
		if args == "" {
			return Result{Handled: true, Response: "usage: /skill <id> [message]"}
		}
		ap := strings.SplitN(args, " ", 2)
		sid := ap[0]
		msg := ""
		if len(ap) > 1 {
			msg = ap[1]
		}
		if msg == "" {
			msg = "Execute skill " + sid
		}
		return Result{Handled: false, Rewrite: "使用 skill " + sid + " " + msg}
	}
	r.mu.RLock()
	h := r.cmds[name]
	r.mu.RUnlock()
	if h == nil {
		// pass-through agent routing prefixes (not local slash handlers)
		switch name {
		case "team", "parallel", "deep":
			return Result{Handled: false, Rewrite: input}
		}
		return Result{Handled: true, Response: fmt.Sprintf("unknown command /%s — try /help", name)}
	}
	return h(args, ctx)
}
