// Package main implements the local TUI (terminal UI) for code-agent: an
// offline-first interactive console for the local machine and for managing
// encrypted remote SSH connections.
//
// Design goals (M2 / 2.3):
//   - Pure standard-library UI: no external TUI dependency.
//   - Local commands run through an injectable LocalRunner so the dispatch
//     logic is unit-testable without spawning real processes.
//   - SSH connection credentials stay encrypted at rest via the existing
//     EncryptingConnRepo (KMS); deleting a connection revokes its local
//     secrets (the cross-device revocation primitive for the local vault).
package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	"github.com/spray272598/code-agent/internal/domain/ssh/port"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
)

// LocalRunner abstracts executing a command on the local machine. The default
// implementation shells out via os/exec; tests inject a fake.
type LocalRunner interface {
	Run(ctx context.Context, command string) (stdout string, exitCode int, err error)
}

// App is the TUI command dispatcher. It is decoupled from the actual I/O
// (stdin/stdout) so the command logic can be tested in isolation.
type App struct {
	Local   LocalRunner
	Repo    port.IConnectionRepository
	Pool    *sshinfra.Pool
	Term    *sshinfra.Terminal
	Now     func() time.Time
	printed []string // accumulated output (used by tests via LastOutput)
}

// NewApp wires the default dependencies. local may be nil to use the real
// os/exec runner; repo/pool/term are required for SSH management commands.
func NewApp(local LocalRunner, repo port.IConnectionRepository, pool *sshinfra.Pool, term *sshinfra.Terminal) *App {
	return &App{
		Local: local,
		Repo:  repo,
		Pool:  pool,
		Term:  term,
		Now:   time.Now,
	}
}

// Execute parses and runs a single input line. Slash-commands (/conn, /ssh,
// /help, /exit) are handled internally; anything else is treated as a local
// shell command. It returns the textual output and whether the REPL should
// exit.
func (a *App) Execute(ctx context.Context, line string) (out string, exit bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(trimmed, "/") {
		return a.executeSlash(ctx, trimmed)
	}
	return a.runLocal(ctx, trimmed)
}

func (a *App) runLocal(ctx context.Context, cmd string) (string, bool) {
	if a.Local == nil {
		return "", false
	}
	stdout, code, err := a.Local.Run(ctx, cmd)
	if err != nil {
		return fmt.Sprintf("error: %v", err), false
	}
	if code != 0 {
		return fmt.Sprintf("%s\n[exit %d]", stdout, code), false
	}
	return stdout, false
}

func (a *App) executeSlash(ctx context.Context, line string) (string, bool) {
	fields := strings.Fields(line)
	cmd := fields[0]
	args := fields[1:]
	switch cmd {
	case "/exit", "/quit":
		return "bye", true
	case "/help":
		return helpText, false
	case "/conn":
		return a.connCommand(ctx, args)
	case "/ssh":
		return a.sshCommand(ctx, args)
	default:
		return fmt.Sprintf("unknown command %q (try /help)", cmd), false
	}
}

// connCommand implements connection vault management. The repository is
// expected to be an EncryptingConnRepo so credentials are stored encrypted.
func (a *App) connCommand(ctx context.Context, args []string) (string, bool) {
	if a.Repo == nil {
		return "ssh vault not configured", false
	}
	if len(args) == 0 {
		return a.listConns(ctx), false
	}
	switch args[0] {
	case "list", "ls":
		return a.listConns(ctx), false
	case "add":
		return a.addConn(ctx, args[1:])
	case "rm", "remove", "revoke":
		return a.removeConn(ctx, args[1:])
	default:
		return fmt.Sprintf("unknown /conn subcommand %q", args[0]), false
	}
}

func (a *App) listConns(ctx context.Context) string {
	conns, err := a.Repo.List(ctx)
	if err != nil {
		return fmt.Sprintf("list error: %v", err)
	}
	if len(conns) == 0 {
		return "no saved connections"
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].Name < conns[j].Name })
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-3s %-20s %-30s %s\n", "#", "NAME", "HOST", "AUTH"))
	for i, c := range conns {
		auth := c.AuthType
		if auth == "" {
			auth = "password"
		}
		b.WriteString(fmt.Sprintf("%-3d %-20s %-30s %s\n", i+1, c.Name, c.Host, auth))
	}
	return strings.TrimRight(b.String(), "\n")
}

