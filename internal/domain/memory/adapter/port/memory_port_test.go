package port

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	if got := CosineSimilarity(a, b); math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("identical vectors should be 1.0, got %v", got)
	}
	c := []float32{0, 1, 0}
	if got := CosineSimilarity(a, c); math.Abs(got-0.0) > 1e-6 {
		t.Fatalf("orthogonal vectors should be 0, got %v", got)
	}
	// mismatch length → 0
	if got := CosineSimilarity(a, []float32{1, 0}); got != 0 {
		t.Fatalf("mismatch length should be 0, got %v", got)
	}
	// empty → 0
	if got := CosineSimilarity(nil, a); got != 0 {
		t.Fatalf("empty should be 0, got %v", got)
	}
}

func TestEmbeddingRoundTrip(t *testing.T) {
	v := []float32{0.1, 0.2, 0.3}
	s := EncodeEmbedding(v)
	got := DecodeEmbedding(s)
	if len(got) != len(v) {
		t.Fatalf("roundtrip length mismatch: %d vs %d", len(got), len(v))
	}
	for i := range v {
		if math.Abs(float64(v[i]-got[i])) > 1e-6 {
			t.Fatalf("roundtrip value mismatch at %d", i)
		}
	}
	if DecodeEmbedding("") != nil {
		t.Fatal("empty string should decode to nil")
	}
	if EncodeEmbedding(nil) != "" {
		t.Fatal("nil vector should encode to empty string")
	}
}
