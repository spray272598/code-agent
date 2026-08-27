package jsonrpc

import (
	"encoding/json"
	"errors"
)

const Version = "2.0"

type ID = json.RawMessage

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      ID              `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      ID              `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e.Data != nil {
		return "jsonrpc error " + itoa(e.Code) + ": " + e.Message + " (data: " + jsonStr(e.Data) + ")"
	}
	return "jsonrpc error " + itoa(e.Code) + ": " + e.Message
}

func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

const (
	CodeTimeout        = -32001
	CodeUnauthorized   = -32002
	CodeForbidden      = -32003
	CodeConnectionLost = -32004
	CodeToolNotFound   = -32011
	CodeToolBusy       = -32016
)

func NewError(code int, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

func NewErrorWithData(code int, msg string, data any) *Error {
	return &Error{Code: code, Message: msg, Data: data}
}

func ParseFrame(raw []byte) (any, error) {
	var peek struct {
		Method *string `json:"method"`
		ID     *ID     `json:"id"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, NewError(CodeParseError, "invalid JSON")
	}

	if peek.Method != nil && peek.ID != nil {
		var r Request
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, NewError(CodeParseError, "invalid request")
		}
		return &r, nil
	}

	if peek.Method != nil && peek.ID == nil {
		var n Notification
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, NewError(CodeParseError, "invalid notification")
		}
		return &n, nil
	}

	if peek.Method == nil && peek.ID != nil {
		var r Response
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, NewError(CodeParseError, "invalid response")
		}
		return &r, nil
	}

	return nil, NewError(CodeInvalidRequest, "missing method and id")
}

func MustID(v any) ID {
	switch val := v.(type) {
	case string:
		b, _ := json.Marshal(val)
		return b
	case float64:
		b, _ := json.Marshal(val)
		return b
	case int:
		b, _ := json.Marshal(val)
		return b
	case nil:
		return nil
	default:
		b, _ := json.Marshal(val)
		return b
	}
}

func IDString(id ID) string {
	if id == nil {
		return "<nil>"
	}
	return string(id)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
