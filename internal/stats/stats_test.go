package stats

import (
	"fmt"
	"reflect"
	"testing"

	"sift/internal/core"
	"sift/internal/index"
)

// makeTokens turns terms into tokens with ascending positions.
func makeTokens(terms ...string) []core.Token {
	out := make([]core.Token, 0, len(terms))
	for i, t := range terms {
		out = append(out, core.Token{Term: t, Position: i})
	}
	return out
}

// makeDoc builds a document whose Fields carry kind and language. An empty
// language leaves the field out entirely, which is how extraction records a
// document that is not source code.
func makeDoc(id, kind, language string) core.Document {
	fields := map[string]string{"kind": kind, "dir": "docs"}
	if language != "" {
		fields["language"] = language
	}
	return core.Document{
		ID:       core.DocID(id),
		Path:     id,
		Title:    "Title of " + id,
		Kind:     kind,
		Language: language,
		Fields:   fields,
	}
}

// sampleIndex holds three documents: two markdown and one Go source.
func sampleIndex(t *testing.T) *index.Index {
	t.Helper()
	idx := index.New()
	idx.Add(makeDoc("docs/alpha.md", "markdown", ""), makeTokens("alpha", "beta", "alpha"))
	idx.Add(makeDoc("docs/beta.md", "markdown", ""), makeTokens("beta", "gamma"))
	idx.Add(makeDoc("src/main.go", "source", "go"), makeTokens("delta"))
	return idx
}

func TestComputeHeadlineCounts(t *testing.T) {
	got := Compute(sampleIndex(t))

	if got.Documents != 3 {
		t.Errorf("Documents = %d, want 3", got.Documents)
	}
	// Distinct terms: alpha, beta, gamma, delta.
	if got.Terms != 4 {
		t.Errorf("Terms = %d, want 4", got.Terms)
	}
	// Token occurrences: 3 + 2 + 1.
	if got.Tokens != 6 {
		t.Errorf("Tokens = %d, want 6", got.Tokens)
	}
}

func TestComputeGroupsDocuments(t *testing.T) {
	idx := sampleIndex(t)
	// A document with no fields at all must still be counted, under "".
	idx.Add(core.Document{ID: "notes", Path: "notes", Title: "Notes"}, makeTokens("epsilon"))

	got := Compute(idx)

	wantKind := map[string]int{"markdown": 2, "source": 1, "": 1}
	if !reflect.DeepEqual(got.ByKind, wantKind) {
		t.Errorf("ByKind = %v, want %v", got.ByKind, wantKind)
	}
	wantLanguage := map[string]int{"go": 1, "": 3}
	if !reflect.DeepEqual(got.ByLanguage, wantLanguage) {
		t.Errorf("ByLanguage = %v, want %v", got.ByLanguage, wantLanguage)
	}
}

func TestComputeGroupTotalsCoverEveryDocument(t *testing.T) {
	idx := sampleIndex(t)
	idx.Add(core.Document{ID: "notes", Path: "notes"}, makeTokens("epsilon"))
	got := Compute(idx)

	for _, tc := range []struct {
		name   string
		counts map[string]int
	}{
		{"ByKind", got.ByKind},
		{"ByLanguage", got.ByLanguage},
	} {
		sum := 0
		for _, n := range tc.counts {
			sum += n
		}
		if sum != got.Documents {
			t.Errorf("%s totals to %d, want Documents = %d", tc.name, sum, got.Documents)
		}
	}
}

