package index

import (
	"reflect"
	"testing"

	"sift/internal/core"
)

// tok builds tokens with contiguous ascending positions, as the token package
// produces them.
func tok(terms ...string) []core.Token {
	out := make([]core.Token, 0, len(terms))
	for i, term := range terms {
		out = append(out, core.Token{Term: term, Position: i})
	}
	return out
}

// testDoc builds a document with one field set.
func testDoc(id core.DocID, kind string) core.Document {
	return core.Document{
		ID:          id,
		Path:        string(id),
		Title:       "title of " + string(id),
		Fields:      map[string]string{"kind": kind},
		ContentHash: "hash-" + string(id),
	}
}

func TestAddAndAccessors(t *testing.T) {
	ix := New()
	ix.Add(testDoc("a/one.md", "markdown"), tok("alpha", "beta", "alpha", "gamma"))
	ix.Add(testDoc("b/two.md", "text"), tok("beta", "beta"))

	if got, want := ix.DocCount(), 2; got != want {
		t.Fatalf("DocCount = %d, want %d", got, want)
	}
	if got, want := ix.TermCount(), 3; got != want {
		t.Fatalf("TermCount = %d, want %d", got, want)
	}
	if got, want := ix.AvgDocLength(), 3.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}

	wantDocs := []core.DocInfo{
		{ID: "a/one.md", Length: 4, Fields: map[string]string{"kind": "markdown"}, Title: "title of a/one.md", ContentHash: "hash-a/one.md"},
		{ID: "b/two.md", Length: 2, Fields: map[string]string{"kind": "text"}, Title: "title of b/two.md", ContentHash: "hash-b/two.md"},
	}
	if got := ix.Docs(); !reflect.DeepEqual(got, wantDocs) {
		t.Fatalf("Docs = %+v, want %+v", got, wantDocs)
	}

	wantTerms := []core.TermStats{
		{Term: "alpha", DocFreq: 1, TotalFreq: 2},
		{Term: "beta", DocFreq: 2, TotalFreq: 3},
		{Term: "gamma", DocFreq: 1, TotalFreq: 1},
	}
	if got := ix.Terms(); !reflect.DeepEqual(got, wantTerms) {
		t.Fatalf("Terms = %+v, want %+v", got, wantTerms)
	}

	wantPostings := []core.Posting{
		{DocID: "a/one.md", Freq: 1, Positions: []int{1}},
		{DocID: "b/two.md", Freq: 2, Positions: []int{0, 1}},
	}
	if got := ix.Postings("beta"); !reflect.DeepEqual(got, wantPostings) {
		t.Fatalf("Postings(beta) = %+v, want %+v", got, wantPostings)
	}
	if got := ix.Postings("alpha"); len(got) != 1 || !reflect.DeepEqual(got[0].Positions, []int{0, 2}) {
		t.Fatalf("Postings(alpha) = %+v, want one posting at positions [0 2]", got)
	}
	if got := ix.Postings("missing"); got == nil || len(got) != 0 {
		t.Fatalf("Postings(missing) = %#v, want an empty non-nil slice", got)
	}
}

func TestAddReplacesExistingDocument(t *testing.T) {
	ix := New()
	ix.Add(testDoc("d1", "markdown"), tok("alpha", "alpha", "beta"))
	ix.Add(testDoc("d2", "text"), tok("beta"))
	ix.Add(testDoc("d1", "text"), tok("gamma"))

	if got, want := ix.DocCount(), 2; got != want {
		t.Fatalf("DocCount = %d, want %d", got, want)
	}
	if got := ix.Postings("alpha"); len(got) != 0 {
		t.Fatalf("Postings(alpha) = %+v, want none after replacement", got)
	}
	wantTerms := []core.TermStats{
		{Term: "beta", DocFreq: 1, TotalFreq: 1},
		{Term: "gamma", DocFreq: 1, TotalFreq: 1},
	}
	if got := ix.Terms(); !reflect.DeepEqual(got, wantTerms) {
		t.Fatalf("Terms = %+v, want %+v", got, wantTerms)
	}
	if got, want := ix.TermCount(), 2; got != want {
		t.Fatalf("TermCount = %d, want %d", got, want)
	}
	info, ok := ix.Doc("d1")
	if !ok {
		t.Fatal("Doc(d1) missing after replacement")
	}
	if info.Length != 1 || info.Fields["kind"] != "text" {
		t.Fatalf("Doc(d1) = %+v, want length 1 and kind text", info)
	}
	if got, want := ix.AvgDocLength(), 1.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}
}

