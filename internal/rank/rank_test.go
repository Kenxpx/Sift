package rank

import (
	"math"
	"reflect"
	"testing"

	"sift/internal/core"
	"sift/internal/index"
	"sift/internal/query"
)

// Scores computed by hand from the corpus built in newCorpus: four documents,
// 100 tokens in total, so N = 4 and avgdl = 25.
const (
	alphaInD1 = 1.2499375387146556 // df 2, tf 3, dl 10
	alphaInD2 = 0.75491277090687114
	betaInD1  = 0.91862879351318039
	betaInD3  = 0.90232177350998799
	gammaInD4 = 0.96669349252447423

	tolerance = 1e-12
)

// docTokens returns length tokens for one document: the terms in spec in order,
// then "fill" for the remaining positions, so a document's length is exactly
// length and every document shares the common term "fill".
func docTokens(spec []string, length int) []core.Token {
	out := make([]core.Token, 0, length)
	for i := 0; i < length; i++ {
		term := "fill"
		if i < len(spec) {
			term = spec[i]
		}
		out = append(out, core.Token{Term: term, Position: i})
	}
	return out
}

// add indexes one document of the given length whose leading tokens are spec.
func add(ix *index.Index, id core.DocID, spec []string, length int) {
	ix.Add(core.Document{ID: id, Path: string(id), Title: string(id)}, docTokens(spec, length))
}

// newCorpus builds the shared fixture: d1 holds alpha three times and beta
// once, d2 holds alpha once, d3 holds beta twice, d4 holds gamma once, and
// every document is padded with "fill" to its length.
func newCorpus() *index.Index {
	ix := index.New()
	add(ix, "d1", []string{"alpha", "alpha", "alpha", "beta"}, 10)
	add(ix, "d2", []string{"alpha"}, 20)
	add(ix, "d3", []string{"beta", "beta"}, 30)
	add(ix, "d4", []string{"gamma"}, 40)
	return ix
}

func mustParse(t *testing.T, s string) query.Query {
	t.Helper()
	q, err := query.Parse(s)
	if err != nil {
		t.Fatalf("query.Parse(%q) returned error %v", s, err)
	}
	return q
}

func hitSet(ids ...core.DocID) map[core.DocID]int {
	hits := make(map[core.DocID]int, len(ids))
	for _, id := range ids {
		hits[id] = 1
	}
	return hits
}

func checkScores(t *testing.T, got map[core.DocID]float64, want map[core.DocID]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("scored %d documents %v, want %d %v", len(got), got, len(want), want)
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok {
			t.Errorf("document %q was not scored", id)
			continue
		}
		if math.Abs(g-w) > tolerance {
			t.Errorf("score(%q) = %.17g, want %.17g", id, g, w)
		}
	}
}

func TestScoreBM25Values(t *testing.T) {
	ix := newCorpus()
	if got := ix.DocCount(); got != 4 {
		t.Fatalf("fixture DocCount = %d, want 4", got)
	}
	if got := ix.AvgDocLength(); got != 25 {
		t.Fatalf("fixture AvgDocLength = %v, want 25", got)
	}

	tests := []struct {
		name string
		q    string
		hits map[core.DocID]int
		want map[core.DocID]float64
	}{
		{
			name: "one term over both documents that hold it",
			q:    "alpha",
			hits: hitSet("d1", "d2"),
			want: map[core.DocID]float64{"d1": alphaInD1, "d2": alphaInD2},
		},
		{
			name: "clause scores add up",
			q:    "alpha beta",
			hits: hitSet("d1", "d3"),
			want: map[core.DocID]float64{"d1": alphaInD1 + betaInD1, "d3": betaInD3},
		},
		{
			name: "a phrase is weighed by every one of its words",
			q:    "\"alpha beta\"",
			hits: hitSet("d1"),
			want: map[core.DocID]float64{"d1": alphaInD1 + betaInD1},
		},
		{
			name: "a rare term carries more weight",
			q:    "gamma",
			hits: hitSet("d4"),
			want: map[core.DocID]float64{"d4": gammaInD4},
		},
		{
			name: "a term written twice is weighed twice",
			q:    "alpha alpha",
			hits: hitSet("d1"),
			want: map[core.DocID]float64{"d1": 2 * alphaInD1},
		},
		{
			name: "a term nobody holds leaves the candidates at zero",
			q:    "missing",
			hits: hitSet("d1", "d2"),
			want: map[core.DocID]float64{"d1": 0, "d2": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkScores(t, Score(ix, mustParse(t, tt.q), tt.hits), tt.want)
		})
	}
}

