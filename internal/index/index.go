// Package index accumulates documents and their tokens into an in-memory
// inverted index and answers the questions ranking, faceting and reporting ask
// of it: which documents exist, which terms exist, and where a term occurs.
//
// Every accessor returns freshly built, sorted data, so an index never leaks
// its internal maps and map iteration order never reaches a caller. Term
// statistics are derived from the stored postings on every read rather than
// accumulated, which is what keeps replacement and Merge honest: a document
// added twice, or merged from two indexes, is counted once.
package index

import (
	"sort"

	"sift/internal/core"
)

// Index is an in-memory inverted index over a set of documents. The zero value
// is not usable; call New. An Index is not safe for concurrent use.
type Index struct {
	// docs holds the per-document data, keyed by DocID.
	docs map[core.DocID]core.DocInfo
	// docTerms lists, per document, the terms it contributed, sorted
	// ascending, so a document can be removed without scanning every term.
	docTerms map[core.DocID][]string
	// postings maps a term to its posting for each document containing it.
	postings map[string]map[core.DocID]core.Posting
	// totalLength is the sum of the document lengths, for AvgDocLength.
	totalLength int
}

// New returns an empty index.
func New() *Index {
	return &Index{
		docs:     make(map[core.DocID]core.DocInfo),
		docTerms: make(map[core.DocID][]string),
		postings: make(map[string]map[core.DocID]core.Posting),
	}
}

// Add indexes one document and its tokens. The document length is the number
// of tokens supplied. Adding a document whose ID is already present replaces it
// completely: the previous postings are removed first, so no term keeps a stale
// occurrence.
func (ix *Index) Add(doc core.Document, tokens []core.Token) {
	info := core.DocInfo{
		ID:          doc.ID,
		Length:      len(tokens),
		Fields:      copyFields(doc.Fields),
		Title:       doc.Title,
		ContentHash: doc.ContentHash,
	}
	positions := make(map[string][]int)
	for _, t := range tokens {
		if t.Term == "" {
			continue
		}
		positions[t.Term] = append(positions[t.Term], t.Position)
	}
	ix.put(info, positions)
}

// AddPostings indexes a document that was tokenized earlier, for example one
// read back from a segment file. The postings are keyed by term and each
// posting supplies that document's positions; the DocID recorded inside a
// posting is ignored in favour of info.ID. When info.Length is not positive it
// is derived from the postings. A document already present under the same ID is
// replaced.
func (ix *Index) AddPostings(info core.DocInfo, postings map[string]core.Posting) {
	info.Fields = copyFields(info.Fields)
	positions := make(map[string][]int, len(postings))
	total := 0
	for term, p := range postings {
		if term == "" || len(p.Positions) == 0 {
			continue
		}
		positions[term] = p.Positions
		total += len(p.Positions)
	}
	if info.Length < 1 {
		info.Length = total
	}
	ix.put(info, positions)
}

// Docs returns every document, sorted by DocID ascending.
func (ix *Index) Docs() []core.DocInfo {
	out := make([]core.DocInfo, 0, len(ix.docs))
	for _, info := range ix.docs {
		info.Fields = copyFields(info.Fields)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Terms returns the statistics of every indexed term, sorted by term ascending.
// The statistics are computed from the current postings.
func (ix *Index) Terms() []core.TermStats {
	out := make([]core.TermStats, 0, len(ix.postings))
	for term, byDoc := range ix.postings {
		stats := core.TermStats{Term: term, DocFreq: len(byDoc)}
		for _, p := range byDoc {
			stats.TotalFreq += p.Freq
		}
		out = append(out, stats)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Term < out[j].Term })
	return out
}

// Postings returns the postings of one term, sorted by DocID ascending. An
// unknown term yields an empty slice, never nil.
func (ix *Index) Postings(term string) []core.Posting {
	byDoc := ix.postings[term]
	out := make([]core.Posting, 0, len(byDoc))
	for _, p := range byDoc {
		p.Positions = copyInts(p.Positions)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DocID < out[j].DocID })
	return out
}

// DocCount returns the number of indexed documents.
func (ix *Index) DocCount() int { return len(ix.docs) }

// TermCount returns the number of distinct indexed terms.
func (ix *Index) TermCount() int { return len(ix.postings) }

// AvgDocLength returns the mean document length in tokens, or zero when the
// index is empty.
func (ix *Index) AvgDocLength() float64 {
	if len(ix.docs) == 0 {
		return 0
	}
	return float64(ix.totalLength) / float64(len(ix.docs))
}

// Doc returns the data held for one document and reports whether it exists.
func (ix *Index) Doc(id core.DocID) (core.DocInfo, bool) {
	info, ok := ix.docs[id]
	if !ok {
		return core.DocInfo{}, false
	}
	info.Fields = copyFields(info.Fields)
	return info, true
}

// Merge combines indexes into a new index, leaving every input untouched. A
// document present in more than one index is taken from the last index that
// holds it, so later indexes win. Term statistics are recomputed from the
// merged postings and are never summed across inputs. Nil inputs are skipped.
func Merge(indexes ...*Index) *Index {
	out := New()
	for _, ix := range indexes {
		if ix == nil {
			continue
		}
		for _, id := range ix.sortedDocIDs() {
			info := ix.docs[id]
			info.Fields = copyFields(info.Fields)
			positions := make(map[string][]int, len(ix.docTerms[id]))
			for _, term := range ix.docTerms[id] {
				positions[term] = ix.postings[term][id].Positions
			}
			out.put(info, positions)
		}
	}
	return out
}

// put replaces any document with the same ID and stores the given positions.
// The positions are copied and sorted, so callers may reuse their slices.
func (ix *Index) put(info core.DocInfo, positions map[string][]int) {
	ix.remove(info.ID)
	terms := make([]string, 0, len(positions))
	for term, pos := range positions {
		if term == "" || len(pos) == 0 {
			continue
		}
		p := copyInts(pos)
		sort.Ints(p)
		byDoc := ix.postings[term]
		if byDoc == nil {
			byDoc = make(map[core.DocID]core.Posting)
			ix.postings[term] = byDoc
		}
		byDoc[info.ID] = core.Posting{DocID: info.ID, Freq: len(p), Positions: p}
		terms = append(terms, term)
	}
	sort.Strings(terms)
	ix.docs[info.ID] = info
	ix.docTerms[info.ID] = terms
	ix.totalLength += info.Length
}

// remove drops a document and every posting it contributed.
func (ix *Index) remove(id core.DocID) {
	info, ok := ix.docs[id]
	if !ok {
		return
	}
	for _, term := range ix.docTerms[id] {
		byDoc := ix.postings[term]
		delete(byDoc, id)
		if len(byDoc) == 0 {
			delete(ix.postings, term)
		}
	}
	delete(ix.docTerms, id)
	delete(ix.docs, id)
	ix.totalLength -= info.Length
}

// sortedDocIDs returns the document IDs in ascending order.
func (ix *Index) sortedDocIDs() []core.DocID {
	ids := make([]core.DocID, 0, len(ix.docs))
	for id := range ix.docs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// copyFields copies a field map, preserving a nil map as nil.
func copyFields(fields map[string]string) map[string]string {
	if fields == nil {
		return nil
	}
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

// copyInts copies a position slice.
func copyInts(in []int) []int {
	out := make([]int, len(in))
	copy(out, in)
	return out
}
