package memory

import (
	"strings"

	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
)

var englishStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "in": true, "on": true, "at": true,
	"to": true, "for": true, "of": true, "and": true, "or": true,
	"not": true, "but": true, "if": true, "then": true, "else": true,
	"when": true, "where": true, "how": true, "what": true, "which": true,
	"who": true, "this": true, "that": true, "these": true, "those": true,
	"i": true, "you": true, "he": true, "she": true, "it": true,
	"we": true, "they": true, "me": true, "him": true, "her": true,
	"us": true, "them": true, "my": true, "your": true, "his": true,
	"its": true, "our": true, "their": true, "be": true, "been": true,
	"being": true, "have": true, "has": true, "had": true, "do": true,
	"does": true, "did": true, "will": true, "would": true, "shall": true,
	"should": true, "can": true, "could": true, "may": true, "might": true,
	"must": true, "am": true, "so": true, "than": true, "too": true,
	"very": true, "just": true, "about": true, "above": true, "after": true,
	"again": true, "all": true, "also": true, "any": true, "because": true,
	"before": true, "between": true, "both": true, "down": true, "during": true,
	"each": true, "few": true, "further": true, "here": true, "into": true,
	"more": true, "most": true, "other": true, "out": true, "over": true,
	"own": true, "same": true, "some": true, "such": true, "there": true,
	"through": true, "under": true, "until": true, "up": true, "while": true,
	"with": true, "from": true, "by": true, "as": true,
}

var chineseStopWords = map[string]bool{
	"的": true, "了": true, "在": true, "是": true, "我": true,
	"你": true, "他": true, "她": true, "它": true,
	"什么": true, "那个": true, "这个": true,
	"东西": true, "怎么": true, "如何": true, "为了": true,
	"和": true, "与": true, "或": true, "不": true, "没": true,
	"有": true, "就": true, "都": true, "而": true, "但": true,
	"也": true, "又": true, "还": true, "只": true, "很": true,
	"非常": true, "已经": true, "可以": true, "因为": true, "所以": true,
	"如果": true, "但是": true, "然后": true, "这样": true, "那样": true,
	"这里": true, "那里": true, "哪里": true, "时候": true, "地方": true,
	"一个": true, "一些": true, "一下": true, "一点": true, "不要": true,
	"就是": true, "还是": true, "或者": true, "以及": true, "等等": true,
	"我们": true, "你们": true, "他们": true, "为": true,
}

var multiCharChineseStopWords = []string{
	"什么", "那个", "这个", "东西", "怎么", "如何", "为了",
	"非常", "已经", "可以", "因为", "所以", "如果", "但是",
	"然后", "这样", "那样", "这里", "那里", "哪里", "时候",
	"地方", "一个", "一些", "一下", "一点", "不要", "就是",
	"还是", "或者", "以及", "等等", "我们", "你们", "他们",
}

func isStopWord(token string) bool {
	if englishStopWords[token] {
		return true
	}
	if chineseStopWords[token] {
		return true
	}
	return false
}

func removeChineseStopWords(text string) string {
	runes := []rune(text)
	for _, sw := range multiCharChineseStopWords {
		swRunes := []rune(sw)
		for {
			idx := indexOf(runes, swRunes)
			if idx < 0 {
				break
			}
			runes = append(runes[:idx], runes[idx+len(swRunes):]...)
		}
	}
	return string(runes)
}

func indexOf(text, sub []rune) int {
	if len(sub) == 0 || len(sub) > len(text) {
		return -1
	}
	for i := 0; i <= len(text)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if text[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func ExpandQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	tokens := memport.Tokenize(query)
	if len(tokens) == 0 {
		return query
	}

	var filtered []string
	for _, t := range tokens {
		if !isStopWord(strings.ToLower(t)) {
			filtered = append(filtered, t)
		}
	}

	result := strings.Join(filtered, " ")

	for _, sw := range multiCharChineseStopWords {
		if strings.Contains(result, sw) {
			result = removeChineseStopWords(result)
			break
		}
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return query
	}
	return result
}