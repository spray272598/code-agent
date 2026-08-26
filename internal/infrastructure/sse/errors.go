package sse

import "errors"

var (
	ErrPoolFull        = errors.New("SSE connection pool is full")
	ErrBufferFull      = errors.New("backpressure buffer is full")
	ErrConnectionClosed = errors.New("SSE connection is closed")
	ErrContextCanceled  = errors.New("request context canceled")
	ErrWriteTimeout     = errors.New("SSE write timeout")
)
