package model

import "time"

// ConnectionConfig SSH连接配置
type ConnectionConfig struct {
	ID              string
	Name            string
	Host            string
	Port            int
	Username        string
	AuthType        string // "password" | "private_key"
	Password        string
	PrivateKey      string
	Enabled         bool
	LastConnectedAt time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ExecResult 命令执行结果
type ExecResult struct {
	Output   string
	ExitCode int
	Command  string
	Duration time.Duration
}

// FileEntry 文件目录条目
type FileEntry struct {
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
	Mode    string
}

// HealthStatus 连接健康状态
type HealthStatus struct {
	Name    string
	Online  bool
	Latency time.Duration
	Error   string
}

// TerminalSession PTY终端会话（预留WebSocket）
type TerminalSession struct {
	ID           string
	ConnectionID string
	Cols         int
	Rows         int
	Active       bool
	CreatedAt    time.Time
	LastActiveAt time.Time
}
