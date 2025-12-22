// Package facet counts the field values of a set of documents.
//
// Faceting is deliberately separate from ranking and from paging: a facet
// describes the whole set of documents handed to it, so a caller that later
// truncates its result page still reports counts covering every match.
package facet

import (
	"sort"

	"sift/internal/core"
	"sift/internal/index"
)

// Count counts the values of each named field across the given documents.
//
// The result holds one core.Facet per distinct field, keyed by field name.
// Fields are visited in ascending order and a repeated field name is counted
// once, so the counts do not depend on the order the caller listed them in. A
// named field with nothing to count is still present in the result, with an
// empty count map.
//
// Every element of ids is counted, so a repeated id contributes more than
// once; callers that count query matches pass each document once. A document
// that carries no value for a field, and an id the index does not know, are
// both counted under the empty string.
func Count(idx *index.Index, ids []core.DocID, fields []string) map[string]core.Facet {
	names := sortedUnique(fields)
	out := make(map[string]core.Facet, len(names))
	for _, name := range names {
		out[name] = core.Facet{Field: name, Counts: make(map[string]int)}
	}
	if len(names) == 0 {
		return out
	}
	for _, id := range ids {
		info, known := lookup(idx, id)
		for _, name := range names {
			value := ""
			if known {
				value = info.Fields[name]
			}
			counts := out[name].Counts
			counts[value]++
		}
	}
	return out
}

// lookup returns the document info for id, reporting whether the index knows
// it. A nil index knows nothing, which keeps Count usable before an index has
// been loaded.
func lookup(idx *index.Index, id core.DocID) (core.DocInfo, bool) {
	if idx == nil {
		return core.DocInfo{}, false
	}
	return idx.Doc(id)
}

// sortedUnique returns the field names in ascending order without duplicates.
func sortedUnique(fields []string) []string {
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
