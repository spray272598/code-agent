package memory

import (
	"math"
	"sort"
	"strings"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

const defaultMMRLambda = 0.7

func jaccardSimilarity(a, b string) float64 {
	tokensA := tokenizeSet(a)
	tokensB := tokenizeSet(b)
	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 0
	}
	intersection := 0
	for t := range tokensA {
		if tokensB[t] {
			intersection++
		}
	}
	union := len(tokensA) + len(tokensB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenizeSet(s string) map[string]bool {
	tokens := memport.Tokenize(s)
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			set[t] = true
		}
	}
	return set
}

func MMRReRank(items []memport.MemoryItem, scores []float64, lambda float64) []memport.MemoryItem {
	if len(items) == 0 {
		return nil
	}
	if lambda <= 0 || lambda > 1 {
		lambda = defaultMMRLambda
	}
	n := len(items)
	selected := make([]memport.MemoryItem, 0, n)
	remaining := make([]int, n)
	for i := range remaining {
		remaining[i] = i
	}

	firstIdx := 0
	bestScore := math.Inf(-1)
	for i, s := range scores {
		if s > bestScore {
			bestScore = s
			firstIdx = i
		}
	}
	selected = append(selected, items[firstIdx])
	remaining = removeIndex(remaining, firstIdx)

	for len(remaining) > 0 {
		var bestIdx int = -1
		bestMMR := math.Inf(-1)
		lastSelected := selected[len(selected)-1]

		for _, idx := range remaining {
			relevance := scores[idx]
			maxSim := 0.0
			for _, sel := range selected {
				sim := jaccardSimilarity(items[idx].Content, sel.Content)
				if sim > maxSim {
					maxSim = sim
				}
			}
			mmrScore := lambda*relevance - (1-lambda)*maxSim
			if mmrScore > bestMMR {
				bestMMR = mmrScore
				bestIdx = idx
			}
		}
		if bestIdx < 0 {
			break
		}
		_ = lastSelected
		selected = append(selected, items[bestIdx])
		remaining = removeIndex(remaining, bestIdx)
	}

	return selected
}

func removeIndex(slice []int, idx int) []int {
	for i, v := range slice {
		if v == idx {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func MMRReRankWithDefault(items []memport.MemoryItem, scores []float64) []memport.MemoryItem {
	return MMRReRank(items, scores, defaultMMRLambda)
}

func scoreAndSort(items []memport.MemoryItem, scores []float64) []memport.MemoryItem {
	type scored struct {
		it    memport.MemoryItem
		score float64
	}
	ranked := make([]scored, len(items))
	for i := range items {
		ranked[i] = scored{it: items[i], score: scores[i]}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	out := make([]memport.MemoryItem, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].it
	}
	return out
}