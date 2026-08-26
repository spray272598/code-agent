package subagent

import (
	"testing"
)

func TestRegexDebug(t *testing.T) {
	text1 := "I can't proceed without user input.\n\nOther paragraph."
	paras := splitParagraphs(text1)
	t.Logf("text1 paras len=%d", len(paras))
	for i, p := range paras {
		t.Logf("para[%d] = %q", i, p)
	}

	if len(paras) != 2 {
		t.Errorf("want 2 paras, got %d", len(paras))
		return
	}
	if paras[0] != "I can't proceed without user input." {
		t.Errorf("para0 = %q", paras[0])
	}

	// Check if paras[0] matches the regex
	d := NewPrematureStopDetector()
	if got := d.Detect(text1); got != "unable_to_proceed" {
		t.Errorf("detect(text1) = %q, want unable_to_proceed", got)
	}
}
