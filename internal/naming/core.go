package naming

import (
	"strings"
	"unicode"
)

// This source also supplies the generated runtime helpers, so compile-time
// wire names and runtime Words/JoinWords use exactly the same implementation.
func _borkCodecWords(name string) []string {
	runes := []rune(name)
	words := []string{}
	start := 0
	appendWord := func(end int) {
		if start < end {
			words = append(words, strings.ToLower(string(runes[start:end])))
		}
	}
	for i, r := range runes {
		if r == '_' || r == '-' || unicode.IsSpace(r) {
			appendWord(i)
			start = i + 1
			continue
		}
		if i > start && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			appendWord(i)
			start = i
		}
	}
	appendWord(len(runes))
	return words
}

func _borkCodecJoinWords(words []string, policy string) string {
	out := append([]string(nil), words...)
	separator := ""
	for i, word := range out {
		word = strings.ToLower(word)
		switch policy {
		case "Pascal", "Camel":
			if policy == "Pascal" || i > 0 {
				runes := []rune(word)
				if len(runes) > 0 {
					runes[0] = unicode.ToUpper(runes[0])
					word = string(runes)
				}
			}
		case "ScreamingSnake":
			word = strings.ToUpper(word)
		}
		out[i] = word
	}
	switch policy {
	case "Snake", "ScreamingSnake":
		separator = "_"
	case "Kebab":
		separator = "-"
	}
	return strings.Join(out, separator)
}
