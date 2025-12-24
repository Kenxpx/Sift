package search

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"sift/internal/core"
	"sift/internal/index"
)

// corpus builds a small index with known token positions, so every expected
// frequency and phrase match in these tests can be read off the token lists.
//
//	docs/alpha.md  alpha beta alpha gamma
//	docs/beta.md   beta gamma beta
//	notes.txt      beta alpha
//	src/alpha.go   alpha beta
//	src/gamma.go   gamma gamma gamma
func corpus() *index.Index {
	idx := index.New()
	add := func(id, title string, fields map[string]string, terms ...string) {
		tokens := make([]core.Token, len(terms))
		for i, term := range terms {
			tokens[i] = core.Token{Term: term, Position: i}
		}
		idx.Add(core.Document{
			ID:     core.DocID(id),
			Path:   id,
			Title:  title,
			Kind:   fields["kind"],
			Fields: fields,
		}, tokens)
	}
	add("docs/alpha.md", "Alpha", map[string]string{"kind": "markdown", "dir": "docs"},
		"alpha", "beta", "alpha", "gamma")
	add("docs/beta.md", "Beta", map[string]string{"kind": "markdown", "dir": "docs"},
		"beta", "gamma", "beta")
	add("notes.txt", "Notes", map[string]string{"kind": "text", "dir": "."},
		"beta", "alpha")
	add("src/alpha.go", "Alpha source", map[string]string{"kind": "source", "language": "go", "dir": "src"},
		"alpha", "beta")
	add("src/gamma.go", "Gamma source", map[string]string{"kind": "source", "language": "go", "dir": "src"},
		"gamma", "gamma", "gamma")
	return idx
}

