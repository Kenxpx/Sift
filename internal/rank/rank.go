// Package rank scores the documents a query matched and puts search results
// into their canonical order.
//
// Scoring is BM25-lite: the Okapi BM25 term weight with k1 = 1.2 and b = 0.75,
// computed from the statistics the index already keeps, so no extra data has to
// be stored alongside a generation. Each positive body clause of the query
// contributes
//
//	idf(t) * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * dl / avgdl))
//
// with idf(t) = ln(1 + (N - df + 0.5) / (df + 0.5)), which is always positive,
// N the number of indexed documents, df the number of documents holding the
// term, tf the term frequency in the document, dl the document length in tokens
// and avgdl the mean document length. Rare terms therefore outweigh common
// ones, and a hit in a short document outweighs the same hit in a long one.
//
// Negated clauses and field clauses select documents but never score them:
// selection is the caller's job, so a query made only of those leaves every
// candidate at zero and the ordering falls through to the tie-breakers. Scoring
// visits clauses, then terms, then postings in a fixed order, so a given index
// and query always produce bit-identical scores.
package rank

import (
	"math"
	"sort"

	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/query"
)

// BM25 free parameters. k1 controls how quickly term frequency saturates and b
// controls how strongly document length is normalized.
const (
	k1 = 1.2
	b  = 0.75
)

// Score assigns a relevance score to every document in hits, which maps a
// candidate document to the number of query-term occurrences it holds. The
// returned map has exactly the keys of hits: a candidate no positive body
// clause reaches scores zero rather than disappearing. Documents outside hits
// are never scored, so the caller stays in control of which documents match.
// A nil index or an empty hit set yields a map of zeros.
func Score(idx *index.Index, q query.Query, hits map[core.DocID]int) map[core.DocID]float64 {
	scores := make(map[core.DocID]float64, len(hits))
	for id := range hits {
		scores[id] = 0
	}
	if idx == nil || len(hits) == 0 {
		return scores
	}
	n := float64(idx.DocCount())
	if n <= 0 {
		return scores
	}
	avg := idx.AvgDocLength()
	if avg <= 0 {
		avg = 1
	}
	for _, c := range q.Clauses {
		if c.Negate || c.Field != "" {
			continue
		}
		for _, term := range clauseTerms(c) {
			postings := idx.Postings(term)
			df := float64(len(postings))
			if df == 0 {
				continue
			}
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			for _, p := range postings {
				if _, ok := hits[p.DocID]; !ok {
					continue
				}
				dl := avg
				if info, ok := idx.Doc(p.DocID); ok && info.Length > 0 {
					dl = float64(info.Length)
				}
				tf := float64(p.Freq)
				scores[p.DocID] += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*dl/avg))
			}
		}
	}
	return scores
}

// clauseTerms returns the index terms a clause weighs. A phrase is weighed by
// its words, since the index scores terms and the caller has already checked
// that they occur adjacently.
func clauseTerms(c query.Clause) []string {
	if len(c.Phrase) > 0 {
		return c.Phrase
	}
	if c.Term == "" {
		return nil
	}
	return []string{c.Term}
}

// Order sorts results in place into the canonical result order: Score
// descending, then Freq descending, then DocID ascending. The last key makes
// the order total, so equally relevant documents never change places between
// runs.
func Order(results []core.SearchResult) {
	sort.SliceStable(results, func(i, j int) bool {
		left, right := &results[i], &results[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.Freq != right.Freq {
			return left.Freq > right.Freq
		}
		return left.DocID < right.DocID
	})
}
