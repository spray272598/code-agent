package llm

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryCount int
		maxRetries int
		want       RetryDecision
		wantMinB   time.Duration
		wantMaxB   time.Duration
		wantNilB   bool
	}{
		// --- 401 / 403: always emit, never retry ---
		{
			name:   "401 unauthorized emits to session",
			status: 401, body: `{"error":"unauthorized"}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionEmitToSession, wantNilB: true,
		},
		{
			name:   "403 forbidden emits to session",
			status: 403, body: `{"error":"forbidden"}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionEmitToSession, wantNilB: true,
		},
		{
			name:   "401 still emits even with retries exhausted",
			status: 401, body: `{"error":"unauthorized"}`,
			retryCount: 5, maxRetries: 5,
			want: RetryDecisionEmitToSession, wantNilB: true,
		},

		// --- 429: rate limit → backoff ---
		{
			name:   "429 rate limited",
			status: 429, body: `{"error":"rate limited"}`,
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},
		{
			name:   "429 second retry uses larger backoff",
			status: 429, body: `{"error":"rate limited"}`,
			retryCount: 2, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 6400 * time.Millisecond, wantMaxB: 30 * time.Second,
		},

		// --- 400 context length exceeded → compaction ---
		{
			name:   "400 openai context_length_exceeded",
			status: 400, body: `{"error":{"message":"This model's maximum context length is 128000 tokens","type":"invalid_request_error","code":"context_length_exceeded"}}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionRetryCompaction, wantNilB: true,
		},
		{
			name:   "400 anthropic prompt too long",
			status: 400, body: `{"error":{"type":"invalid_request_error","message":"prompt is too long: 200000 tokens > 200000"}}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionRetryCompaction, wantNilB: true,
		},
		{
			name:   "400 openai maximum context length",
			status: 400, body: `{"error":{"message":"maximum context length is 8192 tokens"}}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionRetryCompaction, wantNilB: true,
		},
		{
			name:   "400 anthropic too many tokens",
			status: 400, body: `{"error":{"message":"too many tokens in request: 256000"}}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionRetryCompaction, wantNilB: true,
		},

		// --- 400 other: fatal ---
		{
			name:   "400 invalid parameter",
			status: 400, body: `{"error":{"message":"Invalid value for 'temperature'","type":"invalid_request_error"}}`,
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionFatal, wantNilB: true,
		},
		{
			name:   "400 empty body",
			status: 400, body: "",
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionFatal, wantNilB: true,
		},

		// --- 5xx: server error → retry backoff ---
		{
			name:   "500 internal server error",
			status: 500, body: `{"error":"internal error"}`,
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},
		{
			name:   "502 bad gateway",
			status: 502, body: `bad gateway`,
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},
		{
			name:   "503 service unavailable",
			status: 503, body: `service unavailable`,
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},
		{
			name:   "529 overloaded (openai)",
			status: 529, body: `{"error":"overloaded"}`,
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},

		// --- budget exhausted ---
		{
			name:   "budget exhausted fatal",
			status: 500, body: `{"error":"internal error"}`,
			retryCount: 5, maxRetries: 5,
			want: RetryDecisionFatal, wantNilB: true,
		},
		{
			name:   "budget exhausted (retryCount > maxRetries)",
			status: 503, body: "",
			retryCount: 10, maxRetries: 5,
			want: RetryDecisionFatal, wantNilB: true,
		},
		{
			name:   "no budget at all (maxRetries=0)",
			status: 500, body: "",
			retryCount: 0, maxRetries: 0,
			want: RetryDecisionFatal, wantNilB: true,
		},

		// --- other retryable codes ---
		{
			name:   "408 request timeout",
			status: 408, body: "",
			retryCount: 0, maxRetries: 5,
			want:     RetryDecisionRetryBackoff,
			wantMinB: 1600 * time.Millisecond, wantMaxB: 30 * time.Second,
		},

		// --- 413 payload too large → fatal ---
		{
			name:   "413 payload too large",
			status: 413, body: "",
			retryCount: 0, maxRetries: 5,
			want: RetryDecisionFatal, wantNilB: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyLLMError(tt.status, tt.body, tt.retryCount, tt.maxRetries)
			if result.Decision != tt.want {
				t.Errorf("ClassifyLLMError(%d, ..., %d, %d) decision = %v, want %v",
					tt.status, tt.retryCount, tt.maxRetries, result.Decision, tt.want)
			}
			if tt.wantNilB && result.Backoff != 0 {
				t.Errorf("expected zero backoff, got %v", result.Backoff)
			}
			if tt.wantMinB > 0 && result.Backoff < tt.wantMinB {
				t.Errorf("backoff %v < min %v", result.Backoff, tt.wantMinB)
			}
			if tt.wantMaxB > 0 && result.Backoff > tt.wantMaxB {
				t.Errorf("backoff %v > max %v", result.Backoff, tt.wantMaxB)
			}
			if result.Reason == "" {
				t.Error("reason should not be empty")
			}
		})
	}
}

