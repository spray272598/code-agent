package sse

import (
	"sync/atomic"
)

var (
	sseActiveConnections  atomic.Int64
	sseTotalConnections   atomic.Int64
	sseTotalEvents        atomic.Int64
	sseTotalBytes         atomic.Int64
	sseTotalDropped       atomic.Int64
	sseReconnectAttempts  atomic.Int64
	sseReconnectSuccesses atomic.Int64
	sseDoomLoopDetected   atomic.Int64
	sseHeartbeatsSent     atomic.Int64
)

func SSEObserveConnection()       { sseActiveConnections.Add(1); sseTotalConnections.Add(1) }
func SSEObserveDisconnection()    { sseActiveConnections.Add(-1) }
func SSEObserveEvent()            { sseTotalEvents.Add(1) }
func SSEObserveBytes(n int64)     { sseTotalBytes.Add(n) }
func SSEObserveDrop()             { sseTotalDropped.Add(1) }
func SSEObserveReconnectAttempt() { sseReconnectAttempts.Add(1) }
func SSEObserveReconnectSuccess() { sseReconnectSuccesses.Add(1) }
func SSEObserveDoomLoop()         { sseDoomLoopDetected.Add(1) }
func SSEObserveHeartbeat()        { sseHeartbeatsSent.Add(1) }

func SSEActiveConnections() int64  { return sseActiveConnections.Load() }
func SSETotalConnections() int64   { return sseTotalConnections.Load() }
func SSETotalEvents() int64        { return sseTotalEvents.Load() }
func SSETotalBytes() int64         { return sseTotalBytes.Load() }
func SSETotalDropped() int64       { return sseTotalDropped.Load() }
func SSEReconnectAttempts() int64  { return sseReconnectAttempts.Load() }
func SSEReconnectSuccesses() int64 { return sseReconnectSuccesses.Load() }
func SSEDoomLoopDetected() int64   { return sseDoomLoopDetected.Load() }
func SSEHeartbeatsSent() int64     { return sseHeartbeatsSent.Load() }
