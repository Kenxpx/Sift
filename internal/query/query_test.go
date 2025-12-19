package query

import (
	"errors"
	"reflect"
	"testing"

	"sift/internal/core"
)

func TestParseClauses(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Query
	}{
		{
			name: "single term",
			in:   "alpha",
			want: Query{Clauses: []Clause{{Term: "alpha"}}},
		},
		{
			name: "terms are lower cased and order is kept",
			in:   "Alpha BETA gamma",
			want: Query{Clauses: []Clause{{Term: "alpha"}, {Term: "beta"}, {Term: "gamma"}}},
		},
		{
			name: "runs of whitespace separate clauses",
			in:   "  alpha \t beta \n ",
			want: Query{Clauses: []Clause{{Term: "alpha"}, {Term: "beta"}}},
		},
		{
			name: "phrase keeps word order and lower cases",
			in:   "\"Hello Brave World\"",
			want: Query{Clauses: []Clause{{Phrase: []string{"hello", "brave", "world"}}}},
		},
		{
			name: "phrase collapses inner whitespace",
			in:   "\"alpha   beta\"",
			want: Query{Clauses: []Clause{{Phrase: []string{"alpha", "beta"}}}},
		},
		{
			name: "single word phrase stays a phrase",
			in:   "\"alpha\"",
			want: Query{Clauses: []Clause{{Phrase: []string{"alpha"}}}},
		},
		{
			name: "field clause lower cases the name but keeps the value",
			in:   "Dir:docs/Guide",
			want: Query{Clauses: []Clause{{Field: "dir", Term: "docs/Guide"}}},
		},
		{
			name: "value keeps every colon after the first",
			in:   "url:http://x",
			want: Query{Clauses: []Clause{{Field: "url", Term: "http://x"}}},
		},
		{
			name: "field clause with a quoted value",
			in:   "dir:\"My Docs\"",
			want: Query{Clauses: []Clause{{Field: "dir", Phrase: []string{"My", "Docs"}}}},
		},
		{
			name: "negated term",
			in:   "-alpha",
			want: Query{Clauses: []Clause{{Term: "alpha", Negate: true}}},
		},
		{
			name: "negated phrase and negated field",
			in:   "-\"alpha beta\" -kind:source",
			want: Query{Clauses: []Clause{
				{Phrase: []string{"alpha", "beta"}, Negate: true},
				{Field: "kind", Term: "source", Negate: true},
			}},
		},
		{
			name: "hyphen inside a term is not a negation",
			in:   "well-known",
			want: Query{Clauses: []Clause{{Term: "well-known"}}},
		},
		{
			name: "mixed clauses",
			in:   "alpha \"beta gamma\" kind:markdown -delta",
			want: Query{Clauses: []Clause{
				{Term: "alpha"},
				{Phrase: []string{"beta", "gamma"}},
				{Field: "kind", Term: "markdown"},
				{Term: "delta", Negate: true},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if got.MatchAll {
				t.Fatalf("Parse(%q) set MatchAll for a query with %d clauses", tt.in, len(got.Clauses))
			}
		})
	}
}

