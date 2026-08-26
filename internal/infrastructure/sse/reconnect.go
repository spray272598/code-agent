package sse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRetryInitialBackoff = 1 * time.Second
	DefaultRetryMaxBackoff     = 30 * time.Second
	DefaultRetryMaxAttempts    = 5
	DefaultRetryJitterFactor   = 0.2
)

type ReconnectPolicy struct {
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	MaxAttempts     int
	JitterFactor    float64
	EnableAutoRetry bool
}

func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		InitialBackoff:  DefaultRetryInitialBackoff,
		MaxBackoff:      DefaultRetryMaxBackoff,
		MaxAttempts:     DefaultRetryMaxAttempts,
		JitterFactor:    DefaultRetryJitterFactor,
		EnableAutoRetry: true,
	}
}

type ReconnectManager struct {
	mu              sync.Mutex
	policy          ReconnectPolicy
	lastEventID     uint64
	retryCount      int
	lastErr         error
	lastErrAt       time.Time
	lastReconnectAt time.Time
}

func NewReconnectManager(policy ReconnectPolicy) *ReconnectManager {
	return &ReconnectManager{
		policy: policy,
	}
}

func (r *ReconnectManager) SetLastEventID(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastEventID = id
}

func (r *ReconnectManager) LastEventID() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastEventID
}

func (r *ReconnectManager) RecordError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr = err
	r.lastErrAt = time.Now()
}

func (r *ReconnectManager) RecordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr = nil
	r.retryCount = 0
}

func (r *ReconnectManager) CanRetry() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policy.EnableAutoRetry && r.retryCount < r.policy.MaxAttempts
}

func (r *ReconnectManager) NextBackoff() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.retryCount >= r.policy.MaxAttempts {
		return 0
	}

	backoff := r.policy.InitialBackoff
	maxB := r.policy.MaxBackoff

	for i := 0; i < r.retryCount; i++ {
		backoff *= 2
		if backoff >= maxB {
			backoff = maxB
			break
		}
	}

	jitter := float64(backoff) * r.policy.JitterFactor
	jittered := backoff + time.Duration(rand.Float64()*2*jitter-jitter)
	if jittered < 0 {
		jittered = 0
	}

	r.retryCount++
	r.lastReconnectAt = time.Now()
	return jittered
}

func (r *ReconnectManager) Stats() (retryCount int, lastErr error, lastReconnectAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retryCount, r.lastErr, r.lastReconnectAt
}

type SSEClientConfig struct {
	URL           string
	SessionID     string
	Headers       map[string]string
	InitialLastID uint64
	Reconnect     ReconnectPolicy
}

func DefaultSSEClientConfig(url, sessionID string) SSEClientConfig {
	return SSEClientConfig{
		URL:       url,
		SessionID: sessionID,
		Headers:   map[string]string{},
		Reconnect: DefaultReconnectPolicy(),
	}
}

type SSEClient struct {
	config  SSEClientConfig
	manager *ReconnectManager
	client  *http.Client
}

func NewSSEClient(config SSEClientConfig) *SSEClient {
	return &SSEClient{
		config:  config,
		manager: NewReconnectManager(config.Reconnect),
		client:  &http.Client{Timeout: 0},
	}
}

func (c *SSEClient) ReconnectManager() *ReconnectManager {
	return c.manager
}

func (c *SSEClient) Connect(ctx context.Context) (chan *StructuredEvent, error) {
	req, err := c.buildRequest(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New("unexpected status: " + strconv.Itoa(resp.StatusCode))
	}

	if c.config.InitialLastID > 0 {
		c.manager.SetLastEventID(c.config.InitialLastID)
	}

	ch := make(chan *StructuredEvent, 256)
	go c.consumeLoop(ctx, resp, ch)

	return ch, nil
}

func (c *SSEClient) buildRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.URL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "text/event-stream")
	if c.config.SessionID != "" {
		req.Header.Set("X-Session-ID", c.config.SessionID)
	}
	if lastID := c.manager.LastEventID(); lastID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastID, 10))
	}
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (c *SSEClient) consumeLoop(ctx context.Context, resp *http.Response, ch chan<- *StructuredEvent) {
	defer func() {
		close(ch)
		resp.Body.Close()
	}()

	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ev, err := readSSEEvent(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.manager.RecordError(err)
				if c.manager.CanRetry() {
					backoff := c.manager.NextBackoff()
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
						resp.Body.Close()
						req, err := c.buildRequest(ctx)
						if err != nil {
							return
						}
						newResp, err := c.client.Do(req)
						if err != nil {
							c.manager.RecordError(err)
							if !c.manager.CanRetry() {
								return
							}
							continue
						}
						if newResp.StatusCode != http.StatusOK {
							newResp.Body.Close()
							c.manager.RecordError(errors.New("bad status"))
							if !c.manager.CanRetry() {
								return
							}
							continue
						}
						resp = newResp
						reader = bufio.NewReader(resp.Body)
						c.manager.RecordSuccess()
						continue
					}
				}
			}
			return
		}

		if ev != nil {
			if ev.Seq > 0 {
				c.manager.SetLastEventID(ev.Seq)
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

func readSSEEvent(reader *bufio.Reader) (*StructuredEvent, error) {
	var eventType EventType
	var dataParts []string
	var lastID uint64

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if len(line) > 0 {
				ev := buildEvent(eventType, dataParts, lastID)
				if ev != nil {
					return ev, nil
				}
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			if len(dataParts) > 0 || eventType != "" {
				return buildEvent(eventType, dataParts, lastID), nil
			}
			return nil, nil
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}

		field := line[:colonIdx]
		value := strings.TrimLeft(line[colonIdx+1:], " ")

		switch field {
		case "event":
			eventType = EventType(value)
		case "data":
			dataParts = append(dataParts, value)
		case "id":
			if id, err := strconv.ParseUint(value, 10, 64); err == nil {
				lastID = id
			}
		case "retry":
			// ignore
		}
	}
}

func buildEvent(eventType EventType, dataParts []string, lastID uint64) *StructuredEvent {
	if eventType == "" && len(dataParts) == 0 {
		return nil
	}

	data := ""
	for i, p := range dataParts {
		if i > 0 {
			data += "\n"
		}
		data += p
	}

	ev := &StructuredEvent{
		Seq:       lastID,
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
	}

	if data != "" {
		if err := json.Unmarshal([]byte(data), ev); err == nil && ev.Type == "" {
			// Use the decoded event type from the data body if not set
		}
		if data[0] == '{' || data[0] == '[' {
			ev.Data = json.RawMessage(data)
			if ev.Type == "" {
				var typed struct {
					Type EventType `json:"type"`
				}
				if err := json.Unmarshal([]byte(data), &typed); err == nil && typed.Type != "" {
					ev.Type = typed.Type
				}
			}
		} else {
			ev.Content = data
		}
	}

	if ev.Type == "" {
		ev.Type = EventTextDelta
	}

	return ev
}

type SSEStatusError struct {
	Code int
	Msg  string
}

func (e *SSEStatusError) Error() string {
	return "sse: " + strconv.Itoa(e.Code) + " " + e.Msg
}
