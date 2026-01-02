// Package stats summarizes a published index. It answers the questions an
// operator asks about a corpus once it is indexed: how many documents, terms
// and tokens it holds, how those documents break down by kind and by source
// language, and which documents are the longest.
//
// Every result is derived from the index alone, so Compute is pure and
// deterministic: the same index always yields the same Corpus, including the
// order of LargestDocs.
package stats

import (
	"sort"

	"sift/internal/core"
	"sift/internal/index"
)

// MaxLargestDocs is the number of documents Compute keeps in Corpus.LargestDocs.
const MaxLargestDocs = 10

// Corpus is the statistical summary of one index.
type Corpus struct {
	// Documents is the number of indexed documents.
	Documents int
	// Terms is the number of distinct terms across the whole index.
	Terms int
	// Tokens is the total number of token occurrences, that is the sum of
	// every document length.
	Tokens int
	// ByKind counts documents per value of the "kind" field. Every document
	// is counted exactly once; a document without a kind is counted under
	// the empty string.
	ByKind map[string]int
	// ByLanguage counts documents per value of the "language" field. Every
	// document is counted exactly once; a document without a language is
	// counted under the empty string.
	ByLanguage map[string]int
	// LargestDocs holds the longest documents, at most MaxLargestDocs of
	// them, ordered by Length descending and then by ID ascending.
	LargestDocs []core.DocInfo
}

// Compute summarizes idx. The returned maps are always non-nil, and
// LargestDocs is always non-nil, so callers can range over the result of an
// empty or nil index without a guard.
func Compute(idx *index.Index) Corpus {
	c := Corpus{
		ByKind:      make(map[string]int),
		ByLanguage:  make(map[string]int),
		LargestDocs: []core.DocInfo{},
	}
	if idx == nil {
		return c
	}

	docs := idx.Docs()
	c.Documents = idx.DocCount()
	c.Terms = idx.TermCount()
	for _, d := range docs {
		c.Tokens += d.Length
		c.ByKind[d.Fields["kind"]]++
		c.ByLanguage[d.Fields["language"]]++
	}

	largest := make([]core.DocInfo, len(docs))
	copy(largest, docs)
	sort.Slice(largest, func(i, j int) bool {
		if largest[i].Length != largest[j].Length {
			return largest[i].Length > largest[j].Length
		}
		return largest[i].ID < largest[j].ID
	})
	if len(largest) > MaxLargestDocs {
		largest = largest[:MaxLargestDocs]
	}
	c.LargestDocs = largest
	return c
}
