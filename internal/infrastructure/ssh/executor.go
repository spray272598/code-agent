package ssh

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	"github.com/spray272598/code-agent/internal/types/common"
	sshlib "golang.org/x/crypto/ssh"
)

type Executor struct {
	pool *Pool
}

func NewExecutor(pool *Pool) *Executor {
	return &Executor{pool: pool}
}

func (e *Executor) Exec(ctx context.Context, connName, command string, timeout time.Duration) (*model.ExecResult, error) {
	client, err := e.pool.GetConnection(connName)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	defer session.Close()

	start := time.Now()
	done := make(chan struct{})
	var output []byte
	var execErr error

	go func() {
		defer close(done)
		output, execErr = session.CombinedOutput(command)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		_ = session.Signal(sshlib.SIGKILL)
		return &model.ExecResult{
			Output:   "command timed out after " + timeout.String(),
			ExitCode: -1,
			Command:  command,
			Duration: time.Since(start),
		}, ctx.Err()
	}

	result := &model.ExecResult{
		Output:   string(output),
		Command:  command,
		Duration: time.Since(start),
	}
	if execErr != nil {
		if exitErr, ok := execErr.(*sshlib.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else {
			result.ExitCode = -1
			result.Output = result.Output + "\n" + execErr.Error()
		}
	}
	result.Output = common.TruncateRunes(result.Output, common.BashOutputMaxRunes)
	return result, nil
}

func (e *Executor) ExecStreaming(ctx context.Context, connName, command string, timeout time.Duration, onChunk func(string)) (*model.ExecResult, error) {
	client, err := e.pool.GetConnection(connName)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := session.Start(command); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	var sb strings.Builder
	done := make(chan struct{})

	go func() {
		defer close(done)
		reader := io.MultiReader(stdout, stderr)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text() + "\n"
			sb.WriteString(line)
			if onChunk != nil {
				onChunk(line)
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait()
	}()

	select {
	case err := <-waitDone:
		<-done
		result := &model.ExecResult{
			Output:   common.TruncateRunes(sb.String(), common.BashOutputMaxRunes),
			Command:  command,
			Duration: time.Since(start),
		}
		if err != nil {
			if exitErr, ok := err.(*sshlib.ExitError); ok {
				result.ExitCode = exitErr.ExitStatus()
			} else {
				result.ExitCode = -1
			}
		}
		return result, nil
	case <-ctx.Done():
		_ = session.Signal(sshlib.SIGKILL)
		return &model.ExecResult{
			Output:   sb.String() + "\ncommand timed out",
			ExitCode: -1,
			Command:  command,
			Duration: time.Since(start),
		}, ctx.Err()
	}
}
