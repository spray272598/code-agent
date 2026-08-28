package application

import (
	"context"
	"fmt"

	sshmodel "github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshport "github.com/spray272598/code-agent/internal/domain/ssh/port"
	sshinfra "github.com/spray272598/code-agent/internal/infrastructure/ssh"
)

type SSHService struct {
	pool *sshinfra.Pool
	repo sshport.IConnectionRepository
}

func (s *SSHService) InstallSSH(ctx context.Context, cfg sshmodel.ConnectionConfig) error {
	if s.pool == nil || s.repo == nil {
		return fmt.Errorf("ssh disabled")
	}
	if cfg.ID == "" {
		cfg.ID = newID("ssh")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "password"
	}
	if err := s.repo.Save(ctx, &cfg); err != nil {
		return err
	}
	if cfg.Enabled {
		return s.pool.Connect(ctx, cfg)
	}
	return nil
}

func (s *SSHService) ListSSHConnections(ctx context.Context) ([]*sshmodel.ConnectionConfig, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("ssh disabled")
	}
	return s.repo.List(ctx)
}

func (s *SSHService) DeleteSSHConnection(ctx context.Context, id string) error {
	if s.pool == nil || s.repo == nil {
		return fmt.Errorf("ssh disabled")
	}
	cfg, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if cfg != nil {
		_ = s.pool.Disconnect(cfg.Name)
	}
	return s.repo.Delete(ctx, id)
}

func (s *SSHService) SSHHealth() []sshmodel.HealthStatus {
	if s.pool == nil {
		return nil
	}
	return s.pool.Health()
}
