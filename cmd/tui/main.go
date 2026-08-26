package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
	"github.com/spray272598/code-agent/internal/infrastructure/sqlite"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
)

// dataDir returns a per-user local data directory for the TUI vault.
func dataDir() string {
	if d := os.Getenv("CODE_AGENT_DATA"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "./data"
	}
	return filepath.Join(home, ".code-agent")
}

// execRunner is the default LocalRunner: runs commands through the host shell.
type execRunner struct{ shell string }

func (r execRunner) Run(ctx context.Context, command string) (string, int, error) {
	c := exec.CommandContext(ctx, r.shell, "-c", command)
	out, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return string(out), -1, err
		}
	}
	return string(out), code, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, cleanup, err := buildApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui init error: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	runREPL(ctx, app, os.Stdin, os.Stdout)
}

// buildApp wires the real dependencies: KMS-sealed sqlite repo, SSH pool.
func buildApp() (*App, func(), error) {
	sealer, err := kmsinfra.NewSealer()
	if err != nil {
		return nil, nil, fmt.Errorf("kms: %w", err)
	}
	// Local vault: sqlite file in the user's data dir.
	dbPath := filepath.Join(dataDir(), "codeagent.db")
	db, err := sqlite.Open(dbPath, true)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: %w", err)
	}
	repo := sshinfra.NewSQLiteConnRepo(db)
	encrypted := sshinfra.NewEncryptingConnRepo(repo, sealer)

	pool := sshinfra.NewPool()
	term := sshinfra.NewTerminal(pool)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}
	app := NewApp(execRunner{shell: shell}, encrypted, pool, term)
	return app, func() { pool.CloseAll() }, nil
}

// runREPL is the read-eval-print loop. It is parameterised over the input and
// output streams so it can be exercised in tests.
func runREPL(ctx context.Context, app *App, in io.Reader, out io.Writer) {
	prompt := "code-agent$ "
	fmt.Fprintln(out, "code-agent TUI (offline terminal). Type /help.")
	r := bufio.NewReader(in)
	for {
		fmt.Fprint(out, prompt)
		line, err := r.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(out, "read error: %v\n", err)
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		// Interactive ssh is handled in the REPL (needs os.Stdin streaming).
		if strings.HasPrefix(strings.TrimSpace(line), "/ssh") {
			if handled, herr := runInteractiveSSH(ctx, app, line, out); handled {
				if herr != nil {
					fmt.Fprintf(out, "%v\n", herr)
				}
				continue
			}
		}
		res, exit := app.Execute(ctx, line)
		if res != "" {
			fmt.Fprintln(out, res)
		}
		if exit {
			return
		}
	}
}

// runInteractiveSSH opens a PTY session and proxies os.Stdin to it, polling
// for output until the user sends Ctrl-D (EOF) or "/exit".
func runInteractiveSSH(ctx context.Context, app *App, line string, out io.Writer) (bool, error) {
	name := strings.TrimSpace(strings.TrimPrefix(strings.Fields(line)[0], "/ssh"))
	if name == "" {
		name = strings.TrimSpace(line[len("/ssh"):])
	}
	if name == "" {
		fmt.Fprintln(out, "usage: /ssh <name>")
		return true, nil
	}
	// Ensure connected.
	if _, _ = app.Execute(ctx, "/ssh "+name); !app.Pool.IsConnected(name) {
		fmt.Fprintf(out, "cannot connect to %q\n", name)
		return true, nil
	}
	sess, err := app.Term.OpenTerminal(name, 80, 24)
	if err != nil {
		return true, fmt.Errorf("open terminal: %w", err)
	}
	fmt.Fprintf(out, "[connected to %s — type /exit to leave]\n", name)
	// Pump stdin.
	go func() {
		br := bufio.NewReader(os.Stdin)
		for {
			b, err := br.ReadByte()
			if err != nil {
				return
			}
			if b == 4 { // Ctrl-D
				_ = app.Term.Close(sess.ID)
				return
			}
			if err := app.Term.Write(sess.ID, []byte{b}); err != nil {
				return
			}
		}
	}()
	// Poll output.
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = app.Term.Close(sess.ID)
			return true, nil
		case <-tick.C:
			chunk, err := app.Term.Read(sess.ID, true)
			if err != nil {
				return true, nil
			}
			if chunk != "" {
				fmt.Fprint(out, chunk)
			}
		}
	}
}