func TestRetryBackoff(t *testing.T) {
	// Verify monotonic increase up to cap, and jitter stays within ±20%
	for i := 1; i <= 10; i++ {
		b := RetryBackoff(i)
		if b > 30*time.Second+1 {
			t.Errorf("RetryBackoff(%d) = %v exceeds 30s cap", i, b)
		}
		if b < 0 {
			t.Errorf("RetryBackoff(%d) = %v is negative", i, b)
		}
	}
	// retryCount=1 should be around 2s ±20%
	b1 := RetryBackoff(1)
	if b1 < 1600*time.Millisecond || b1 > 2400*time.Millisecond {
		t.Errorf("RetryBackoff(1) = %v, want ~2s", b1)
	}
	// retryCount=5 should be at the 30s cap
	b5 := RetryBackoff(5)
	if b5 < 24*time.Second || b5 > 30*time.Second {
		t.Errorf("RetryBackoff(5) = %v, want ~30s", b5)
	}
}

func TestIsContextLengthError(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"openai context_length_exceeded", `{"code":"context_length_exceeded"}`, true},
		{"openai maximum context length", `{"message":"maximum context length is 8192 tokens"}`, true},
		{"anthropic too many tokens", `{"message":"too many tokens: 256000"}`, true},
		{"anthropic prompt too long", `{"message":"prompt is too long: 200000"}`, true},
		{"max_tokens_plus_max_prompt_tokens", `{"code":"max_tokens_plus_max_prompt_tokens"}`, true},
		{"case insensitive", `{"CODE":"Context_Length_Exceeded"}`, true},
		{"empty body", "", false},
		{"unrelated error", `{"message":"Invalid value for 'model'"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isContextLengthError(tt.body)
			if got != tt.want {
				t.Errorf("isContextLengthError(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestDecisionString(t *testing.T) {
	tests := []struct {
		d    RetryDecision
		want string
	}{
		{RetryDecisionRetry, "retry"},
		{RetryDecisionRetryBackoff, "retry_backoff"},
		{RetryDecisionRetryCompaction, "retry_compaction"},
		{RetryDecisionEmitToSession, "emit_to_session"},
		{RetryDecisionFatal, "fatal"},
		{RetryDecision(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("RetryDecision(%d).String() = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestExtractStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"http 400", fmt.Errorf("llm http 400: bad request"), 400},
		{"http 429", fmt.Errorf("llm http 429: rate limited"), 429},
		{"http 500", fmt.Errorf("llm http 500: internal error"), 500},
		{"non-http error", errors.New("connection refused"), 0},
		{"empty message", errors.New(""), 0},
		{"http 401", fmt.Errorf("llm http 401: unauthorized"), 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStatus(tt.err)
			if got != tt.want {
				t.Errorf("extractStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{123, "123"},
		{-5, "-5"},
		{999, "999"},
	}
	for _, tt := range tests {
		if got := itoa(tt.n); got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