func TestAvgDocLength(t *testing.T) {
	ix := New()
	if got, want := ix.AvgDocLength(), 0.0; got != want {
		t.Fatalf("empty AvgDocLength = %v, want %v", got, want)
	}
	if got := ix.Docs(); got == nil || len(got) != 0 {
		t.Fatalf("empty Docs = %#v, want an empty non-nil slice", got)
	}
	ix.Add(testDoc("d1", "text"), tok("a", "b", "c"))
	ix.Add(testDoc("d2", "text"), tok("d", "e", "f", "g", "h"))
	if got, want := ix.AvgDocLength(), 4.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}
	ix.Add(testDoc("d3", "text"), nil)
	if got, want := ix.AvgDocLength(), 8.0/3.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}
	if got, want := ix.DocCount(), 3; got != want {
		t.Fatalf("DocCount = %d, want %d", got, want)
	}
}

func TestDocLookup(t *testing.T) {
	ix := New()
	ix.Add(testDoc("d1", "markdown"), tok("alpha"))

	info, ok := ix.Doc("d1")
	if !ok {
		t.Fatal("Doc(d1) not found")
	}
	want := core.DocInfo{ID: "d1", Length: 1, Fields: map[string]string{"kind": "markdown"}, Title: "title of d1", ContentHash: "hash-d1"}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("Doc(d1) = %+v, want %+v", info, want)
	}
	if got, ok := ix.Doc("nope"); ok || !reflect.DeepEqual(got, core.DocInfo{}) {
		t.Fatalf("Doc(nope) = %+v, %v, want zero value and false", got, ok)
	}
}

func TestMergeRecomputesTermStats(t *testing.T) {
	first := New()
	first.Add(testDoc("d1", "markdown"), tok("alpha", "alpha", "alpha"))

	second := New()
	second.Add(testDoc("d1", "text"), tok("alpha", "beta"))
	second.Add(testDoc("d2", "text"), tok("alpha", "alpha"))

	merged := Merge(first, second)

	if got, want := merged.DocCount(), 2; got != want {
		t.Fatalf("DocCount = %d, want %d", got, want)
	}
	wantTerms := []core.TermStats{
		{Term: "alpha", DocFreq: 2, TotalFreq: 3},
		{Term: "beta", DocFreq: 1, TotalFreq: 1},
	}
	if got := merged.Terms(); !reflect.DeepEqual(got, wantTerms) {
		t.Fatalf("Terms = %+v, want %+v (statistics must be recomputed, not summed)", got, wantTerms)
	}
	info, ok := merged.Doc("d1")
	if !ok || info.Length != 2 || info.Fields["kind"] != "text" {
		t.Fatalf("Doc(d1) = %+v, %v, want the later document with length 2", info, ok)
	}
	wantPostings := []core.Posting{
		{DocID: "d1", Freq: 1, Positions: []int{0}},
		{DocID: "d2", Freq: 2, Positions: []int{0, 1}},
	}
	if got := merged.Postings("alpha"); !reflect.DeepEqual(got, wantPostings) {
		t.Fatalf("Postings(alpha) = %+v, want %+v", got, wantPostings)
	}
	if got, want := merged.AvgDocLength(), 2.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}

	// The inputs must be untouched.
	wantFirst := []core.TermStats{{Term: "alpha", DocFreq: 1, TotalFreq: 3}}
	if got := first.Terms(); !reflect.DeepEqual(got, wantFirst) {
		t.Fatalf("first index changed: Terms = %+v, want %+v", got, wantFirst)
	}
	if info, _ := first.Doc("d1"); info.Length != 3 {
		t.Fatalf("first index changed: Doc(d1).Length = %d, want 3", info.Length)
	}
}

