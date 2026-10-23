package sqlstick

import (
	"strings"
	"unicode"
)

// writeKeywords start a query that writes.
var writeKeywords = []string{"INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE"}

// startsWithWriteKeyword reports whether the leading keyword of query is
// one that writes.
//
//declscope:shared // wrap.go guesses the writes of a query with it
func startsWithWriteKeyword(query string) bool {
	keyword := leadingKeyword(query)
	for _, kw := range writeKeywords {
		if strings.EqualFold(keyword, kw) {
			return true
		}
	}
	return false
}

// leadingKeyword returns the first word of query, after spaces, comments
// and opening parentheses. An unclosed block comment leaves none.
func leadingKeyword(query string) string {
	for {
		query = strings.TrimLeftFunc(query, func(r rune) bool {
			return unicode.IsSpace(r) || r == '('
		})
		switch {
		case strings.HasPrefix(query, "--"), strings.HasPrefix(query, "#"):
			end := strings.IndexByte(query, '\n')
			if end < 0 {
				return ""
			}
			query = query[end+1:]
		case strings.HasPrefix(query, "/*"):
			end := strings.Index(query, "*/")
			if end < 0 {
				return ""
			}
			query = query[end+2:]
		default:
			end := strings.IndexFunc(query, func(r rune) bool {
				return !unicode.IsLetter(r)
			})
			if end < 0 {
				return query
			}
			return query[:end]
		}
	}
}
