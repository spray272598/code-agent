package ssh

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshlib "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type pooledConn struct {
	client   *sshlib.Client
	config   model.ConnectionConfig
	lastUsed time.Time
	online   bool
	mu       sync.Mutex
}

type Pool struct {
	mu        sync.RWMutex
	conns     map[string]*pooledConn
	stopCh    chan struct{}
	stopped   chan struct{}
	heartbeat time.Duration
	knownKeys string // path to known_hosts file, empty = InsecureIgnoreHostKey
}

func NewPool() *Pool {
	p := &Pool{
		conns:     make(map[string]*pooledConn),
		stopCh:    make(chan struct{}),
		stopped:   make(chan struct{}),
		heartbeat: 30 * time.Second,
	}
	go p.watchdog()
	return p
}

// NewPoolWithKnownHosts creates a pool that verifies host keys against a known_hosts file.
func NewPoolWithKnownHosts(knownHostsPath string) *Pool {
	p := NewPool()
	p.knownKeys = knownHostsPath
	return p
}

// Connect establishes an SSH connection.
func (p *Pool) Connect(ctx context.Context, cfg model.ConnectionConfig) error {
	client, err := p.dial(ctx, cfg)
	if err != nil {
		return err
	}
	p.mu.Lock()
	pc := &pooledConn{client: client, config: cfg, lastUsed: time.Now(), online: true}
	p.conns[cfg.Name] = pc
	p.mu.Unlock()
	return nil
}

// hostKeyCallback returns an appropriate host key callback based on configuration.
func (p *Pool) hostKeyCallback() sshlib.HostKeyCallback {
	if p.knownKeys == "" {
		return sshlib.InsecureIgnoreHostKey()
	}
	kh, err := knownhosts.New(p.knownKeys)
	if err != nil {
		// Fall back to insecure if known_hosts file is invalid/missing.
		return sshlib.InsecureIgnoreHostKey()
	}
	return kh
}

// dial establishes an SSH connection.
func (p *Pool) dial(ctx context.Context, cfg model.ConnectionConfig) (*sshlib.Client, error) {
	authMethods := make([]sshlib.AuthMethod, 0)
	if cfg.AuthType == "private_key" && cfg.PrivateKey != "" {
		signer, err := sshlib.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, sshlib.PublicKeys(signer))
	} else if cfg.Password != "" {
		authMethods = append(authMethods, sshlib.Password(cfg.Password))
	} else {
		return nil, fmt.Errorf("no auth method provided")
	}

	port := cfg.Port
	if port == 0 {
		port = 22
	}

	sshCfg := &sshlib.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: p.hostKeyCallback(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := sshlib.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return sshlib.NewClient(sshConn, chans, reqs), nil
}

// GetConnection returns an active connection, reconnecting if necessary.
func (p *Pool) GetConnection(name string) (*sshlib.Client, error) {
	p.mu.Lock()
	pc, ok := p.conns[name]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ssh connection '%s' not found", name)
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.online {
		client, err := p.dial(context.Background(), pc.config)
		if err != nil {
			return nil, fmt.Errorf("reconnect failed: %w", err)
		}
		pc.client = client
		pc.online = true
	}
	pc.lastUsed = time.Now()
	return pc.client, nil
}

// Disconnect closes and removes a named connection.
func (p *Pool) Disconnect(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.conns[name]
	if !ok {
		return fmt.Errorf("connection '%s' not found", name)
	}
	if pc.client != nil {
		_ = pc.client.Close()
	}
	delete(p.conns, name)
	return nil
}

// IsConnected checks if a connection is active.
func (p *Pool) IsConnected(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pc, ok := p.conns[name]
	return ok && pc.online
}

// Health returns the health status of all connections.
func (p *Pool) Health() []model.HealthStatus {
	p.mu.RLock()
	names := make([]string, 0, len(p.conns))
	for name := range p.conns {
		names = append(names, name)
	}
	p.mu.RUnlock()

	result := make([]model.HealthStatus, 0, len(names))
	for _, name := range names {
		p.mu.RLock()
		pc := p.conns[name]
		p.mu.RUnlock()
		if pc == nil {
			continue
		}
		pc.mu.Lock()
		status := model.HealthStatus{Name: name, Online: pc.online}
		if pc.online && pc.client != nil {
			start := time.Now()
			_, _, err := pc.client.SendRequest("keepalive@golang.org", true, nil)
			status.Latency = time.Since(start)
			if err != nil {
				status.Online = false
				status.Error = err.Error()
			}
		}
		pc.mu.Unlock()
		result = append(result, status)
	}
	return result
}

// CloseAll closes all connections and stops the watchdog.
func (p *Pool) CloseAll() {
	close(p.stopCh)
	<-p.stopped // wait for watchdog to finish
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pc := range p.conns {
		if pc.client != nil {
			_ = pc.client.Close()
		}
	}
	p.conns = make(map[string]*pooledConn)
}

// watchdog performs periodic health checks.
func (p *Pool) watchdog() {
	defer close(p.stopped)
	ticker := time.NewTicker(p.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.healthCheck()
		}
	}
}

func (p *Pool) healthCheck() {
	p.mu.RLock()
	names := make([]string, 0, len(p.conns))
	for name := range p.conns {
		names = append(names, name)
	}
	p.mu.RUnlock()

	for _, name := range names {
		p.mu.RLock()
		pc := p.conns[name]
		p.mu.RUnlock()
		if pc == nil {
			continue
		}
		pc.mu.Lock()
		if pc.online && pc.client != nil {
			_, _, err := pc.client.SendRequest("keepalive@golang.org", true, nil)
			if err != nil {
				pc.online = false
			}
		}
		pc.mu.Unlock()
	}
}

// GetConfig returns the connection configuration (for reconnection).
func (p *Pool) GetConfig(name string) (model.ConnectionConfig, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pc, ok := p.conns[name]
	if !ok {
		return model.ConnectionConfig{}, false
	}
	return pc.config, true
}

// ListConnections returns all connection names.
func (p *Pool) ListConnections() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.conns))
	for name := range p.conns {
		names = append(names, name)
	}
	return names
}

// EnsureDefaultKnownHosts creates a default known_hosts file if it doesn't exist.
func EnsureDefaultKnownHosts(dir string) (string, error) {
	path := filepath.Join(dir, "known_hosts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			return "", fmt.Errorf("create known_hosts: %w", err)
		}
	}
	return path, nil
}