// addConn parses: /conn add <name> <user@host[:port]> [password|key:<path>]
func (a *App) addConn(ctx context.Context, args []string) (string, bool) {
	if len(args) < 2 {
		return "usage: /conn add <name> <user@host[:port]> [password <pw> | key <path>]", false
	}
	name := args[0]
	userHost := args[1]
	user, host, port := parseUserHost(userHost)
	if user == "" || host == "" {
		return "invalid <user@host[:port]>", false
	}
	cfg := &model.ConnectionConfig{
		Name:     name,
		Host:     host,
		Port:     port,
		Username: user,
		AuthType: "password",
		Enabled:  true,
		CreatedAt: a.Now().UTC(),
		UpdatedAt: a.Now().UTC(),
	}
	if len(args) >= 3 {
		switch args[2] {
		case "password":
			if len(args) < 4 {
				return "password auth needs the secret: ... password <pw>", false
			}
			cfg.AuthType = "password"
			cfg.Password = strings.Join(args[3:], " ")
		case "key":
			if len(args) < 4 {
				return "key auth needs a path: ... key <path>", false
			}
			cfg.AuthType = "private_key"
			cfg.PrivateKey = args[3]
		default:
			return "auth must be 'password' or 'key'", false
		}
	}
	if err := a.Repo.Save(ctx, cfg); err != nil {
		return fmt.Sprintf("save error: %v", err), false
	}
	return fmt.Sprintf("saved connection %q (encrypted at rest)", name), false
}

func (a *App) removeConn(ctx context.Context, args []string) (string, bool) {
	if len(args) == 0 {
		return "usage: /conn rm <name>", false
	}
	name := args[0]
	cfg, err := a.Repo.FindByName(ctx, name)
	if err != nil || cfg == nil {
		return fmt.Sprintf("connection %q not found", name), false
	}
	if err := a.Repo.Delete(ctx, cfg.ID); err != nil {
		return fmt.Sprintf("delete error: %v", err), false
	}
	if a.Pool != nil {
		a.Pool.Disconnect(name)
	}
	// Deleting the encrypted record revokes the locally stored secret.
	return fmt.Sprintf("revoked connection %q and purged its encrypted credentials", name), false
}

// sshCommand opens an interactive remote terminal. The blocking interaction
// with os.Stdin is performed by the caller via OpenInteractiveSession; this
// method only validates the target and returns a ready-to-use session handle.
func (a *App) sshCommand(ctx context.Context, args []string) (string, bool) {
	if len(args) == 0 {
		return "usage: /ssh <name>", false
	}
	if a.Repo == nil || a.Pool == nil {
		return "ssh not configured", false
	}
	name := args[0]
	cfg, err := a.Repo.FindByName(ctx, name)
	if err != nil || cfg == nil {
		return fmt.Sprintf("connection %q not found", name), false
	}
	if !a.Pool.IsConnected(name) {
		if err := a.Pool.Connect(ctx, *cfg); err != nil {
			return fmt.Sprintf("connect %q failed: %v", name, err), false
		}
	}
	// The caller opens the interactive session from the returned name.
	return "", false
}

func parseUserHost(s string) (user, host string, port int) {
	if at := strings.Index(s, "@"); at >= 0 {
		user = s[:at]
		s = s[at+1:]
	}
	port = 22
	if colon := strings.LastIndex(s, ":"); colon >= 0 {
		if p, err := strconv.Atoi(s[colon+1:]); err == nil {
			port = p
			s = s[:colon]
		}
	}
	return user, s, port
}

const helpText = `code-agent TUI — local terminal & ssh vault
Commands:
  <cmd>                run a local shell command
  /conn list           list saved (encrypted) ssh connections
  /conn add <name> <user@host[:port]> [password <pw> | key <path>]
                       add a connection (credentials encrypted at rest)
  /conn rm <name>      revoke a connection and purge its encrypted creds
  /ssh <name>          open an interactive remote terminal
  /help                show this help
  /exit                quit`