func TestParseEmptyMatchesAll(t *testing.T) {
	for _, in := range []string{"", " ", "\t\n  \r\n"} {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error %v", in, err)
		}
		if !got.MatchAll {
			t.Errorf("Parse(%q).MatchAll = false, want true", in)
		}
		if len(got.Clauses) != 0 {
			t.Errorf("Parse(%q) produced %d clauses, want 0", in, len(got.Clauses))
		}
		if s := got.String(); s != "" {
			t.Errorf("Parse(%q).String() = %q, want empty", in, s)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantPos    int
		wantReason string
	}{
		{name: "unterminated quote", in: "alpha \"beta", wantPos: 6, wantReason: "unterminated quote"},
		{name: "unterminated quote at start", in: "\"", wantPos: 0, wantReason: "unterminated quote"},
		{name: "empty phrase", in: "\"\"", wantPos: 0, wantReason: "empty phrase"},
		{name: "blank phrase", in: "alpha \"   \"", wantPos: 6, wantReason: "empty phrase"},
		{name: "trailing hyphen", in: "alpha -", wantPos: 6, wantReason: "missing term after '-'"},
		{name: "lone hyphen", in: "- alpha", wantPos: 0, wantReason: "missing term after '-'"},
		{name: "missing field name", in: ":alpha", wantPos: 0, wantReason: "missing field name before ':'"},
		{name: "missing negated field name", in: "-:alpha", wantPos: 1, wantReason: "missing field name before ':'"},
		{name: "missing value at end", in: "kind:", wantPos: 4, wantReason: "missing value after ':'"},
		{name: "missing value before space", in: "kind: markdown", wantPos: 4, wantReason: "missing value after ':'"},
		{name: "quote inside a term", in: "alpha be\"ta", wantPos: 8, wantReason: "unexpected quote"},
		{name: "text after a phrase", in: "\"alpha beta\"x", wantPos: 12, wantReason: "unexpected text after quote"},
		{name: "offsets count bytes not runes", in: "n\u00e9e\"x", wantPos: 4, wantReason: "unexpected quote"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want an error", tt.in, got)
			}
			if !reflect.DeepEqual(got, Query{}) {
				t.Errorf("Parse(%q) returned %+v alongside its error, want the zero Query", tt.in, got)
			}
			if !errors.Is(err, core.ErrQuery) {
				t.Fatalf("Parse(%q) error %v does not match core.ErrQuery", tt.in, err)
			}
			var qe *core.QueryError
			if !errors.As(err, &qe) {
				t.Fatalf("Parse(%q) error %v is not a *core.QueryError", tt.in, err)
			}
			if qe.Position != tt.wantPos {
				t.Errorf("Parse(%q) position = %d, want %d", tt.in, qe.Position, tt.wantPos)
			}
			if qe.Reason != tt.wantReason {
				t.Errorf("Parse(%q) reason = %q, want %q", tt.in, qe.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseErrorMessage(t *testing.T) {
	_, err := Parse("alpha \"beta")
	if err == nil {
		t.Fatal("Parse returned no error")
	}
	const want = "sift: query: at 6: unterminated quote"
	if err.Error() != want {
		t.Errorf("error message = %q, want %q", err.Error(), want)
	}
}

func TestQueryString(t *testing.T) {
	tests := []struct {
		name string
		q    Query
		want string
	}{
		{name: "match all", q: Query{MatchAll: true}, want: ""},
		{name: "term", q: Query{Clauses: []Clause{{Term: "alpha"}}}, want: "alpha"},
		{
			name: "phrase is quoted",
			q:    Query{Clauses: []Clause{{Phrase: []string{"alpha", "beta"}}}},
			want: "\"alpha beta\"",
		},
		{
			name: "field and negation",
			q: Query{Clauses: []Clause{
				{Field: "kind", Term: "markdown"},
				{Term: "alpha", Negate: true},
			}},
			want: "kind:markdown -alpha",
		},
		{
			name: "negated field phrase",
			q:    Query{Clauses: []Clause{{Field: "dir", Phrase: []string{"My", "Docs"}, Negate: true}}},
			want: "-dir:\"My Docs\"",
		},
		{
			name: "clauses with no term and no phrase are omitted",
			q: Query{Clauses: []Clause{
				{Term: "alpha"},
				{Field: "kind"},
				{Term: "beta"},
			}},
			want: "alpha beta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseStringRoundTrip(t *testing.T) {
	inputs := []string{
		"",
		"alpha",
		"alpha beta gamma",
		"\"alpha beta\"",
		"kind:markdown",
		"dir:\"My Docs\"",
		"-alpha",
		"-\"alpha beta\"",
		"-kind:source",
		"alpha \"beta gamma\" kind:markdown -delta -\"epsilon zeta\"",
		"url:http://x",
	}
	for _, in := range inputs {
		first, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) returned error %v", in, err)
		}
		text := first.String()
		second, err := Parse(text)
		if err != nil {
			t.Fatalf("Parse(%q) (round trip of %q) returned error %v", text, in, err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Errorf("round trip of %q changed the query: %+v then %+v", in, first, second)
		}
		if again := second.String(); again != text {
			t.Errorf("String() is not idempotent for %q: %q then %q", in, text, again)
		}
	}
}

func TestParseIsIdempotentOnCase(t *testing.T) {
	q, err := Parse("Alpha \"Beta Gamma\" Kind:Markdown")
	if err != nil {
		t.Fatalf("Parse returned error %v", err)
	}
	const want = "alpha \"beta gamma\" kind:Markdown"
	if got := q.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	again, err := Parse(want)
	if err != nil {
		t.Fatalf("Parse of the canonical form returned error %v", err)
	}
	if !reflect.DeepEqual(q, again) {
		t.Fatalf("case folding is not idempotent: %+v then %+v", q, again)
	}
}
