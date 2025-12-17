// Package query parses the small search language Sift accepts and turns it
// back into canonical text.
//
// A query is a whitespace-separated list of clauses. Each clause is one of
//
//	term              a bare term, matched against the tokenized body
//	"two words"       a phrase, matched as adjacent terms
//	field:value       an exact match against a document field
//	field:"two words" an exact match whose value contains spaces
//
// and any clause may be negated by a leading '-', which excludes the
// documents it matches. An empty query, or one that is only whitespace,
// carries no clauses and matches every document.
//
// Parsing is purely syntactic and total: every input either yields a Query or
// a *core.QueryError naming the byte offset that could not be read. Bare terms
// and phrase words are lower-cased so they line up with the tokenizer's
// output; the value of a field clause is kept exactly as written because
// field values are compared exactly. Both rules are idempotent, so
// Parse(q.String()) always reproduces q.
package query

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"sift/internal/core"
)

// Clause is one condition of a query.
type Clause struct {
	// Field names the document field to match exactly. It is empty for a
	// clause that searches the document body.
	Field string
	// Term is the single term to match. It is empty when Phrase is set.
	Term string
	// Phrase lists the words of a quoted phrase in order. It is nil when
	// Term is set.
	Phrase []string
	// Negate reports whether matching documents are excluded rather than
	// required.
	Negate bool
}

// Query is a parsed search expression.
type Query struct {
	// Clauses are the conditions in the order they were written.
	Clauses []Clause
	// MatchAll reports that the query carries no clauses and therefore
	// selects every document.
	MatchAll bool
}

// Parse reads a query string. An empty or whitespace-only string yields a
// Query with MatchAll set and no clauses. Any syntax problem returns a
// *core.QueryError, which matches core.ErrQuery, carrying the zero-based byte
// offset of the offending character.
func Parse(s string) (Query, error) {
	var q Query
	i, n := 0, len(s)
	for i < n {
		if r, size := utf8.DecodeRuneInString(s[i:]); unicode.IsSpace(r) {
			i += size
			continue
		}
		c, next, err := parseClause(s, i)
		if err != nil {
			return Query{}, err
		}
		q.Clauses = append(q.Clauses, c)
		i = next
	}
	if len(q.Clauses) == 0 {
		q.MatchAll = true
	}
	return q, nil
}

// parseClause reads one clause starting at the non-space byte offset start and
// returns it together with the offset just past it.
func parseClause(s string, start int) (Clause, int, error) {
	var c Clause
	n := len(s)
	i := start

	if r, size := utf8.DecodeRuneInString(s[i:]); r == '-' {
		c.Negate = true
		i += size
		if i >= n {
			return Clause{}, 0, &core.QueryError{Position: start, Reason: "missing term after '-'"}
		}
		if r, _ := utf8.DecodeRuneInString(s[i:]); unicode.IsSpace(r) {
			return Clause{}, 0, &core.QueryError{Position: start, Reason: "missing term after '-'"}
		}
	}

	// An unquoted run followed by ':' introduces a field clause. The value
	// after the colon may itself contain colons.
	j := i
	for j < n {
		r, size := utf8.DecodeRuneInString(s[j:])
		if unicode.IsSpace(r) || r == ':' || r == '"' {
			break
		}
		j += size
	}
	if j < n && s[j] == ':' {
		if j == i {
			return Clause{}, 0, &core.QueryError{Position: j, Reason: "missing field name before ':'"}
		}
		c.Field = strings.ToLower(s[i:j])
		i = j + 1
		if i >= n {
			return Clause{}, 0, &core.QueryError{Position: j, Reason: "missing value after ':'"}
		}
		if r, _ := utf8.DecodeRuneInString(s[i:]); unicode.IsSpace(r) {
			return Clause{}, 0, &core.QueryError{Position: j, Reason: "missing value after ':'"}
		}
	}

	if s[i] == '"' {
		words, next, err := parsePhrase(s, i)
		if err != nil {
			return Clause{}, 0, err
		}
		if c.Field == "" {
			for k, w := range words {
				words[k] = strings.ToLower(w)
			}
		}
		c.Phrase = words
		return c, next, nil
	}

	j = i
	for j < n {
		r, size := utf8.DecodeRuneInString(s[j:])
		if unicode.IsSpace(r) {
			break
		}
		if r == '"' {
			return Clause{}, 0, &core.QueryError{Position: j, Reason: "unexpected quote"}
		}
		j += size
	}
	c.Term = s[i:j]
	if c.Field == "" {
		c.Term = strings.ToLower(c.Term)
	}
	return c, j, nil
}

// parsePhrase reads a double-quoted phrase whose opening quote is at open and
// returns its words together with the offset just past the closing quote.
func parsePhrase(s string, open int) ([]string, int, error) {
	rest := s[open+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return nil, 0, &core.QueryError{Position: open, Reason: "unterminated quote"}
	}
	words := strings.Fields(rest[:end])
	if len(words) == 0 {
		return nil, 0, &core.QueryError{Position: open, Reason: "empty phrase"}
	}
	next := open + 1 + end + 1
	if next < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[next:]); !unicode.IsSpace(r) {
			return nil, 0, &core.QueryError{Position: next, Reason: "unexpected text after quote"}
		}
	}
	return words, next, nil
}

// String renders the query in canonical form: the clauses in order, separated
// by single spaces, phrases quoted and negations prefixed with '-'. A query
// with no clauses renders as the empty string. Parsing the result reproduces
// the query, so String round-trips through Parse.
func (q Query) String() string {
	parts := make([]string, 0, len(q.Clauses))
	for _, c := range q.Clauses {
		if c.Term == "" && len(c.Phrase) == 0 {
			// Not expressible in the language, and Parse never builds one.
			continue
		}
		var b strings.Builder
		if c.Negate {
			b.WriteByte('-')
		}
		if c.Field != "" {
			b.WriteString(c.Field)
			b.WriteByte(':')
		}
		if len(c.Phrase) > 0 {
			b.WriteByte('"')
			b.WriteString(strings.Join(c.Phrase, " "))
			b.WriteByte('"')
		} else {
			b.WriteString(c.Term)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, " ")
}
