// Package token splits normalized document bodies into the terms an index
// stores. Tokenization is deterministic: the same body and the same
// configuration always produce the same tokens, in the same order, with the
// same positions.
//
// A term is a maximal run of letters and digits, lower-cased. Runs of any
// other character separate terms and are never indexed. Terms shorter than
// the configured minimum length and terms on the stopword list are dropped,
// and the surviving tokens are numbered from zero without gaps, so a
// document's token count is always the number of tokens returned here.
package token

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"sift/internal/core"
)

// DefaultMinTermLength is the minimum term length used when a configuration
// does not set one. It matches the documented default of core.Config.
const DefaultMinTermLength = 2

// DefaultStopwords lists the terms dropped when a configuration names no
// stopwords of its own. The list is lower-case, unique and sorted ascending.
// Callers must treat it as read-only.
var DefaultStopwords = []string{
	"a", "about", "after", "all", "also", "an", "and", "any", "are", "as",
	"at", "be", "because", "been", "but", "by", "can", "did", "do", "does",
	"for", "from", "had", "has", "have", "he", "her", "here", "his", "how",
	"i", "if", "in", "into", "is", "it", "its", "may", "more", "most", "no",
	"not", "of", "on", "one", "only", "or", "other", "our", "out", "over",
	"said", "she", "so", "some", "such", "than", "that", "the", "their",
	"them", "then", "there", "these", "they", "this", "those", "through",
	"to", "too", "up", "use", "very", "was", "we", "were", "what", "when",
	"where", "which", "who", "will", "with", "would", "you", "your",
}

// Tokenize splits a normalized document body into tokens. Terms are
// lower-cased, split on runs of characters that are neither letters nor
// digits, and dropped when shorter than cfg.MinTermLength (measured in runes)
// or listed as a stopword. Positions are the zero-based ordinals of the
// returned tokens, so they stay contiguous even where terms were dropped.
// The result is never nil.
func Tokenize(body string, cfg core.Config) []core.Token {
	minLen := minTermLength(cfg)
	stop := stopwordSet(cfg)

	tokens := make([]core.Token, 0, len(body)/8+1)
	var term strings.Builder
	flush := func() {
		if term.Len() == 0 {
			return
		}
		t := term.String()
		term.Reset()
		if utf8.RuneCountInString(t) < minLen {
			return
		}
		if _, ok := stop[t]; ok {
			return
		}
		tokens = append(tokens, core.Token{Term: t, Position: len(tokens)})
	}
	for _, r := range body {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			term.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return tokens
}

// Terms returns the term of every token in token order, duplicates included.
// The result is never nil.
func Terms(tokens []core.Token) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, t.Term)
	}
	return out
}

// IsStopword reports whether a term is dropped by the configuration. The
// comparison is case-insensitive. A configuration with no stopwords of its own
// uses DefaultStopwords.
func IsStopword(cfg core.Config, term string) bool {
	t := normalize(term)
	if t == "" {
		return false
	}
	for _, w := range stopwordList(cfg) {
		if normalize(w) == t {
			return true
		}
	}
	return false
}

// minTermLength returns the effective minimum term length.
func minTermLength(cfg core.Config) int {
	if cfg.MinTermLength < 1 {
		return DefaultMinTermLength
	}
	return cfg.MinTermLength
}

// stopwordList returns the stopwords the configuration applies.
func stopwordList(cfg core.Config) []string {
	if len(cfg.Stopwords) == 0 {
		return DefaultStopwords
	}
	return cfg.Stopwords
}

// stopwordSet returns the configured stopwords as a normalized lookup set.
func stopwordSet(cfg core.Config) map[string]struct{} {
	list := stopwordList(cfg)
	set := make(map[string]struct{}, len(list))
	for _, w := range list {
		if n := normalize(w); n != "" {
			set[n] = struct{}{}
		}
	}
	return set
}

// normalize lower-cases and trims a term so lookups ignore case and padding.
func normalize(term string) string {
	return strings.ToLower(strings.TrimSpace(term))
}