func TestComputeLargestDocsOrder(t *testing.T) {
	idx := index.New()
	idx.Add(makeDoc("b.md", "markdown", ""), makeTokens("a", "b"))
	idx.Add(makeDoc("a.md", "markdown", ""), makeTokens("a", "b"))
	idx.Add(makeDoc("long.md", "markdown", ""), makeTokens("a", "b", "c", "d"))
	idx.Add(makeDoc("short.md", "markdown", ""), makeTokens("a"))

	got := Compute(idx)

	wantIDs := []core.DocID{"long.md", "a.md", "b.md", "short.md"}
	if len(got.LargestDocs) != len(wantIDs) {
		t.Fatalf("LargestDocs has %d entries, want %d", len(got.LargestDocs), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got.LargestDocs[i].ID != want {
			t.Errorf("LargestDocs[%d].ID = %q, want %q", i, got.LargestDocs[i].ID, want)
		}
	}
	if got.LargestDocs[0].Length != 4 {
		t.Errorf("LargestDocs[0].Length = %d, want 4", got.LargestDocs[0].Length)
	}
}

func TestComputeLargestDocsCap(t *testing.T) {
	idx := index.New()
	// Document i gets i+1 tokens, so doc-12 is the longest.
	for i := 0; i < 12; i++ {
		terms := make([]string, 0, i+1)
		for j := 0; j <= i; j++ {
			terms = append(terms, fmt.Sprintf("t%02d", j))
		}
		idx.Add(makeDoc(fmt.Sprintf("doc-%02d", i+1), "markdown", ""), makeTokens(terms...))
	}

	got := Compute(idx)

	if len(got.LargestDocs) != MaxLargestDocs {
		t.Fatalf("LargestDocs has %d entries, want %d", len(got.LargestDocs), MaxLargestDocs)
	}
	if got.LargestDocs[0].ID != "doc-12" || got.LargestDocs[0].Length != 12 {
		t.Errorf("first = %q/%d, want doc-12/12", got.LargestDocs[0].ID, got.LargestDocs[0].Length)
	}
	// The tenth longest is doc-03, with three tokens; doc-01 and doc-02 fall off.
	last := got.LargestDocs[MaxLargestDocs-1]
	if last.ID != "doc-03" || last.Length != 3 {
		t.Errorf("last = %q/%d, want doc-03/3", last.ID, last.Length)
	}
	if got.Documents != 12 {
		t.Errorf("Documents = %d, want 12", got.Documents)
	}
}

func TestComputeEmptyIndex(t *testing.T) {
	got := Compute(index.New())

	if got.Documents != 0 || got.Terms != 0 || got.Tokens != 0 {
		t.Errorf("counts = %d/%d/%d, want 0/0/0", got.Documents, got.Terms, got.Tokens)
	}
	if got.ByKind == nil || got.ByLanguage == nil || got.LargestDocs == nil {
		t.Fatalf("Compute returned nil maps or slice: %+v", got)
	}
	if len(got.ByKind) != 0 || len(got.ByLanguage) != 0 || len(got.LargestDocs) != 0 {
		t.Errorf("empty index produced %+v", got)
	}
}

func TestComputeNilIndex(t *testing.T) {
	got := Compute(nil)

	if got.Documents != 0 || got.Terms != 0 || got.Tokens != 0 {
		t.Errorf("counts = %d/%d/%d, want 0/0/0", got.Documents, got.Terms, got.Tokens)
	}
	if got.ByKind == nil || got.ByLanguage == nil || got.LargestDocs == nil {
		t.Fatalf("Compute(nil) returned nil maps or slice: %+v", got)
	}
}

func TestComputeIsIndependentOfInsertionOrder(t *testing.T) {
	docs := []struct {
		id, kind, language string
		terms              []string
	}{
		{"a.md", "markdown", "", []string{"alpha", "beta"}},
		{"b.go", "source", "go", []string{"beta", "gamma", "beta"}},
		{"c.txt", "text", "", []string{"delta"}},
	}

	forward := index.New()
	for _, d := range docs {
		forward.Add(makeDoc(d.id, d.kind, d.language), makeTokens(d.terms...))
	}
	reverse := index.New()
	for i := len(docs) - 1; i >= 0; i-- {
		d := docs[i]
		reverse.Add(makeDoc(d.id, d.kind, d.language), makeTokens(d.terms...))
	}

	first, second := Compute(forward), Compute(reverse)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("insertion order changed the summary:\n%+v\n%+v", first, second)
	}
	if first.Tokens != 6 || first.Documents != 3 || first.Terms != 4 {
		t.Errorf("summary = %d docs / %d terms / %d tokens, want 3/4/6",
			first.Documents, first.Terms, first.Tokens)
	}
}