func TestScoreOnlyTouchesTheHits(t *testing.T) {
	ix := newCorpus()
	// d2 holds alpha but is not a candidate, and d4 is a candidate that
	// alpha never reaches.
	got := Score(ix, mustParse(t, "alpha"), hitSet("d1", "d4"))
	if _, ok := got["d2"]; ok {
		t.Errorf("document outside the hit set was scored: %v", got)
	}
	checkScores(t, got, map[core.DocID]float64{"d1": alphaInD1, "d4": 0})
}

func TestScoreIgnoresNegatedAndFieldClauses(t *testing.T) {
	ix := newCorpus()
	hits := hitSet("d1", "d2")

	zero := map[core.DocID]float64{"d1": 0, "d2": 0}
	for _, q := range []string{"-alpha", "kind:markdown", "-kind:markdown", "-alpha -beta", ""} {
		checkScores(t, Score(ix, mustParse(t, q), hits), zero)
	}

	// A negated or field clause next to a real one changes nothing.
	want := map[core.DocID]float64{"d1": alphaInD1, "d2": alphaInD2}
	for _, q := range []string{"alpha", "alpha -beta", "alpha kind:markdown", "kind:markdown alpha -gamma"} {
		checkScores(t, Score(ix, mustParse(t, q), hits), want)
	}
}

func TestScoreRareTermOutweighsCommonTerm(t *testing.T) {
	ix := index.New()
	add(ix, "d1", []string{"rare", "common"}, 10)
	add(ix, "d2", []string{"common"}, 10)
	add(ix, "d3", []string{"common"}, 10)
	add(ix, "d4", []string{"common"}, 10)

	hits := hitSet("d1")
	rare := Score(ix, mustParse(t, "rare"), hits)["d1"]
	common := Score(ix, mustParse(t, "common"), hits)["d1"]
	if !(rare > common) {
		t.Fatalf("rare term scored %.17g, common term scored %.17g, want the rare one higher", rare, common)
	}
	// df 1 of 4 versus df 4 of 4: ln(1+3.5/1.5) against ln(1+0.5/4.5).
	if math.Abs(rare/common-math.Log(1+3.5/1.5)/math.Log(1+0.5/4.5)) > 1e-9 {
		t.Errorf("score ratio %.17g does not follow the idf ratio", rare/common)
	}
}

func TestScoreNormalizesByDocumentLength(t *testing.T) {
	ix := index.New()
	add(ix, "short", []string{"alpha"}, 10)
	add(ix, "long", []string{"alpha"}, 40)

	got := Score(ix, mustParse(t, "alpha"), hitSet("short", "long"))
	if !(got["short"] > got["long"]) {
		t.Fatalf("short = %.17g, long = %.17g, want the short document to score higher", got["short"], got["long"])
	}
}

func TestScoreSaturatesWithTermFrequency(t *testing.T) {
	ix := index.New()
	add(ix, "once", []string{"alpha"}, 20)
	add(ix, "twice", []string{"alpha", "alpha"}, 20)
	add(ix, "other", []string{"beta"}, 20)

	got := Score(ix, mustParse(t, "alpha"), hitSet("once", "twice"))
	if !(got["twice"] > got["once"]) {
		t.Fatalf("twice = %.17g, once = %.17g, want more occurrences to score higher", got["twice"], got["once"])
	}
	if got["twice"] >= 2*got["once"] {
		t.Errorf("twice = %.17g is not below twice once = %.17g, so the term weight does not saturate", got["twice"], 2*got["once"])
	}
}

func TestScoreEdgeCases(t *testing.T) {
	ix := newCorpus()
	q := mustParse(t, "alpha")

	if got := Score(nil, q, hitSet("d1")); !reflect.DeepEqual(got, map[core.DocID]float64{"d1": 0}) {
		t.Errorf("Score with a nil index = %v, want a zero for every hit", got)
	}
	if got := Score(ix, q, nil); len(got) != 0 {
		t.Errorf("Score with no hits = %v, want an empty map", got)
	}
	if got := Score(ix, q, map[core.DocID]int{}); got == nil || len(got) != 0 {
		t.Errorf("Score with an empty hit set = %v, want a non-nil empty map", got)
	}
	if got := Score(index.New(), q, hitSet("d1")); !reflect.DeepEqual(got, map[core.DocID]float64{"d1": 0}) {
		t.Errorf("Score against an empty index = %v, want a zero for every hit", got)
	}
	all := mustParse(t, "")
	if !all.MatchAll {
		t.Fatal("the empty query does not match all")
	}
	checkScores(t, Score(ix, all, hitSet("d1", "d2")), map[core.DocID]float64{"d1": 0, "d2": 0})
}

