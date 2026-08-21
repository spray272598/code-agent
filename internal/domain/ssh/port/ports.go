package port

import (
	"context"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	"golang.org/x/crypto/ssh"
)

// IConnectionPool SSH连接池接口
type IConnectionPool interface {
	Connect(ctx context.Context, cfg model.ConnectionConfig) error
	Disconnect(name string) error
	GetConnection(name string) (ssh.Conn, error) // 返回活跃连接
	IsConnected(name string) bool
	Health() []model.HealthStatus
	CloseAll()
}

// IExecutor SSH命令执行器接口
type IExecutor interface {
	Exec(ctx context.Context, connName, command string, timeout time.Duration) (*model.ExecResult, error)
	ExecStreaming(ctx context.Context, connName, command string, timeout time.Duration, onChunk func(string)) (*model.ExecResult, error)
}

// IFileTransfer SFTP文件操作接口
type IFileTransfer interface {
	ReadFile(ctx context.Context, connName, path string) ([]byte, error)
	WriteFile(ctx context.Context, connName, path string, content []byte) error
	ListDir(ctx context.Context, connName, path string) ([]model.FileEntry, error)
	Delete(ctx context.Context, connName, path string) error
	Mkdir(ctx context.Context, connName, path string) error
}

// ITerminal PTY终端接口（交互式 shell / WebSocket 终端）
type ITerminal interface {
	OpenTerminal(connName string, cols, rows int) (*model.TerminalSession, error)
	Write(sessionID string, data []byte) error
	// Read 返回缓冲区中累积的 PTY 输出；clear=true 时读取后清空缓冲，
	// 多次读取之间只返回自上次清空以来的新输出。
	Read(sessionID string, clear bool) (string, error)
	Close(sessionID string) error
	Resize(sessionID string, cols, rows int) error
}

// IConnectionRepository 连接配置持久化接口
type IConnectionRepository interface {
	Save(ctx context.Context, cfg *model.ConnectionConfig) error
	FindByID(ctx context.Context, id string) (*model.ConnectionConfig, error)
	FindByName(ctx context.Context, name string) (*model.ConnectionConfig, error)
	List(ctx context.Context) ([]*model.ConnectionConfig, error)
	Delete(ctx context.Context, id string) error
}
