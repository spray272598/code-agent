package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

type Transport interface {
	ReadFrame() ([]byte, error)
	WriteFrame(frame []byte) error
	Close() error
}

type StdioTransport struct {
	reader io.Reader
	writer io.Writer
	mu     sync.Mutex
}

func NewStdioTransport(r io.Reader, w io.Writer) *StdioTransport {
	return &StdioTransport{reader: r, writer: w}
}

func (t *StdioTransport) ReadFrame() ([]byte, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := t.reader.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
	}
	return line, nil
}

func (t *StdioTransport) WriteFrame(frame []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	data := append(frame, '\n')
	n, err := t.writer.(io.Writer).Write(data)
	_ = n
	return err
}

func (t *StdioTransport) Close() error {
	return nil
}

type HTTPTransport struct {
	url        string
	httpClient *http.Client
	sessionID  string
	mu         sync.Mutex
}

func NewHTTPTransport(url string, httpClient *http.Client) *HTTPTransport {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &HTTPTransport{url: url, httpClient: httpClient}
}

func (t *HTTPTransport) WriteFrame(frame []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	req, err := http.NewRequest("POST", t.url, bytes.NewReader(frame))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}
	return nil
}

func (t *HTTPTransport) ReadFrame() ([]byte, error) {
	return nil, io.ErrNoProgress
}

func (t *HTTPTransport) Close() error {
	return nil
}

type HandlerFunc func(ctx context.Context, id ID, method string, params json.RawMessage) (any, error)

type Server struct {
	handlers map[string]HandlerFunc
}

func NewServer() *Server {
	return &Server{handlers: make(map[string]HandlerFunc)}
}

func (s *Server) Handle(method string, h HandlerFunc) {
	s.handlers[method] = h
}

func (s *Server) CallHandler(ctx context.Context, id ID, method string, params json.RawMessage) (any, error) {
	h, ok := s.handlers[method]
	if !ok {
		return nil, NewError(CodeMethodNotFound, "method not found: "+method)
	}
	return h(ctx, id, method, params)
}

func (s *Server) CallHandlerDirect(ctx context.Context, id ID, method string, params json.RawMessage) error {
	_, err := s.CallHandler(ctx, id, method, params)
	return err
}

func (s *Server) Serve(ctx context.Context, t Transport) error {
	for {
		raw, err := t.ReadFrame()
		if err != nil {
			return err
		}
		go s.handleFrame(ctx, t, raw)
	}
}

func (s *Server) handleFrame(ctx context.Context, t Transport, raw []byte) {
	frame, err := ParseFrame(raw)
	if err != nil {
		s.sendError(t, nil, err.(*Error))
		return
	}

	switch f := frame.(type) {
	case *Request:
		h, ok := s.handlers[f.Method]
		if !ok {
			s.sendError(t, f.ID, NewError(CodeMethodNotFound, "method not found: "+f.Method))
			return
		}
		result, err := h(ctx, f.ID, f.Method, f.Params)
		if err != nil {
			if rpcErr, ok := err.(*Error); ok {
				s.sendError(t, f.ID, rpcErr)
			} else {
				s.sendError(t, f.ID, NewError(CodeInternalError, err.Error()))
			}
			return
		}
		s.sendResult(t, f.ID, result)
	case *Notification:
		if h, ok := s.handlers[f.Method]; ok {
			h(ctx, nil, f.Method, f.Params)
		}
	case *Response:
		// server doesn't handle responses
	}
}

func (s *Server) sendResult(t Transport, id ID, result any) {
	if result == nil {
		result = struct{}{}
	}
	b, err := json.Marshal(result)
	if err != nil {
		s.sendError(t, id, NewError(CodeInternalError, "marshal result: "+err.Error()))
		return
	}
	resp := Response{JSONRPC: Version, ID: id, Result: b}
	frame, _ := json.Marshal(resp)
	t.WriteFrame(frame)
}

func (s *Server) sendError(t Transport, id ID, e *Error) {
	resp := Response{JSONRPC: Version, ID: id, Error: e}
	frame, _ := json.Marshal(resp)
	t.WriteFrame(frame)
}

type Client struct {
	transport Transport
	pending   map[string]chan *Response
	mu        sync.Mutex
	nextID    atomic.Int64
}

func NewClient(t Transport) *Client {
	return &Client{transport: t, pending: make(map[string]chan *Response)}
}

func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	idStr := itoa(int(id))
	idBytes := MustID(idStr)
	key := string(idBytes)

	var paramsRaw json.RawMessage
	if params != nil {
		var err error
		paramsRaw, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}

	req := Request{JSONRPC: Version, ID: idBytes, Method: method, Params: paramsRaw}
	frame, _ := json.Marshal(req)

	ch := make(chan *Response, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	if err := c.transport.WriteFrame(frame); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) Notify(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		var err error
		paramsRaw, err = json.Marshal(params)
		if err != nil {
			return err
		}
	}
	n := Notification{JSONRPC: Version, Method: method, Params: paramsRaw}
	frame, _ := json.Marshal(n)
	return c.transport.WriteFrame(frame)
}

func (c *Client) ReadLoop(ctx context.Context) error {
	for {
		raw, err := c.transport.ReadFrame()
		if err != nil {
			return err
		}
		frame, err := ParseFrame(raw)
		if err != nil {
			continue
		}
		switch f := frame.(type) {
		case *Response:
			key := string(f.ID)
			c.mu.Lock()
			ch, ok := c.pending[key]
			c.mu.Unlock()
			if ok {
				select {
				case ch <- f:
				default:
				}
			}
		}
	}
}