func TestMergeCombinesDocumentsAndHandlesNil(t *testing.T) {
	if got := Merge(); got == nil || got.DocCount() != 0 || got.TermCount() != 0 || got.AvgDocLength() != 0 {
		t.Fatalf("Merge() = %+v, want an empty index", got)
	}

	first := New()
	first.Add(testDoc("b", "text"), tok("alpha"))
	second := New()
	second.Add(testDoc("a", "markdown"), tok("beta", "beta"))

	merged := Merge(nil, first, nil, second, nil)
	wantIDs := []core.DocID{"a", "b"}
	var gotIDs []core.DocID
	for _, info := range merged.Docs() {
		gotIDs = append(gotIDs, info.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("merged doc IDs = %v, want %v", gotIDs, wantIDs)
	}
	if got, want := merged.TermCount(), 2; got != want {
		t.Fatalf("TermCount = %d, want %d", got, want)
	}
	if got, want := merged.AvgDocLength(), 1.5; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}

	// Merging into a new index must not alias the inputs.
	merged.Add(testDoc("a", "text"), tok("gamma"))
	if got, want := second.TermCount(), 1; got != want {
		t.Fatalf("input TermCount = %d, want %d", got, want)
	}
	if got := second.Postings("beta"); len(got) != 1 || got[0].Freq != 2 {
		t.Fatalf("input Postings(beta) = %+v, want one posting with Freq 2", got)
	}
}

func TestAddPostings(t *testing.T) {
	ix := New()
	info := core.DocInfo{ID: "d1", Fields: map[string]string{"kind": "source"}, Title: "restored", ContentHash: "hash-d1"}
	ix.AddPostings(info, map[string]core.Posting{
		"alpha": {DocID: "ignored", Freq: 3, Positions: []int{4, 0, 2}},
		"beta":  {DocID: "ignored", Freq: 1, Positions: []int{1}},
		"empty": {DocID: "ignored"},
	})

	if got, want := ix.TermCount(), 2; got != want {
		t.Fatalf("TermCount = %d, want %d", got, want)
	}
	wantAlpha := []core.Posting{{DocID: "d1", Freq: 3, Positions: []int{0, 2, 4}}}
	if got := ix.Postings("alpha"); !reflect.DeepEqual(got, wantAlpha) {
		t.Fatalf("Postings(alpha) = %+v, want %+v", got, wantAlpha)
	}
	stored, ok := ix.Doc("d1")
	if !ok {
		t.Fatal("Doc(d1) missing")
	}
	if stored.Length != 4 {
		t.Fatalf("Doc(d1).Length = %d, want 4 derived from the postings", stored.Length)
	}
	if got, want := ix.AvgDocLength(), 4.0; got != want {
		t.Fatalf("AvgDocLength = %v, want %v", got, want)
	}

	// A recorded length is kept as it is.
	ix.AddPostings(core.DocInfo{ID: "d2", Length: 9}, map[string]core.Posting{"alpha": {Positions: []int{0}}})
	if info, _ := ix.Doc("d2"); info.Length != 9 {
		t.Fatalf("Doc(d2).Length = %d, want 9", info.Length)
	}
	wantStats := []core.TermStats{
		{Term: "alpha", DocFreq: 2, TotalFreq: 4},
		{Term: "beta", DocFreq: 1, TotalFreq: 1},
	}
	if got := ix.Terms(); !reflect.DeepEqual(got, wantStats) {
		t.Fatalf("Terms = %+v, want %+v", got, wantStats)
	}
}

func TestAccessorsReturnCopies(t *testing.T) {
	fields := map[string]string{"kind": "markdown"}
	doc := testDoc("d1", "markdown")
	doc.Fields = fields
	tokens := tok("alpha", "alpha")

	ix := New()
	ix.Add(doc, tokens)

	// Mutating the caller data must not reach the index.
	fields["kind"] = "mutated"
	tokens[0].Term = "mutated"

	// Mutating returned data must not reach the index either.
	docs := ix.Docs()
	docs[0].Fields["kind"] = "also-mutated"
	postings := ix.Postings("alpha")
	postings[0].Positions[0] = 99

	got, ok := ix.Doc("d1")
	if !ok {
		t.Fatal("Doc(d1) missing")
	}
	if got.Fields["kind"] != "markdown" {
		t.Fatalf("Doc(d1).Fields[kind] = %q, want %q", got.Fields["kind"], "markdown")
	}
	wantPostings := []core.Posting{{DocID: "d1", Freq: 2, Positions: []int{0, 1}}}
	if p := ix.Postings("alpha"); !reflect.DeepEqual(p, wantPostings) {
		t.Fatalf("Postings(alpha) = %+v, want %+v", p, wantPostings)
	}
	if got := ix.TermCount(); got != 1 {
		t.Fatalf("TermCount = %d, want 1", got)
	}
}
