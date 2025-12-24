// Package search answers queries against a loaded index.
//
// Run is the whole public surface: it parses the query text, gathers the
// documents that satisfy it, applies the caller filters, counts facets over
// every match, and only then orders and truncates the page of results.
// Counting before truncating is what lets a caller show "12 of 340 matches"
// beside facet counts that describe all 340.
package search

import (
	"fmt"
	"sort"
	"strings"

	"sift/internal/core"
	"sift/internal/facet"
	"sift/internal/index"
	"sift/internal/query"
	"sift/internal/rank"
)

// Run answers one query against idx.
//
// A query that cannot be parsed yields the zero report and an error wrapping
// core.ErrQuery. Otherwise a document matches when it satisfies every plain
// clause, matches no negated clause, and equals every entry of opts.Filters
// exactly. An empty query matches every document, and so does one carrying
// only negated clauses, which are then subtracted from the whole corpus.
//
// Report.Total and Report.Facets describe every match rather than the returned
// page: both are computed before opts.Limit applies. Results are ordered
// globally by the rank package (score descending, then frequency descending,
// then document id ascending) and truncated afterwards, so a limited page is
// always the head of the full ordering. A non-positive limit returns
// everything.
//
// Snippets are left empty: an index keeps document statistics rather than
// bodies, so there is no text to excerpt here.
func Run(idx *index.Index, opts core.SearchOptions) (core.SearchReport, error) {
	q, err := query.Parse(opts.Query)
	if err != nil {
		return core.SearchReport{}, fmt.Errorf("search %q: %w", opts.Query, err)
	}
	if idx == nil {
		idx = index.New()
	}

	hits := gather(idx, q)
	matched, infos := filter(idx, hits, opts.Filters)

	freqs := make(map[core.DocID]int, len(matched))
	for _, id := range matched {
		freqs[id] = hits[id]
	}
	scores := rank.Score(idx, q, freqs)

	results := make([]core.SearchResult, 0, len(matched))
	for _, id := range matched {
		info := infos[id]
		results = append(results, core.SearchResult{
			DocID:  id,
			Path:   string(id),
			Title:  info.Title,
			Score:  scores[id],
			Freq:   freqs[id],
			Fields: cloneFields(info.Fields),
		})
	}
	rank.Order(results)

	report := core.SearchReport{
		Options: opts,
		Total:   len(matched),
		Facets:  facet.Count(idx, matched, opts.Facets),
	}
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	report.Results = results
	return report, nil
}

// gather returns the documents matching q, mapped to the number of query term
// occurrences found in each. Plain clauses combine with AND so every extra
// clause narrows the result, negated clauses subtract, and a query with no
// plain clause at all starts from the whole corpus.
func gather(idx *index.Index, q query.Query) map[core.DocID]int {
	var plain, negated []query.Clause
	for _, c := range q.Clauses {
		switch {
		case empty(c):
			// A clause with neither field, term nor phrase constrains
			// nothing, so it neither narrows nor subtracts.
		case c.Negate:
			negated = append(negated, c)
		default:
			plain = append(plain, c)
		}
	}

	hits := make(map[core.DocID]int)
	if q.MatchAll || len(plain) == 0 {
		for _, info := range idx.Docs() {
			hits[info.ID] = 0
		}
	} else {
		for i, c := range plain {
			clause := match(idx, c)
			if i == 0 {
				hits = clause
				continue
			}
			hits = intersect(hits, clause)
			if len(hits) == 0 {
				break
			}
		}
	}

	for _, c := range negated {
		if len(hits) == 0 {
			break
		}
		for id := range match(idx, c) {
			delete(hits, id)
		}
	}
	return hits
}

// filter drops hits the index does not describe or that fail a filter, and
// returns the surviving ids in ascending order beside their documents.
func filter(idx *index.Index, hits map[core.DocID]int, filters map[string]string) ([]core.DocID, map[core.DocID]core.DocInfo) {
	matched := make([]core.DocID, 0, len(hits))
	infos := make(map[core.DocID]core.DocInfo, len(hits))
	for id := range hits {
		info, ok := idx.Doc(id)
		if !ok || !keep(info, filters) {
			continue
		}
		matched = append(matched, id)
		infos[id] = info
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i] < matched[j] })
	return matched, infos
}

