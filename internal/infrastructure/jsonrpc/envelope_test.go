package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

type chanTransport struct {
	inCh  chan []byte
	outCh chan []byte
}

func newChanTransport() (*chanTransport, *chanTransport) {
	a := &chanTransport{inCh: make(chan []byte, 16), outCh: make(chan []byte, 16)}
	b := &chanTransport{inCh: a.outCh, outCh: a.inCh}
	return a, b
}

func (t *chanTransport) ReadFrame() ([]byte, error) {
	raw, ok := <-t.inCh
	if !ok {
		return nil, io.EOF
	}
	return raw, nil
}

func (t *chanTransport) WriteFrame(frame []byte) error {
	cp := make([]byte, len(frame))
	copy(cp, frame)
	t.outCh <- cp
	return nil
}

func (t *chanTransport) Close() error {
	close(t.outCh)
	return nil
}

func TestParseFrameRequest(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":"1","method":"test","params":{"x":1}}`)
	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	req, ok := frame.(*Request)
	if !ok {
		t.Fatalf("expected *Request, got %T", frame)
	}
	if req.Method != "test" {
		t.Errorf("expected test, got %s", req.Method)
	}
	if string(req.ID) != `"1"` {
		t.Errorf("expected \"1\", got %s", string(req.ID))
	}
}

func TestParseFrameNotification(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"notify","params":{}}`)
	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := frame.(*Notification)
	if !ok {
		t.Fatalf("expected *Notification, got %T", frame)
	}
	if n.Method != "notify" {
		t.Errorf("expected notify, got %s", n.Method)
	}
}

func TestParseFrameResponse(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":"1","result":{"ok":true}}`)
	frame, err := ParseFrame(raw)
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := frame.(*Response)
	if !ok {
		t.Fatalf("expected *Response, got %T", frame)
	}
	if resp.Result == nil {
		t.Error("expected result")
	}
}

func TestParseFrameError(t *testing.T) {
	raw := []byte(`not json`)
	_, err := ParseFrame(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if rpcErr.Code != CodeParseError {
		t.Errorf("expected %d, got %d", CodeParseError, rpcErr.Code)
	}
}

func TestServerHandleRequest(t *testing.T) {
	srv := NewServer()
	srv.Handle("add", func(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
		var p struct{ A, B int }
		json.Unmarshal(params, &p)
		return map[string]int{"sum": p.A + p.B}, nil
	})

	pr, pw := io.Pipe()
	var buf bytes.Buffer
	tr := NewStdioTransport(pr, &buf)

	req := Request{JSONRPC: Version, ID: MustID("1"), Method: "add", Params: json.RawMessage(`{"a":2,"b":3}`)}
	b, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, tr) }()

	pw.Write(append(b, '\n'))

	select {
	case <-done:
	case <-ctx.Done():
	}

	pw.Close()
	raw := buf.Bytes()
	if len(raw) == 0 {
		t.Fatal("no response")
	}
	var resp Response
	json.Unmarshal(bytes.TrimSpace(raw), &resp)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var result map[string]int
	json.Unmarshal(resp.Result, &result)
	if result["sum"] != 5 {
		t.Errorf("expected 5, got %d", result["sum"])
	}
}

func TestServerMethodNotFound(t *testing.T) {
	srv := NewServer()
	pr, pw := io.Pipe()
	var buf bytes.Buffer
	tr := NewStdioTransport(pr, &buf)

	req := Request{JSONRPC: Version, ID: MustID("1"), Method: "nope"}
	b, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, tr) }()

	pw.Write(append(b, '\n'))

	select {
	case <-done:
	case <-ctx.Done():
	}

	pw.Close()
	raw := buf.Bytes()
	var resp Response
	json.Unmarshal(bytes.TrimSpace(raw), &resp)
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("expected %d, got %d", CodeMethodNotFound, resp.Error.Code)
	}
}

func TestClientRequestResponse(t *testing.T) {
	srv := NewServer()
	srv.Handle("echo", func(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
		return json.RawMessage(params), nil
	})

	srvTr, cliTr := newChanTransport()

	client := NewClient(cliTr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go srv.Serve(ctx, srvTr)
	go client.ReadLoop(ctx)

	result, err := client.Request(ctx, "echo", map[string]string{"msg": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]string
	json.Unmarshal(result, &r)
	if r["msg"] != "hello" {
		t.Errorf("expected hello, got %s", r["msg"])
	}

	cliTr.Close()
}

func TestClientNotify(t *testing.T) {
	srvTr, cliTr := newChanTransport()

	var received bool
	srv := NewServer()
	srv.Handle("ping", func(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
		received = true
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go srv.Serve(ctx, srvTr)

	client := NewClient(cliTr)
	err := client.Notify("ping", nil)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	cliTr.Close()

	if !received {
		t.Error("notification not received")
	}
}

func TestErrorCodes(t *testing.T) {
	e := NewError(CodeTimeout, "timed out")
	if !strings.Contains(e.Error(), "-32001") {
		t.Errorf("expected -32001 in error, got %s", e.Error())
	}

	e2 := NewErrorWithData(CodeToolNotFound, "not found", map[string]string{"name": "foo"})
	if !strings.Contains(e2.Error(), "data:") {
		t.Errorf("expected data in error, got %s", e2.Error())
	}
}