func TestScoreIsDeterministic(t *testing.T) {
	ix := newCorpus()
	q := mustParse(t, "alpha beta gamma fill \"alpha beta\"")
	hits := hitSet("d1", "d2", "d3", "d4")

	first := Score(ix, q, hits)
	for run := 0; run < 50; run++ {
		got := Score(ix, q, hits)
		if len(got) != len(first) {
			t.Fatalf("run %d scored %d documents, want %d", run, len(got), len(first))
		}
		for id, want := range first {
			// Bit-exact: the same additions must happen in the same order.
			if math.Float64bits(got[id]) != math.Float64bits(want) {
				t.Fatalf("run %d score(%q) = %.17g, want %.17g", run, id, got[id], want)
			}
		}
	}
}

func TestOrder(t *testing.T) {
	tests := []struct {
		name string
		in   []core.SearchResult
		want []core.DocID
	}{
		{
			name: "score descending wins first",
			in: []core.SearchResult{
				{DocID: "a", Score: 0.5, Freq: 9},
				{DocID: "b", Score: 2.5, Freq: 1},
				{DocID: "c", Score: 1.5, Freq: 5},
			},
			want: []core.DocID{"b", "c", "a"},
		},
		{
			name: "equal scores fall back to frequency descending",
			in: []core.SearchResult{
				{DocID: "a", Score: 2, Freq: 1},
				{DocID: "b", Score: 2, Freq: 5},
				{DocID: "c", Score: 2, Freq: 3},
			},
			want: []core.DocID{"b", "c", "a"},
		},
		{
			name: "equal scores and frequencies fall back to DocID ascending",
			in: []core.SearchResult{
				{DocID: "zeta", Score: 2, Freq: 4},
				{DocID: "alpha", Score: 2, Freq: 4},
				{DocID: "mid", Score: 2, Freq: 4},
			},
			want: []core.DocID{"alpha", "mid", "zeta"},
		},
		{
			name: "every key together",
			in: []core.SearchResult{
				{DocID: "c", Score: 1, Freq: 1},
				{DocID: "a", Score: 2, Freq: 1},
				{DocID: "b", Score: 2, Freq: 5},
				{DocID: "d", Score: 2, Freq: 5},
				{DocID: "e", Score: 0, Freq: 9},
			},
			want: []core.DocID{"b", "d", "a", "c", "e"},
		},
		{name: "empty", in: nil, want: nil},
		{
			name: "single result",
			in:   []core.SearchResult{{DocID: "only", Score: 1}},
			want: []core.DocID{"only"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := tt.in
			Order(results)
			var got []core.DocID
			for _, r := range results {
				got = append(got, r.DocID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Order() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrderSortsInPlaceAndIsStable(t *testing.T) {
	results := []core.SearchResult{
		{DocID: "same", Score: 1, Freq: 1, Title: "first"},
		{DocID: "same", Score: 1, Freq: 1, Title: "second"},
		{DocID: "same", Score: 1, Freq: 1, Title: "third"},
	}
	backing := results
	Order(results)
	if &backing[0] != &results[0] {
		t.Fatal("Order did not sort in place")
	}
	for i, want := range []string{"first", "second", "third"} {
		if results[i].Title != want {
			t.Errorf("result %d title = %q, want %q: equal results must keep their order", i, results[i].Title, want)
		}
	}
}

func TestOrderMatchesScoreOutput(t *testing.T) {
	ix := newCorpus()
	hits := map[core.DocID]int{"d1": 4, "d2": 1, "d3": 2, "d4": 0}
	scores := Score(ix, mustParse(t, "alpha beta"), hits)

	results := []core.SearchResult{
		{DocID: "d4", Score: scores["d4"], Freq: hits["d4"]},
		{DocID: "d3", Score: scores["d3"], Freq: hits["d3"]},
		{DocID: "d2", Score: scores["d2"], Freq: hits["d2"]},
		{DocID: "d1", Score: scores["d1"], Freq: hits["d1"]},
	}
	Order(results)

	want := []core.DocID{"d1", "d3", "d2", "d4"}
	var got []core.DocID
	for _, r := range results {
		got = append(got, r.DocID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Order() = %v, want %v (scores %v)", got, want, scores)
	}
}