// keep reports whether a document satisfies every filter exactly. A filter on
// a field the document lacks is compared against the empty string, so
// filtering on an empty value selects the documents missing that field.
func keep(info core.DocInfo, filters map[string]string) bool {
	for field, want := range filters {
		if info.Fields[field] != want {
			return false
		}
	}
	return true
}

// match returns the documents satisfying one clause, mapped to the number of
// term occurrences the clause found in each. A field clause contributes no
// occurrences because it constrains an attribute rather than the text.
func match(idx *index.Index, c query.Clause) map[core.DocID]int {
	switch {
	case c.Field != "":
		return matchField(idx, c)
	case len(c.Phrase) > 0:
		return matchPhrase(idx, c.Phrase)
	default:
		return matchTerm(idx, c.Term)
	}
}

// matchField returns the documents whose field equals the clause value. A
// quoted value is joined with single spaces, so kind:"go source" compares
// against that exact two-word value.
func matchField(idx *index.Index, c query.Clause) map[core.DocID]int {
	want := c.Term
	if len(c.Phrase) > 0 {
		want = strings.Join(c.Phrase, " ")
	}
	out := make(map[core.DocID]int)
	for _, info := range idx.Docs() {
		if info.Fields[c.Field] == want {
			out[info.ID] = 0
		}
	}
	return out
}

// matchTerm returns the documents containing a term, mapped to its frequency.
// The term is lower-cased to match the tokenizer, which indexes lower-case
// terms only.
func matchTerm(idx *index.Index, term string) map[core.DocID]int {
	out := make(map[core.DocID]int)
	term = normalize(term)
	if term == "" {
		return out
	}
	for _, p := range idx.Postings(term) {
		out[p.DocID] = p.Freq
	}
	return out
}

// matchPhrase returns the documents holding the words at consecutive token
// positions, mapped to the number of times the phrase occurs. Positions come
// from the token stream, so terms the tokenizer dropped do not break a phrase.
func matchPhrase(idx *index.Index, phrase []string) map[core.DocID]int {
	words := make([]string, 0, len(phrase))
	for _, w := range phrase {
		if w = normalize(w); w != "" {
			words = append(words, w)
		}
	}
	switch len(words) {
	case 0:
		return make(map[core.DocID]int)
	case 1:
		return matchTerm(idx, words[0])
	}

	out := make(map[core.DocID]int)
	heads := idx.Postings(words[0])
	if len(heads) == 0 {
		return out
	}
	// rest[i] holds, per document, the positions of words[i+1].
	rest := make([]map[core.DocID]map[int]bool, len(words)-1)
	for i, w := range words[1:] {
		byDoc := make(map[core.DocID]map[int]bool)
		for _, p := range idx.Postings(w) {
			positions := make(map[int]bool, len(p.Positions))
			for _, pos := range p.Positions {
				positions[pos] = true
			}
			byDoc[p.DocID] = positions
		}
		if len(byDoc) == 0 {
			return out
		}
		rest[i] = byDoc
	}

	for _, head := range heads {
		found := 0
		for _, start := range head.Positions {
			complete := true
			for i := range rest {
				if !rest[i][head.DocID][start+i+1] {
					complete = false
					break
				}
			}
			if complete {
				found++
			}
		}
		if found > 0 {
			out[head.DocID] = found
		}
	}
	return out
}

// intersect keeps the documents present in both sets and adds their occurrence
// counts, so a result frequency covers every clause that matched it.
func intersect(a, b map[core.DocID]int) map[core.DocID]int {
	small, large := a, b
	if len(large) < len(small) {
		small, large = large, small
	}
	out := make(map[core.DocID]int, len(small))
	for id, freq := range small {
		if other, ok := large[id]; ok {
			out[id] = freq + other
		}
	}
	return out
}

// empty reports whether a clause carries no constraint at all, as an empty
// pair of quotes does.
func empty(c query.Clause) bool {
	if c.Field != "" || normalize(c.Term) != "" {
		return false
	}
	for _, w := range c.Phrase {
		if normalize(w) != "" {
			return false
		}
	}
	return true
}

// normalize puts query text into the form the index stores.
func normalize(term string) string {
	return strings.ToLower(strings.TrimSpace(term))
}

// cloneFields copies document fields so a caller cannot reach back into the
// index through a returned result.
func cloneFields(fields map[string]string) map[string]string {
	if fields == nil {
		return nil
	}
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}
