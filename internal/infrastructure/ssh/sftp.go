package ssh

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/pkg/sftp"
	"github.com/spray272598/code-agent/internal/domain/ssh/model"
)

type FileTransfer struct {
	pool *Pool
}

func NewFileTransfer(pool *Pool) *FileTransfer {
	return &FileTransfer{pool: pool}
}

func (f *FileTransfer) getSftpClient(connName string) (*sftp.Client, func(), error) {
	client, err := f.pool.GetConnection(connName)
	if err != nil {
		return nil, nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		return nil, nil, fmt.Errorf("sftp client: %w", err)
	}
	return sc, func() { _ = sc.Close() }, nil
}

func (f *FileTransfer) ReadFile(ctx context.Context, connName, path string) ([]byte, error) {
	sc, cleanup, err := f.getSftpClient(connName)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	r, err := sc.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (f *FileTransfer) WriteFile(ctx context.Context, connName, path string, content []byte) error {
	sc, cleanup, err := f.getSftpClient(connName)
	if err != nil {
		return err
	}
	defer cleanup()
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		_ = sc.MkdirAll(dir)
	}
	w, err := sc.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer w.Close()
	_, err = w.Write(content)
	return err
}

func (f *FileTransfer) ListDir(ctx context.Context, connName, path string) ([]model.FileEntry, error) {
	sc, cleanup, err := f.getSftpClient(connName)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	entries, err := sc.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("readdir %s: %w", path, err)
	}
	result := make([]model.FileEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, model.FileEntry{
			Name:    e.Name(),
			Size:    e.Size(),
			ModTime: e.ModTime(),
			IsDir:   e.IsDir(),
			Mode:    e.Mode().String(),
		})
	}
	return result, nil
}

func (f *FileTransfer) Delete(ctx context.Context, connName, path string) error {
	sc, cleanup, err := f.getSftpClient(connName)
	if err != nil {
		return err
	}
	defer cleanup()
	return sc.Remove(path)
}

func (f *FileTransfer) Mkdir(ctx context.Context, connName, path string) error {
	sc, cleanup, err := f.getSftpClient(connName)
	if err != nil {
		return err
	}
	defer cleanup()
	return sc.MkdirAll(path)
}
