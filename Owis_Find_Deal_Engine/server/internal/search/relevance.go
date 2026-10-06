package search

import (
	"strings"
	"unicode"
)

// Relevant reports whether a web search result is about the query: at
// least half of the query's words (two characters or more) must start a
// word in texts (title, snippet, link path), so "enclosure" matches
// "enclosures". Search engines asked for "x site:shop.com" sometimes ignore
// x and return any page of the shop; this drops those. A query with no
// usable words matches everything.
func Relevant(query string, texts ...string) bool {
	want := words(query)
	if len(want) == 0 {
		return true
	}
	have := words(strings.Join(texts, " "))
	found := 0
	for _, w := range want {
		for _, h := range have {
			if strings.HasPrefix(h, w) {
				found++
				break
			}
		}
	}
	return found*2 >= len(want)
}

// words lowercases s and splits it into words of letters and digits,
// dropping one-character words ("s pen" -> ["pen"]).
func words(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len([]rune(f)) >= 2 {
			out = append(out, f)
		}
	}
	return out
}
