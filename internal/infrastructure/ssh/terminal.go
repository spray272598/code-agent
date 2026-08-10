package ssh

import (
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
	}
	t.mu.Lock()
	t.sessions[id] = ts
	t.mu.Unlock()
	return &model.TerminalSession{
		ID: id, ConnectionID: connName, Cols: cols, Rows: rows,
		Active: true, CreatedAt: time.Now(), LastActiveAt: time.Now(),
	}, nil
}

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

func (t *Terminal) Read(sessionID string) (string, error) {
	t.mu.Lock()
	ts, ok := t.sessions[sessionID]
	t.mu.Unlock()
	if !ok || !ts.active {
		return "", fmt.Errorf("terminal session not found or inactive")
	}
	// 预留：实际使用时需要通过 pipe 异步读取
	return "", nil
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
