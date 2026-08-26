package llm

import (
	"math/rand"
	"strings"
	"time"
)

// RetryDecision classifies an LLM HTTP error into an action for the caller.
type RetryDecision int

const (
	// RetryDecisionRetry means immediate retry with no backoff (e.g. doom-loop recovery).
	RetryDecisionRetry RetryDecision = iota
	// RetryDecisionRetryBackoff means retry after exponential backoff.
	RetryDecisionRetryBackoff
	// RetryDecisionRetryCompaction means the context window is full: caller
	// must compress history and resubmit, not retry blindly.
	RetryDecisionRetryCompaction
	// RetryDecisionEmitToSession means the error should surface to the session
	// layer (e.g. 401 for auth refresh, encrypted-content mismatch).
	RetryDecisionEmitToSession
	// RetryDecisionFatal means the error is deterministic and will not resolve
	// on retry (e.g. invalid request, context overflow after stripping).
	RetryDecisionFatal
)

var retryDecisionNames = [...]string{
	RetryDecisionRetry:           "retry",
	RetryDecisionRetryBackoff:    "retry_backoff",
	RetryDecisionRetryCompaction: "retry_compaction",
	RetryDecisionEmitToSession:   "emit_to_session",
	RetryDecisionFatal:           "fatal",
}

func (d RetryDecision) String() string {
	if int(d) < len(retryDecisionNames) {
		return retryDecisionNames[d]
	}
	return "unknown"
}

// RetryResult is the pure-function output of ClassifyLLMError.
type RetryResult struct {
	Decision RetryDecision
	Backoff  time.Duration // zero for immediate retry / emit / fatal
	Reason   string        // human-readable, safe for telemetry
}

// MaxLLMRetries is the default retry budget for LLM calls.
const MaxLLMRetries = 5

// RetryBackoff computes exponential backoff with ±20% jitter.
//
//	retryCount=1 → ~2s (base 2s × 2^0)
//	retryCount=2 → ~4s
//	retryCount=3 → ~8s
//	retryCount=4 → ~16s
//	retryCount≥5 → 30s (cap)
func RetryBackoff(retryCount int) time.Duration {
	const base = 2 * time.Second
	const maxBackoff = 30 * time.Second
	d := base
	for i := 1; i < retryCount; i++ {
		d *= 2
		if d >= maxBackoff {
			d = maxBackoff
			break
		}
	}
	// ±20% jitter to prevent thundering herd
	jitter := float64(d) * 0.2
	d = time.Duration(float64(d) + (rand.Float64()*2*jitter - jitter))
	if d > maxBackoff {
		d = maxBackoff
	}
	if d < 0 {
		d = 0
	}
	return d
}

// ClassifyLLMError classifies an HTTP error from the LLM endpoint into a
// RetryDecision. This is a pure function: no I/O, no logging, no side effects.
// Fully unit-testable.
//
// Parameters:
//   - statusCode: HTTP status code (>= 300)
//   - body: raw response body string (may be empty)
//   - retryCount: how many retries have already been attempted (0 = first failure)
//   - maxRetries: total retry budget (0 = no retries allowed)
func ClassifyLLMError(statusCode int, body string, retryCount, maxRetries int) RetryResult {
	// --- 401 / 403: auth errors — surface to session for credential refresh ---
	if statusCode == 401 || statusCode == 403 {
		return RetryResult{
			Decision: RetryDecisionEmitToSession,
			Reason:   "auth error: status " + httpStatusText(statusCode),
		}
	}

	// Budget exhausted: no more retries regardless of error type
	if maxRetries == 0 || retryCount >= maxRetries {
		return RetryResult{
			Decision: RetryDecisionFatal,
			Reason:   "retry budget exhausted after " + itoa(retryCount) + " attempts",
		}
	}

	// --- 429: rate limited ---
	if statusCode == 429 {
		return RetryResult{
			Decision: RetryDecisionRetryBackoff,
			Backoff:  RetryBackoff(retryCount + 1),
			Reason:   "rate limited (429)",
		}
	}

	// --- 400: context length exceeded → trigger compaction, not blind retry ---
	if statusCode == 400 && isContextLengthError(body) {
		return RetryResult{
			Decision: RetryDecisionRetryCompaction,
			Reason:   "context length exceeded: needs compaction",
		}
	}

	// --- 400: other bad request → fatal (deterministic, retrying won't help) ---
	if statusCode == 400 {
		return RetryResult{
			Decision: RetryDecisionFatal,
			Reason:   "bad request (400): " + truncateBody(body, 200),
		}
	}

	// --- 413: payload too large → could strip images then retry, but for
	//     text-only agents just surface as fatal ---
	if statusCode == 413 {
		return RetryResult{
			Decision: RetryDecisionFatal,
			Reason:   "payload too large (413)",
		}
	}

	// --- 5xx: server errors → retry with backoff ---
	if statusCode >= 500 {
		return RetryResult{
			Decision: RetryDecisionRetryBackoff,
			Backoff:  RetryBackoff(retryCount + 1),
			Reason:   "server error (" + itoa(statusCode) + ")",
		}
	}

	// --- Other (e.g. 408 timeout, 503, 529) → retry with backoff ---
	return RetryResult{
		Decision: RetryDecisionRetryBackoff,
		Backoff:  RetryBackoff(retryCount + 1),
		Reason:   "retryable error: status " + itoa(statusCode),
	}
}

// isContextLengthError detects context-length-exceeded errors from major LLM
// providers by scanning the response body for known error codes/messages.
func isContextLengthError(body string) bool {
	bodyLower := strings.ToLower(body)
	// OpenAI
	if strings.Contains(bodyLower, "context_length_exceeded") {
		return true
	}
	if strings.Contains(bodyLower, "maximum context length") {
		return true
	}
	if strings.Contains(bodyLower, "max_tokens_plus_max_prompt_tokens") {
		return true
	}
	// Anthropic
	if strings.Contains(bodyLower, "too many tokens") {
		return true
	}
	if strings.Contains(bodyLower, "prompt is too long") {
		return true
	}
	return false
}

// httpStatusText returns a short text for common HTTP status codes.
func httpStatusText(code int) string {
	switch code {
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 429:
		return "Too Many Requests"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	case 529:
		return "Overloaded"
	default:
		return itoa(code)
	}
}

// truncateBody shortens s to maxLen bytes, appending "..." if needed.
func truncateBody(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// itoa is a minimal int-to-string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
