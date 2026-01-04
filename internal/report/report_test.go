package report

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"sift/internal/core"
	"sift/internal/stats"
)

// golden joins lines the way every renderer does: LF endings and one
// trailing newline.
func golden(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// sampleReport is a two-result page of a three-result match, with one facet
// that has an empty-valued bucket.
func sampleReport() core.SearchReport {
	return core.SearchReport{
		Options: core.SearchOptions{
			Query:   "alpha beta",
			Filters: map[string]string{"kind": "markdown"},
			Limit:   2,
			Facets:  []string{"kind"},
		},
		Total: 3,
		Results: []core.SearchResult{
			{
				DocID:   "docs/alpha.md",
				Path:    "docs/alpha.md",
				Title:   "Alpha",
				Score:   1.5,
				Freq:    4,
				Fields:  map[string]string{"kind": "markdown", "dir": "docs"},
				Snippet: "alpha beta gamma",
			},
			{
				DocID:  "docs/beta.md",
				Path:   "docs/beta.md",
				Title:  "Beta",
				Score:  0.25,
				Freq:   1,
				Fields: map[string]string{"kind": "markdown", "dir": "docs"},
			},
		},
		Facets: map[string]core.Facet{
			"kind": {Field: "kind", Counts: map[string]int{"markdown": 2, "": 1}},
		},
	}
}

// emptyReport matches everything but found nothing.
func emptyReport() core.SearchReport {
	return core.SearchReport{}
}

func TestTextGolden(t *testing.T) {
	want := golden(
		"query: alpha beta",
		"filters: kind=markdown",
		"total: 3",
		"shown: 2",
		"",
		"1. docs/alpha.md",
		"   title: Alpha",
		"   score: 1.5000",
		"   freq: 4",
		"   fields: dir=docs, kind=markdown",
		"   snippet: alpha beta gamma",
		"",
		"2. docs/beta.md",
		"   title: Beta",
		"   score: 0.2500",
		"   freq: 1",
		"   fields: dir=docs, kind=markdown",
		"",
		"facets:",
		"  kind:",
		"    (none): 1",
		"    markdown: 2",
	)
	if got := Text(sampleReport()); got != want {
		t.Errorf("Text mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextNoResults(t *testing.T) {
	want := golden(
		"query: (all documents)",
		"total: 0",
		"shown: 0",
		"",
		"no matching documents",
	)
	if got := Text(emptyReport()); got != want {
		t.Errorf("Text mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownGolden(t *testing.T) {
	want := golden(
		"# Search report",
		"",
		"- Query: `alpha beta`",
		"- Filters: `kind=markdown`",
		"- Total: 3",
		"- Shown: 2",
		"",
		"| # | Score | Freq | Document | Title | Snippet |",
		"| --- | --- | --- | --- | --- | --- |",
		"| 1 | 1.5000 | 4 | `docs/alpha.md` | Alpha | alpha beta gamma |",
		"| 2 | 0.2500 | 1 | `docs/beta.md` | Beta |  |",
		"",
		"## Facet: kind",
		"",
		"| Value | Count |",
		"| --- | --- |",
		"| (none) | 1 |",
		"| `markdown` | 2 |",
	)
	if got := Markdown(sampleReport()); got != want {
		t.Errorf("Markdown mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownNoResults(t *testing.T) {
	want := golden(
		"# Search report",
		"",
		"- Query: _(all documents)_",
		"- Total: 0",
		"- Shown: 0",
		"",
		"_No matching documents._",
	)
	if got := Markdown(emptyReport()); got != want {
		t.Errorf("Markdown mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownEscapesTableBreakers(t *testing.T) {
	r := core.SearchReport{
		Total: 1,
		Results: []core.SearchResult{{
			DocID:   "a|b.md",
			Path:    "a|b.md",
			Title:   "Pipe | and\nbreak",
			Score:   1,
			Freq:    1,
			Snippet: "one\ttwo\r\nthree",
		}},
	}
	got := Markdown(r)

	row := "| 1 | 1.0000 | 1 | `a\\|b.md` | Pipe \\| and break | one two three |"
	if !strings.Contains(got, row) {
		t.Errorf("escaped row missing.\ngot:\n%s\nwant row:\n%s", got, row)
	}
	body := strings.SplitN(got, "| --- |", 2)[1]
	if strings.Count(body, "\n") != 2 {
		t.Errorf("row content leaked extra lines:\n%s", body)
	}
}

func TestCSVGolden(t *testing.T) {
	want := golden(
		"rank,doc_id,path,title,score,freq,fields,snippet",
		"1,docs/alpha.md,docs/alpha.md,Alpha,1.5000,4,dir=docs;kind=markdown,alpha beta gamma",
		"2,docs/beta.md,docs/beta.md,Beta,0.2500,1,dir=docs;kind=markdown,",
	)
	if got := CSV(sampleReport()); got != want {
		t.Errorf("CSV mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestCSVIsParsableAndQuotesSeparators(t *testing.T) {
	r := sampleReport()
	r.Results[0].Title = "Alpha, first"
	r.Results[0].Snippet = "he said \"hi\""

	records, err := csv.NewReader(strings.NewReader(CSV(r))).ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3 (header plus two results)", len(records))
	}
	for i, rec := range records {
		if len(rec) != 8 {
			t.Fatalf("record %d has %d fields, want 8", i, len(rec))
		}
	}
	if records[0][0] != "rank" || records[0][7] != "snippet" {
		t.Errorf("header = %v", records[0])
	}
	if records[1][3] != "Alpha, first" {
		t.Errorf("title round-trip = %q, want %q", records[1][3], "Alpha, first")
	}
	if records[1][7] != "he said \"hi\"" {
		t.Errorf("snippet round-trip = %q", records[1][7])
	}
	if records[2][4] != "0.2500" {
		t.Errorf("score = %q, want 0.2500", records[2][4])
	}
}

func TestJSONShape(t *testing.T) {
	r := sampleReport()
	r.Facets["language"] = core.Facet{Field: "language", Counts: map[string]int{"go": 1}}

	out, err := JSON(r)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("JSON output does not end with a newline")
	}

	var got struct {
		Query   string            `json:"query"`
		Filters map[string]string `json:"filters"`
		Limit   int               `json:"limit"`
		Total   int               `json:"total"`
		Shown   int               `json:"shown"`
		Results []struct {
			Rank    int               `json:"rank"`
			DocID   string            `json:"doc_id"`
			Path    string            `json:"path"`
			Title   string            `json:"title"`
			Score   float64           `json:"score"`
			Freq    int               `json:"freq"`
			Fields  map[string]string `json:"fields"`
			Snippet string            `json:"snippet"`
		} `json:"results"`
		Facets []struct {
			Field  string         `json:"field"`
			Counts map[string]int `json:"counts"`
		} `json:"facets"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Query != "alpha beta" || got.Limit != 2 || got.Total != 3 || got.Shown != 2 {
		t.Errorf("header = %q/%d/%d/%d", got.Query, got.Limit, got.Total, got.Shown)
	}
	if got.Filters["kind"] != "markdown" {
		t.Errorf("filters = %v", got.Filters)
	}
	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(got.Results))
	}
	first := got.Results[0]
	if first.Rank != 1 || first.DocID != "docs/alpha.md" || first.Score != 1.5 || first.Freq != 4 {
		t.Errorf("first result = %+v", first)
	}
	if first.Fields["dir"] != "docs" || first.Snippet != "alpha beta gamma" {
		t.Errorf("first result detail = %+v", first)
	}
	if got.Results[1].Rank != 2 || got.Results[1].Snippet != "" {
		t.Errorf("second result = %+v", got.Results[1])
	}
	if len(got.Facets) != 2 {
		t.Fatalf("got %d facets, want 2", len(got.Facets))
	}
	if got.Facets[0].Field != "kind" || got.Facets[1].Field != "language" {
		t.Errorf("facets are not in ascending field order: %v, %v",
			got.Facets[0].Field, got.Facets[1].Field)
	}
	if got.Facets[0].Counts["markdown"] != 2 || got.Facets[0].Counts[""] != 1 {
		t.Errorf("kind counts = %v", got.Facets[0].Counts)
	}
}

func TestJSONEmptyReportGolden(t *testing.T) {
	want := golden(
		"{",
		`  "query": "",`,
		`  "filters": {},`,
		`  "limit": 0,`,
		`  "requested_facets": [],`,
		`  "total": 0,`,
		`  "shown": 0,`,
		`  "results": [],`,
		`  "facets": []`,
		"}",
	)
	got, err := JSON(emptyReport())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if got != want {
		t.Errorf("JSON mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatsGolden(t *testing.T) {
	corpus := stats.Corpus{
		Documents:  3,
		Terms:      4,
		Tokens:     6,
		ByKind:     map[string]int{"markdown": 2, "": 1},
		ByLanguage: map[string]int{"": 3},
		LargestDocs: []core.DocInfo{
			{ID: "docs/alpha.md", Length: 3},
			{ID: "docs/beta.md", Length: 2},
		},
	}
	want := golden(
		"documents: 3",
		"terms: 4",
		"tokens: 6",
		"",
		"by kind:",
		"  (none): 1",
		"  markdown: 2",
		"",
		"by language:",
		"  (none): 3",
		"",
		"largest documents:",
		"  1. docs/alpha.md: 3",
		"  2. docs/beta.md: 2",
	)
	if got := Stats(corpus); got != want {
		t.Errorf("Stats mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatsEmptyCorpusGolden(t *testing.T) {
	want := golden(
		"documents: 0",
		"terms: 0",
		"tokens: 0",
		"",
		"by kind:",
		"",
		"by language:",
		"",
		"largest documents:",
	)
	if got := Stats(stats.Corpus{}); got != want {
		t.Errorf("Stats mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderersUseLFAndOneTrailingNewline(t *testing.T) {
	corpus := stats.Corpus{Documents: 1, Terms: 1, Tokens: 1,
		ByKind:      map[string]int{"markdown": 1},
		ByLanguage:  map[string]int{"": 1},
		LargestDocs: []core.DocInfo{{ID: "a.md", Length: 1}},
	}
	jsonSample, err := JSON(sampleReport())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	jsonEmpty, err := JSON(emptyReport())
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	for _, tc := range []struct {
		name string
		out  string
	}{
		{"Text", Text(sampleReport())},
		{"TextEmpty", Text(emptyReport())},
		{"Markdown", Markdown(sampleReport())},
		{"MarkdownEmpty", Markdown(emptyReport())},
		{"CSV", CSV(sampleReport())},
		{"CSVEmpty", CSV(emptyReport())},
		{"JSON", jsonSample},
		{"JSONEmpty", jsonEmpty},
		{"Stats", Stats(corpus)},
		{"StatsEmpty", Stats(stats.Corpus{})},
	} {
		if strings.Contains(tc.out, "\r") {
			t.Errorf("%s contains a carriage return", tc.name)
		}
		if !strings.HasSuffix(tc.out, "\n") {
			t.Errorf("%s does not end with a newline: %q", tc.name, tc.out)
		}
		if strings.HasSuffix(tc.out, "\n\n") {
			t.Errorf("%s ends with a blank line", tc.name)
		}
	}
}

func TestRenderersAreDeterministic(t *testing.T) {
	r := sampleReport()
	r.Options.Filters["dir"] = "docs"
	r.Results[0].Fields["ext"] = ".md"
	r.Facets["language"] = core.Facet{Field: "language", Counts: map[string]int{"go": 1, "": 2}}

	firstJSON, err := JSON(r)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	wantText, wantMarkdown, wantCSV := Text(r), Markdown(r), CSV(r)

	// Map iteration order varies between runs, so repeat enough times that an
	// unsorted map would show up.
	for i := 0; i < 32; i++ {
		if got := Text(r); got != wantText {
			t.Fatalf("Text differed on iteration %d:\n%s", i, got)
		}
		if got := Markdown(r); got != wantMarkdown {
			t.Fatalf("Markdown differed on iteration %d:\n%s", i, got)
		}
		if got := CSV(r); got != wantCSV {
			t.Fatalf("CSV differed on iteration %d:\n%s", i, got)
		}
		got, err := JSON(r)
		if err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if got != firstJSON {
			t.Fatalf("JSON differed on iteration %d:\n%s", i, got)
		}
	}
}

func TestResultPathFallsBackToDocID(t *testing.T) {
	r := core.SearchReport{
		Total:   1,
		Results: []core.SearchResult{{DocID: "docs/only-id.md", Score: 2, Freq: 1}},
	}
	if got := Text(r); !strings.Contains(got, "1. docs/only-id.md") {
		t.Errorf("Text did not fall back to the doc id:\n%s", got)
	}
	if got := CSV(r); !strings.Contains(got, "1,docs/only-id.md,docs/only-id.md,,2.0000,1,,") {
		t.Errorf("CSV did not fall back to the doc id:\n%s", got)
	}
}
