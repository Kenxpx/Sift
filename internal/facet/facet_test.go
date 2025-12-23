package facet

import (
	"reflect"
	"testing"

	"sift/internal/core"
	"sift/internal/index"
)

// sample builds a small index whose documents differ in kind, language and
// directory, and where two documents carry no language at all.
func sample() *index.Index {
	idx := index.New()
	add := func(id, kind, language, dir string) {
		fields := map[string]string{"kind": kind, "dir": dir}
		if language != "" {
			fields["language"] = language
		}
		idx.Add(core.Document{
			ID:     core.DocID(id),
			Path:   id,
			Title:  id,
			Kind:   kind,
			Fields: fields,
		}, []core.Token{{Term: "alpha", Position: 0}})
	}
	add("a.md", "markdown", "", ".")
	add("b.md", "markdown", "", "docs")
	add("c.go", "source", "go", "src")
	add("d.go", "source", "go", "src")
	add("e.txt", "text", "", ".")
	return idx
}

// counts flattens a facet result so tests can compare plain values.
func counts(got map[string]core.Facet) map[string]map[string]int {
	out := make(map[string]map[string]int, len(got))
	for field, f := range got {
		out[field] = f.Counts
	}
	return out
}

func TestCount(t *testing.T) {
	idx := sample()
	all := []core.DocID{"a.md", "b.md", "c.go", "d.go", "e.txt"}

	tests := []struct {
		name   string
		ids    []core.DocID
		fields []string
		want   map[string]map[string]int
	}{
		{
			name:   "one field over every document",
			ids:    all,
			fields: []string{"kind"},
			want:   map[string]map[string]int{"kind": {"markdown": 2, "source": 2, "text": 1}},
		},
		{
			name:   "missing values counted under empty string",
			ids:    all,
			fields: []string{"language"},
			want:   map[string]map[string]int{"language": {"": 3, "go": 2}},
		},
		{
			name:   "several fields at once",
			ids:    all,
			fields: []string{"dir", "kind"},
			want: map[string]map[string]int{
				"dir":  {".": 2, "docs": 1, "src": 2},
				"kind": {"markdown": 2, "source": 2, "text": 1},
			},
		},
		{
			name:   "subset of documents",
			ids:    []core.DocID{"c.go", "d.go"},
			fields: []string{"dir", "language"},
			want: map[string]map[string]int{
				"dir":      {"src": 2},
				"language": {"go": 2},
			},
		},
		{
			name:   "unknown id counted under empty string",
			ids:    []core.DocID{"a.md", "missing.md"},
			fields: []string{"kind"},
			want:   map[string]map[string]int{"kind": {"markdown": 1, "": 1}},
		},
		{
			name:   "repeated field counted once",
			ids:    all,
			fields: []string{"kind", "kind", "kind"},
			want:   map[string]map[string]int{"kind": {"markdown": 2, "source": 2, "text": 1}},
		},
		{
			name:   "repeated id counted every time",
			ids:    []core.DocID{"a.md", "a.md", "c.go"},
			fields: []string{"kind"},
			want:   map[string]map[string]int{"kind": {"markdown": 2, "source": 1}},
		},
		{
			name:   "unknown field counts everything as empty",
			ids:    all,
			fields: []string{"author"},
			want:   map[string]map[string]int{"author": {"": 5}},
		},
		{
			name:   "no ids keeps the field present and empty",
			ids:    nil,
			fields: []string{"kind"},
			want:   map[string]map[string]int{"kind": {}},
		},
		{
			name:   "no fields counts nothing",
			ids:    all,
			fields: nil,
			want:   map[string]map[string]int{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Count(idx, tc.ids, tc.fields)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d facets, want %d: %v", len(got), len(tc.want), counts(got))
			}
			if diff := counts(got); !reflect.DeepEqual(diff, tc.want) {
				t.Fatalf("counts = %v, want %v", diff, tc.want)
			}
		})
	}
}

func TestCountSetsFieldName(t *testing.T) {
	got := Count(sample(), []core.DocID{"a.md"}, []string{"kind", "dir"})
	for name, f := range got {
		if f.Field != name {
			t.Errorf("facet keyed %q reports Field %q", name, f.Field)
		}
		if f.Counts == nil {
			t.Errorf("facet %q has a nil count map", name)
		}
	}
}

func TestCountIgnoresFieldOrder(t *testing.T) {
	idx := sample()
	ids := []core.DocID{"a.md", "c.go", "e.txt"}
	ascending := Count(idx, ids, []string{"dir", "kind", "language"})
	shuffled := Count(idx, ids, []string{"language", "kind", "dir", "kind"})
	if !reflect.DeepEqual(counts(ascending), counts(shuffled)) {
		t.Fatalf("field order changed the counts: %v vs %v", counts(ascending), counts(shuffled))
	}
}

func TestCountTotalsMatchDocumentCount(t *testing.T) {
	ids := []core.DocID{"a.md", "b.md", "c.go", "d.go", "e.txt"}
	got := Count(sample(), ids, []string{"kind", "language", "dir"})
	for field, f := range got {
		total := 0
		for _, n := range f.Counts {
			total += n
		}
		if total != len(ids) {
			t.Errorf("facet %q totals %d, want %d", field, total, len(ids))
		}
	}
}

func TestCountNilIndexCountsEverythingAsEmpty(t *testing.T) {
	got := Count(nil, []core.DocID{"a.md", "b.md"}, []string{"kind"})
	want := map[string]map[string]int{"kind": {"": 2}}
	if !reflect.DeepEqual(counts(got), want) {
		t.Fatalf("counts = %v, want %v", counts(got), want)
	}
}

func TestCountResultIsIndependentOfTheIndex(t *testing.T) {
	idx := sample()
	got := Count(idx, []core.DocID{"c.go"}, []string{"language"})
	got["language"].Counts["go"] = 99
	again := Count(idx, []core.DocID{"c.go"}, []string{"language"})
	if again["language"].Counts["go"] != 1 {
		t.Fatalf("second count = %d, want 1; the first result shared state", again["language"].Counts["go"])
	}
}

func TestSortedUnique(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "sorts", in: []string{"kind", "dir"}, want: []string{"dir", "kind"}},
		{name: "drops duplicates", in: []string{"kind", "kind"}, want: []string{"kind"}},
		{name: "keeps the empty name", in: []string{"", "kind"}, want: []string{"", "kind"}},
		{name: "empty input", in: nil, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sortedUnique(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("sortedUnique(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
