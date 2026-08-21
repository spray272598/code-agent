package ssh

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshlib "golang.org/x/crypto/ssh"
)

type Terminal struct {
	pool     *Pool
	mu       sync.Mutex
	sessions map[string]*terminalSession
}

type terminalSession struct {
	session  *sshlib.Session
	stdin    io.WriteCloser
	connName string
	active   bool
	created  time.Time
	lastAct  time.Time

	// outBuf accumulates PTY stdout+stderr asynchronously. Read returns a
	// snapshot; Clear drains it so repeated reads don't echo stale output.
	outBuf *bytes.Buffer
	bufMu  sync.Mutex
	closed chan struct{}
}

func NewTerminal(pool *Pool) *Terminal {
	return &Terminal{
		pool:     pool,
		sessions: make(map[string]*terminalSession),
	}
}

func (t *Terminal) OpenTerminal(connName string, cols, rows int) (*model.TerminalSession, error) {
	client, err := t.pool.GetConnection(connName)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	modes := sshlib.TerminalModes{
		sshlib.ECHO:          1,
		sshlib.TTY_OP_ISPEED: 14400,
		sshlib.TTY_OP_OSPEED: 14400,
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := session.RequestPty("xterm", rows, cols, modes); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	if err := session.Shell(); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	id := fmt.Sprintf("term-%d", time.Now().UnixNano())
	ts := &terminalSession{
		session:  session,
		stdin:    stdin,
		connName: connName,
		active:   true,
		created:  time.Now(),
		lastAct:  time.Now(),
		outBuf:   new(bytes.Buffer),
		closed:   make(chan struct{}),
	}
	// Asynchronously drain the PTY into the buffer until the session ends.
	go func() {
		defer close(ts.closed)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = io.Copy(ts.outBuf, stdout) }()
		go func() { defer wg.Done(); _, _ = io.Copy(ts.outBuf, stderr) }()
		wg.Wait()
	}()
	t.mu.Lock()
	t.sessions[id] = ts
	t.mu.Unlock()
	return &model.TerminalSession{
		ID: id, ConnectionID: connName, Cols: cols, Rows: rows,
		Active: true, CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}, nil
}

// Write sends raw bytes to the interactive shell.
func (t *Terminal) Write(sessionID string, data []byte) error {
	t.mu.Lock()
	ts, ok := t.sessions[sessionID]
	t.mu.Unlock()
	if !ok || !ts.active {
		return fmt.Errorf("terminal session not found or inactive")
	}
	ts.lastAct = time.Now()
	_, err := ts.stdin.Write(data)
	return err
}

// Read returns the buffered PTY output since the last Clear (or since open).
// Pass clear=true to drain the buffer after reading (recommended between
// command sends to avoid duplicating prior output).
func (t *Terminal) Read(sessionID string, clear bool) (string, error) {
	t.mu.Lock()
	ts, ok := t.sessions[sessionID]
	t.mu.Unlock()
	if !ok || !ts.active {
		return "", fmt.Errorf("terminal session not found or inactive")
	}
	ts.bufMu.Lock()
	out := ts.outBuf.String()
	if clear {
		ts.outBuf.Reset()
	}
	ts.bufMu.Unlock()
	ts.lastAct = time.Now()
	return out, nil
}

func (t *Terminal) Close(sessionID string) error {
	t.mu.Lock()
	ts, ok := t.sessions[sessionID]
	if ok {
		delete(t.sessions, sessionID)
	}
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("session not found")
	}
	ts.active = false
	return ts.session.Close()
}

func (t *Terminal) Resize(sessionID string, cols, rows int) error {
	t.mu.Lock()
	ts, ok := t.sessions[sessionID]
	t.mu.Unlock()
	if !ok || !ts.active {
		return fmt.Errorf("terminal session not found or inactive")
	}
	return ts.session.WindowChange(rows, cols)
}