// sortedIDs returns the result ids in ascending order, for comparing the set
// of matches independently of the ranking.
func sortedIDs(results []core.SearchResult) []core.DocID {
	out := make([]core.DocID, len(results))
	for i, r := range results {
		out[i] = r.DocID
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// freqs maps each result to its reported frequency.
func freqs(results []core.SearchResult) map[core.DocID]int {
	out := make(map[core.DocID]int, len(results))
	for _, r := range results {
		out[r.DocID] = r.Freq
	}
	return out
}

// checkOrder asserts the global ordering contract: score descending, then
// frequency descending, then document id ascending.
func checkOrder(t *testing.T, results []core.SearchResult) {
	t.Helper()
	for i := 1; i < len(results); i++ {
		prev, cur := results[i-1], results[i]
		switch {
		case prev.Score > cur.Score:
		case prev.Score < cur.Score:
			t.Fatalf("result %d scores %v, above the preceding %v", i, cur.Score, prev.Score)
		case prev.Freq > cur.Freq:
		case prev.Freq < cur.Freq:
			t.Fatalf("result %d has freq %d, above the preceding %d at an equal score", i, cur.Freq, prev.Freq)
		case prev.DocID >= cur.DocID:
			t.Fatalf("results %d and %d tie but are ordered %q then %q", i-1, i, prev.DocID, cur.DocID)
		}
	}
}

func TestRunMatching(t *testing.T) {
	idx := corpus()
	tests := []struct {
		name  string
		query string
		want  []core.DocID
		freqs map[core.DocID]int
	}{
		{
			name:  "empty query matches every document",
			query: "",
			want:  []core.DocID{"docs/alpha.md", "docs/beta.md", "notes.txt", "src/alpha.go", "src/gamma.go"},
			freqs: map[core.DocID]int{"docs/alpha.md": 0, "docs/beta.md": 0, "notes.txt": 0, "src/alpha.go": 0, "src/gamma.go": 0},
		},
		{
			name:  "single term reports its frequency",
			query: "alpha",
			want:  []core.DocID{"docs/alpha.md", "notes.txt", "src/alpha.go"},
			freqs: map[core.DocID]int{"docs/alpha.md": 2, "notes.txt": 1, "src/alpha.go": 1},
		},
		{
			name:  "terms combine with and and sum their frequencies",
			query: "alpha beta",
			want:  []core.DocID{"docs/alpha.md", "notes.txt", "src/alpha.go"},
			freqs: map[core.DocID]int{"docs/alpha.md": 3, "notes.txt": 2, "src/alpha.go": 2},
		},
		{
			name:  "a term nothing contains matches nothing",
			query: "delta",
			want:  []core.DocID{},
			freqs: map[core.DocID]int{},
		},
		{
			name:  "upper case query still matches the lower case index",
			query: "ALPHA",
			want:  []core.DocID{"docs/alpha.md", "notes.txt", "src/alpha.go"},
			freqs: map[core.DocID]int{"docs/alpha.md": 2, "notes.txt": 1, "src/alpha.go": 1},
		},
		{
			name:  "negation subtracts from the other clauses",
			query: "beta -gamma",
			want:  []core.DocID{"notes.txt", "src/alpha.go"},
			freqs: map[core.DocID]int{"notes.txt": 1, "src/alpha.go": 1},
		},
		{
			name:  "a query of only negations starts from the corpus",
			query: "-alpha",
			want:  []core.DocID{"docs/beta.md", "src/gamma.go"},
			freqs: map[core.DocID]int{"docs/beta.md": 0, "src/gamma.go": 0},
		},
		{
			name:  "field clause matches an attribute exactly",
			query: "kind:markdown",
			want:  []core.DocID{"docs/alpha.md", "docs/beta.md"},
			freqs: map[core.DocID]int{"docs/alpha.md": 0, "docs/beta.md": 0},
		},
		{
			name:  "field clause narrows a term clause",
			query: "kind:markdown alpha",
			want:  []core.DocID{"docs/alpha.md"},
			freqs: map[core.DocID]int{"docs/alpha.md": 2},
		},
		{
			name:  "field clause with no matching value matches nothing",
			query: "language:rust",
			want:  []core.DocID{},
			freqs: map[core.DocID]int{},
		},
		{
			name:  "phrase needs consecutive positions",
			query: `"alpha beta"`,
			want:  []core.DocID{"docs/alpha.md", "src/alpha.go"},
			freqs: map[core.DocID]int{"docs/alpha.md": 1, "src/alpha.go": 1},
		},
		{
			// The same two words in the other order select a different set:
			// docs/alpha.md holds both orders, src/alpha.go only the first.
			name:  "phrase order decides which documents match",
			query: `"beta alpha"`,
			want:  []core.DocID{"docs/alpha.md", "notes.txt"},
			freqs: map[core.DocID]int{"docs/alpha.md": 1, "notes.txt": 1},
		},
		{
			name:  "phrase whose words never adjoin matches nothing",
			query: `"gamma alpha"`,
			want:  []core.DocID{},
			freqs: map[core.DocID]int{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Run(idx, core.SearchOptions{Query: tc.query})
			if err != nil {
				t.Fatalf("Run(%q) failed: %v", tc.query, err)
			}
			if got.Total != len(tc.want) {
				t.Errorf("Total = %d, want %d", got.Total, len(tc.want))
			}
			if ids := sortedIDs(got.Results); !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("matches = %v, want %v", ids, tc.want)
			}
			if f := freqs(got.Results); !reflect.DeepEqual(f, tc.freqs) {
				t.Errorf("frequencies = %v, want %v", f, tc.freqs)
			}
			checkOrder(t, got.Results)
		})
	}
}

func TestRunAppliesFilters(t *testing.T) {
	idx := corpus()
	tests := []struct {
		name    string
		query   string
		filters map[string]string
		want    []core.DocID
	}{
		{
			name:    "filter alone",
			filters: map[string]string{"language": "go"},
			want:    []core.DocID{"src/alpha.go", "src/gamma.go"},
		},
		{
			name:    "filter narrows a query",
			query:   "alpha",
			filters: map[string]string{"language": "go"},
			want:    []core.DocID{"src/alpha.go"},
		},
		{
			name:    "filters combine",
			filters: map[string]string{"kind": "markdown", "dir": "docs"},
			want:    []core.DocID{"docs/alpha.md", "docs/beta.md"},
		},
		{
			name:    "contradictory filters match nothing",
			filters: map[string]string{"kind": "markdown", "dir": "src"},
			want:    []core.DocID{},
		},
		{
			name:    "an empty value selects documents missing the field",
			filters: map[string]string{"language": ""},
			want:    []core.DocID{"docs/alpha.md", "docs/beta.md", "notes.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Run(idx, core.SearchOptions{Query: tc.query, Filters: tc.filters})
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if got.Total != len(tc.want) {
				t.Errorf("Total = %d, want %d", got.Total, len(tc.want))
			}
			if ids := sortedIDs(got.Results); !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("matches = %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestRunCountsFacetsOverEveryMatchNotThePage(t *testing.T) {
	got, err := Run(corpus(), core.SearchOptions{Limit: 2, Facets: []string{"kind", "language"}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got.Total != 5 {
		t.Fatalf("Total = %d, want 5", got.Total)
	}
	if len(got.Results) != 2 {
		t.Fatalf("returned %d results, want the 2 the limit allows", len(got.Results))
	}
	wantKind := map[string]int{"markdown": 2, "source": 2, "text": 1}
	if !reflect.DeepEqual(got.Facets["kind"].Counts, wantKind) {
		t.Errorf("kind facet = %v, want %v", got.Facets["kind"].Counts, wantKind)
	}
	wantLanguage := map[string]int{"": 3, "go": 2}
	if !reflect.DeepEqual(got.Facets["language"].Counts, wantLanguage) {
		t.Errorf("language facet = %v, want %v", got.Facets["language"].Counts, wantLanguage)
	}
}

func TestRunFacetsCoverFilteredMatchesOnly(t *testing.T) {
	got, err := Run(corpus(), core.SearchOptions{
		Query:   "alpha",
		Filters: map[string]string{"kind": "markdown"},
		Facets:  []string{"dir"},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if got.Total != 1 {
		t.Fatalf("Total = %d, want 1", got.Total)
	}
	want := map[string]int{"docs": 1}
	if !reflect.DeepEqual(got.Facets["dir"].Counts, want) {
		t.Fatalf("dir facet = %v, want %v", got.Facets["dir"].Counts, want)
	}
}

func TestRunLimitKeepsTheHeadOfTheOrdering(t *testing.T) {
	idx := corpus()
	full, err := Run(idx, core.SearchOptions{Query: "beta"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(full.Results) != 4 {
		t.Fatalf("unlimited search returned %d results, want 4", len(full.Results))
	}
	checkOrder(t, full.Results)

	for _, limit := range []int{1, 2, 3, 4, 9} {
		page, err := Run(idx, core.SearchOptions{Query: "beta", Limit: limit})
		if err != nil {
			t.Fatalf("Run with limit %d failed: %v", limit, err)
		}
		if page.Total != full.Total {
			t.Errorf("limit %d changed Total to %d, want %d", limit, page.Total, full.Total)
		}
		want := limit
		if want > len(full.Results) {
			want = len(full.Results)
		}
		if len(page.Results) != want {
			t.Fatalf("limit %d returned %d results, want %d", limit, len(page.Results), want)
		}
		for i, r := range page.Results {
			if r.DocID != full.Results[i].DocID {
				t.Fatalf("limit %d result %d is %q, want %q from the full ordering", limit, i, r.DocID, full.Results[i].DocID)
			}
		}
	}
}

func TestRunNonPositiveLimitReturnsEverything(t *testing.T) {
	for _, limit := range []int{0, -1} {
		got, err := Run(corpus(), core.SearchOptions{Limit: limit})
		if err != nil {
			t.Fatalf("Run with limit %d failed: %v", limit, err)
		}
		if len(got.Results) != 5 {
			t.Errorf("limit %d returned %d results, want all 5", limit, len(got.Results))
		}
	}
}

func TestRunRejectsUnparsableQuery(t *testing.T) {
	got, err := Run(corpus(), core.SearchOptions{Query: `alpha "beta`})
	if err == nil {
		t.Fatalf("unterminated quote accepted, report %+v", got)
	}
	if !errors.Is(err, core.ErrQuery) {
		t.Fatalf("error %v does not match core.ErrQuery", err)
	}
	if got.Total != 0 || got.Results != nil || got.Facets != nil {
		t.Fatalf("failed search returned %+v, want the zero report", got)
	}
}

func TestRunReportsOptionsAndDocumentDetail(t *testing.T) {
	opts := core.SearchOptions{Query: "gamma", Limit: 1, Facets: []string{"kind"}}
	got, err := Run(corpus(), opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !reflect.DeepEqual(got.Options, opts) {
		t.Errorf("Options = %+v, want %+v", got.Options, opts)
	}
	if got.Total != 3 {
		t.Fatalf("Total = %d, want 3", got.Total)
	}
	r := got.Results[0]
	if r.Path != string(r.DocID) {
		t.Errorf("Path = %q, want %q", r.Path, string(r.DocID))
	}
	if r.Title == "" {
		t.Errorf("result %q has no title", r.DocID)
	}
	if r.Snippet != "" {
		t.Errorf("Snippet = %q, want empty: an index holds no bodies", r.Snippet)
	}
	if r.Fields["kind"] == "" {
		t.Errorf("result fields %v carry no kind", r.Fields)
	}
}

func TestRunResultFieldsDoNotAliasTheIndex(t *testing.T) {
	idx := corpus()
	first, err := Run(idx, core.SearchOptions{Query: "kind:text"})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(first.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(first.Results))
	}
	first.Results[0].Fields["kind"] = "tampered"

	second, err := Run(idx, core.SearchOptions{Query: "kind:text"})
	if err != nil {
		t.Fatalf("second Run failed: %v", err)
	}
	if len(second.Results) != 1 {
		t.Fatalf("got %d results after mutating the first, want 1", len(second.Results))
	}
	if got := second.Results[0].Fields["kind"]; got != "text" {
		t.Fatalf("kind = %q after mutating an earlier result, want %q", got, "text")
	}
}

func TestRunEmptyIndexReportsNoMatches(t *testing.T) {
	tests := []struct {
		name string
		idx  *index.Index
	}{
		{name: "empty index", idx: index.New()},
		{name: "nil index", idx: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Run(tc.idx, core.SearchOptions{Query: "alpha", Facets: []string{"kind"}})
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if got.Total != 0 {
				t.Errorf("Total = %d, want 0", got.Total)
			}
			if got.Results == nil {
				t.Errorf("Results is nil, want an empty slice")
			}
			if len(got.Results) != 0 {
				t.Errorf("got %d results, want none", len(got.Results))
			}
			if f, ok := got.Facets["kind"]; !ok || len(f.Counts) != 0 {
				t.Errorf("kind facet = %+v, want an empty count map", f)
			}
		})
	}
}

func TestIntersect(t *testing.T) {
	tests := []struct {
		name string
		a    map[core.DocID]int
		b    map[core.DocID]int
		want map[core.DocID]int
	}{
		{
			name: "sums the shared documents",
			a:    map[core.DocID]int{"a": 2, "b": 1},
			b:    map[core.DocID]int{"b": 3, "c": 1},
			want: map[core.DocID]int{"b": 4},
		},
		{
			name: "no overlap",
			a:    map[core.DocID]int{"a": 1},
			b:    map[core.DocID]int{"b": 1},
			want: map[core.DocID]int{},
		},
		{
			name: "empty side wins",
			a:    map[core.DocID]int{},
			b:    map[core.DocID]int{"b": 1},
			want: map[core.DocID]int{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := intersect(tc.a, tc.b); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("intersect = %v, want %v", got, tc.want)
			}
		})
	}
}
