package token

import (
	"reflect"
	"strings"
	"testing"

	"sift/internal/core"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		body string
		cfg  core.Config
		want []core.Token
	}{
		{
			name: "lower cases and splits on punctuation",
			body: "Hello, World! Hello.",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"zzz"}},
			want: []core.Token{{Term: "hello", Position: 0}, {Term: "world", Position: 1}, {Term: "hello", Position: 2}},
		},
		{
			name: "drops stopwords and renumbers positions",
			body: "The quick brown fox and the dog",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"the", "and"}},
			want: []core.Token{{Term: "quick", Position: 0}, {Term: "brown", Position: 1}, {Term: "fox", Position: 2}, {Term: "dog", Position: 3}},
		},
		{
			name: "config stopwords are matched case insensitively",
			body: "The dog AND the cat",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"The", "and"}},
			want: []core.Token{{Term: "dog", Position: 0}, {Term: "cat", Position: 1}},
		},
		{
			name: "min term length drops short terms",
			body: "go is a good idea",
			cfg:  core.Config{MinTermLength: 3, Stopwords: []string{"zzz"}},
			want: []core.Token{{Term: "good", Position: 0}, {Term: "idea", Position: 1}},
		},
		{
			name: "digits join letters but punctuation splits",
			body: "Item42 v1.2 sift-index",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"zzz"}},
			want: []core.Token{{Term: "item42", Position: 0}, {Term: "v1", Position: 1}, {Term: "sift", Position: 2}, {Term: "index", Position: 3}},
		},
		{
			name: "every whitespace run separates terms",
			body: "one\ntwo\tthree  four\r\nfive",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"zzz"}},
			want: []core.Token{{Term: "one", Position: 0}, {Term: "two", Position: 1}, {Term: "three", Position: 2}, {Term: "four", Position: 3}, {Term: "five", Position: 4}},
		},
		{
			name: "default stopwords apply when the config lists none",
			body: "The index of the corpus",
			cfg:  core.Config{MinTermLength: 2},
			want: []core.Token{{Term: "index", Position: 0}, {Term: "corpus", Position: 1}},
		},
		{
			name: "min term length falls back to two",
			body: "x yy zzz",
			cfg:  core.Config{},
			want: []core.Token{{Term: "yy", Position: 0}, {Term: "zzz", Position: 1}},
		},
		{
			name: "non ascii letters are terms, not separators",
			body: "\u00c9cole Caf\u00e9",
			cfg:  core.Config{MinTermLength: 2, Stopwords: []string{"zzz"}},
			want: []core.Token{{Term: "\u00e9cole", Position: 0}, {Term: "caf\u00e9", Position: 1}},
		},
		{
			name: "body without terms yields an empty slice",
			body: " ,;-- \n\t",
			cfg:  core.Config{MinTermLength: 2},
			want: []core.Token{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.body, tc.cfg)
			if got == nil {
				t.Fatal("Tokenize returned nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestTokenizeMinTermLengthCountsRunes(t *testing.T) {
	body := "\u00e9\u00e9 abc"
	cfg := core.Config{MinTermLength: 2, Stopwords: []string{"zzz"}}
	got := Terms(Tokenize(body, cfg))
	want := []string{"\u00e9\u00e9", "abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("with MinTermLength 2 got %q, want %q", got, want)
	}

	cfg.MinTermLength = 3
	got = Terms(Tokenize(body, cfg))
	want = []string{"abc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("with MinTermLength 3 got %q, want %q", got, want)
	}
}

func TestTokenizePositionsAreContiguous(t *testing.T) {
	cfg := core.Config{MinTermLength: 2}
	tokens := Tokenize("The sift index is a small index of the corpus", cfg)
	if len(tokens) != 5 {
		t.Fatalf("token count = %d, want 5 (%v)", len(tokens), Terms(tokens))
	}
	for i, tok := range tokens {
		if tok.Position != i {
			t.Fatalf("token %d has Position %d, want %d", i, tok.Position, i)
		}
	}
	want := []string{"sift", "index", "small", "index", "corpus"}
	if got := Terms(tokens); !reflect.DeepEqual(got, want) {
		t.Fatalf("terms = %q, want %q", got, want)
	}
}

func TestTerms(t *testing.T) {
	tokens := []core.Token{{Term: "alpha", Position: 0}, {Term: "beta", Position: 1}, {Term: "alpha", Position: 2}}
	want := []string{"alpha", "beta", "alpha"}
	if got := Terms(tokens); !reflect.DeepEqual(got, want) {
		t.Fatalf("Terms = %q, want %q", got, want)
	}
	got := Terms(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("Terms(nil) = %#v, want an empty non-nil slice", got)
	}
}

func TestIsStopword(t *testing.T) {
	custom := core.Config{Stopwords: []string{"the", "and"}}
	defaults := core.Config{}
	narrow := core.Config{Stopwords: []string{"zzz"}}

	tests := []struct {
		name string
		cfg  core.Config
		term string
		want bool
	}{
		{name: "listed term", cfg: custom, term: "the", want: true},
		{name: "listed term other case", cfg: custom, term: "The", want: true},
		{name: "padded term", cfg: custom, term: " and ", want: true},
		{name: "unlisted term", cfg: custom, term: "dog", want: false},
		{name: "default list used when config lists none", cfg: defaults, term: "of", want: true},
		{name: "default list rejects content term", cfg: defaults, term: "sift", want: false},
		{name: "config list replaces the default list", cfg: narrow, term: "of", want: false},
		{name: "config list is honoured", cfg: narrow, term: "ZZZ", want: true},
		{name: "empty term", cfg: defaults, term: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStopword(tc.cfg, tc.term); got != tc.want {
				t.Fatalf("IsStopword(%q) = %v, want %v", tc.term, got, tc.want)
			}
		})
	}
}

func TestDefaultStopwordsAreSortedLowerAndUnique(t *testing.T) {
	if len(DefaultStopwords) < 50 {
		t.Fatalf("DefaultStopwords has %d entries, want at least 50", len(DefaultStopwords))
	}
	for i, w := range DefaultStopwords {
		if w != strings.ToLower(w) || strings.TrimSpace(w) != w || w == "" {
			t.Fatalf("DefaultStopwords[%d] = %q, want a trimmed lower-case term", i, w)
		}
		if i > 0 && DefaultStopwords[i-1] >= w {
			t.Fatalf("DefaultStopwords[%d] = %q is not greater than %q", i, w, DefaultStopwords[i-1])
		}
	}
	for _, w := range []string{"and", "of", "the"} {
		if !IsStopword(core.Config{}, w) {
			t.Fatalf("DefaultStopwords is missing %q", w)
		}
	}
}
