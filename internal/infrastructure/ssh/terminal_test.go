package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/spray272598/code-agent/internal/domain/ssh/model"
	sshlib "golang.org/x/crypto/ssh"
)

// startTestSSHServer 启动一个进程内 SSH server，对 session 做 pty-req/shell/window-change
// 应答，并把客户端输入原样回显，用于端到端验证 Terminal 的读/写/调整/关闭。
func startTestSSHServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	signer, err := sshlib.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	srv := &sshlib.ServerConfig{
		PasswordCallback: func(_ sshlib.ConnMetadata, pass []byte) (*sshlib.Permissions, error) {
			if string(pass) == "pw" {
				return &sshlib.Permissions{}, nil
			}
			return nil, fmt.Errorf("bad password")
		},
	}
	srv.AddHostKey(signer)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := sshlib.NewServerConn(c, srv)
				if err != nil {
					return
				}
				defer sshConn.Close()
				go sshlib.DiscardRequests(reqs)
				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						_ = newCh.Reject(sshlib.UnknownChannelType, "unknown")
						continue
					}
					ch, reqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for req := range reqs {
							switch req.Type {
							case "pty-req", "shell", "window-change":
								_ = req.Reply(true, nil)
							default:
								_ = req.Reply(req.WantReply, nil)
							}
						}
					}()
					_, _ = ch.Write([]byte("welcome\n$ "))
					buf := make([]byte, 4096)
					for {
						n, err := ch.Read(buf)
						if n > 0 {
							_, _ = ch.Write(buf[:n]) // 回显，使终端缓冲可见
						}
						if err != nil {
							break
						}
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func newTestPoolAndTerminal(t *testing.T) (*Terminal, func()) {
	t.Helper()
	addr, stop := startTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	pool := NewPool()
	cfg := model.ConnectionConfig{
		Name:     "test",
		Host:     host,
		Port:     port,
		Username: "u",
		AuthType: "password",
		Password: "pw",
		Enabled:  true,
	}
	if err := pool.Connect(context.Background(), cfg); err != nil {
		stop()
		t.Fatalf("connect: %v", err)
	}
	return NewTerminal(pool), func() {
		pool.CloseAll()
		stop()
	}
}

func TestTerminal_OpenWriteReadClose(t *testing.T) {
	term, cleanup := newTestPoolAndTerminal(t)
	defer cleanup()

	sess, err := term.OpenTerminal("test", 80, 24)
	if err != nil {
		t.Fatalf("OpenTerminal: %v", err)
	}
	if !sess.Active || sess.ID == "" {
		t.Fatalf("session not active: %+v", sess)
	}

	// 等待服务端 banner 写入缓冲
	time.Sleep(150 * time.Millisecond)
	out, err := term.Read(sess.ID, true)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(out, "welcome") {
		t.Fatalf("banner not received: %q", out)
	}

	// 写入命令，服务端回显后应在缓冲中出现
	if err := term.Write(sess.ID, []byte("echo hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	out, err = term.Read(sess.ID, true)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("command echo not received: %q", out)
	}

	if err := term.Resize(sess.ID, 120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if err := term.Close(sess.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := term.Read(sess.ID, true); err == nil {
		t.Fatal("Read after Close should error")
	}
}

func TestTerminal_OpenTerminal_UnknownConnection(t *testing.T) {
	term, cleanup := newTestPoolAndTerminal(t)
	defer cleanup()
	// 关闭底层连接后，OpenTerminal 应因找不到连接而失败
	_ = term.pool.Disconnect("test")
	if _, err := term.OpenTerminal("test", 80, 24); err == nil {
		t.Fatal("expected error opening terminal on disconnected connection")
	}
}
