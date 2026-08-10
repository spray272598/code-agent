package ssh

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshlib "golang.org/x/crypto/ssh"
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
	heartbeat time.Duration
}

func NewPool() *Pool {
	p := &Pool{
		conns:     make(map[string]*pooledConn),
		stopCh:    make(chan struct{}),
		heartbeat: 30 * time.Second,
	}
	go p.watchdog()
	return p
}

// Connect 建立SSH连接
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

// dial 建立SSH连接的具体实现
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
		HostKeyCallback: sshlib.InsecureIgnoreHostKey(),
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

// GetConnection 获取活跃连接，如果断开则尝试重连
func (p *Pool) GetConnection(name string) (*sshlib.Client, error) {
	p.mu.RLock()
	pc, ok := p.conns[name]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("ssh connection '%s' not found", name)
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if !pc.online {
		// 尝试重连
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

// Disconnect 断开指定连接
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

// IsConnected 检查连接是否活跃
func (p *Pool) IsConnected(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pc, ok := p.conns[name]
	return ok && pc.online
}

// Health 返回所有连接的健康状态
func (p *Pool) Health() []model.HealthStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]model.HealthStatus, 0, len(p.conns))
	for name, pc := range p.conns {
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
		result = append(result, status)
	}
	return result
}

// CloseAll 关闭所有连接
func (p *Pool) CloseAll() {
	close(p.stopCh)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pc := range p.conns {
		if pc.client != nil {
			_ = pc.client.Close()
		}
	}
	p.conns = make(map[string]*pooledConn)
}

// watchdog 心跳检测
func (p *Pool) watchdog() {
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

// GetConfig 获取连接配置（用于重连）
func (p *Pool) GetConfig(name string) (model.ConnectionConfig, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pc, ok := p.conns[name]
	if !ok {
		return model.ConnectionConfig{}, false
	}
	return pc.config, true
}

// ListConnections 列出所有连接名
func (p *Pool) ListConnections() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, 0, len(p.conns))
	for name := range p.conns {
		names = append(names, name)
	}
	return names
}
