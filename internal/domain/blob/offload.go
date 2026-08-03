package blob

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/spray272598/code-agent/internal/types/common"
)

// Default threshold (~4k runes) before offloading full payload.
const DefaultThreshold = 4000

// OffloadIfLarge stores full content when over threshold; returns preview for LLM.
func OffloadIfLarge(ctx context.Context, store Store, sessionID, tool string, content string, threshold int) OffloadResult {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	n := utf8.RuneCountInString(content)
	if store == nil || n <= threshold {
		return OffloadResult{Preview: content, Bytes: len(content), Offloaded: false}
	}
	key := fmt.Sprintf("sessions/%s/tools/%s-%d.txt", sessionID, tool, common.EstimateTokens(content))
	if err := store.Put(ctx, key, []byte(content), "text/plain; charset=utf-8"); err != nil {
		// fallback: truncate only
		return OffloadResult{
			Preview: common.TruncateRunes(content, threshold) + "\n...[offload failed, truncated]",
			Bytes:   len(content), Offloaded: false,
		}
	}
	preview := common.TruncateRunes(content, threshold/2)
	preview += fmt.Sprintf("\n\n[OFFLOADED full_result object_key=%s bytes=%d; use storage API or ask user to fetch]", key, len(content))
	return OffloadResult{Preview: preview, ObjectKey: key, Bytes: len(content), Offloaded: true}
}
