// Package report renders search reports and corpus statistics for humans and
// for machines: plain text, Markdown, CSV, JSON and a statistics summary.
//
// Every renderer is deterministic. Maps are never ranged over directly:
// document fields, filters and facet values are always emitted in ascending
// key order, and facets themselves in ascending field order. All output uses
// LF line endings and ends with exactly one trailing newline, so the rendered
// bytes can be compared directly in tests and golden files.
package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"sift/internal/core"
	"sift/internal/stats"
)

// scoreDigits is the fixed number of fractional digits used for scores, so
// the same score always renders to the same characters.
const scoreDigits = 4

// noValue labels the bucket that collects documents with no value for a
// faceted or grouped field.
const noValue = "(none)"

// lines collects output lines that are joined with LF and terminated by a
// trailing newline.
type lines []string

// add appends one formatted line.
func (l *lines) add(format string, args ...any) {
	*l = append(*l, fmt.Sprintf(format, args...))
}

// blank appends one empty line.
func (l *lines) blank() {
	*l = append(*l, "")
}

// String renders the collected lines.
func (l lines) String() string {
	return strings.Join(l, "\n") + "\n"
}

// oneLineReplacer flattens embedded line breaks and tabs so a value never
// breaks the surrounding line-oriented layout.
var oneLineReplacer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ")

// mdCellReplacer escapes a value for use inside a Markdown table cell.
var mdCellReplacer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ", "|", "\\|")

// mdCodeReplacer escapes a value for use inside a Markdown code span. A
// backtick becomes an apostrophe so the span cannot be closed early.
var mdCodeReplacer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ", "|", "\\|", "`", "'")

// oneLine collapses s onto a single line.
func oneLine(s string) string { return oneLineReplacer.Replace(s) }

// mdCell escapes s for a Markdown table cell.
func mdCell(s string) string { return mdCellReplacer.Replace(s) }

// mdCode wraps s in a Markdown code span, or returns the empty string when
// there is nothing to wrap.
func mdCode(s string) string {
	if s == "" {
		return ""
	}
	return "`" + mdCodeReplacer.Replace(s) + "`"
}

// formatScore renders a relevance score with a fixed number of digits.
func formatScore(f float64) string {
	return strconv.FormatFloat(f, 'f', scoreDigits, 64)
}

// displayValue labels an empty field value.
func displayValue(v string) string {
	if v == "" {
		return noValue
	}
	return v
}

// sortedStringKeys returns the keys of m in ascending order.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortedCountKeys returns the keys of m in ascending order.
func sortedCountKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fieldPairs renders m as "key=value" pairs in ascending key order.
func fieldPairs(m map[string]string) []string {
	keys := sortedStringKeys(m)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+oneLine(m[k]))
	}
	return pairs
}

// sortedFacets returns the facets of m in ascending field order. The Field of
// each returned facet is filled from its map key when the value omits it.
func sortedFacets(m map[string]core.Facet) []core.Facet {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]core.Facet, 0, len(keys))
	for _, k := range keys {
		f := m[k]
		if f.Field == "" {
			f.Field = k
		}
		out = append(out, f)
	}
	return out
}

// resultPath is the path shown for a result, falling back to the document id.
func resultPath(r core.SearchResult) string {
	if r.Path != "" {
		return r.Path
	}
	return string(r.DocID)
}

// queryLabel describes the query text, naming the match-everything case.
func queryLabel(q string) string {
	if strings.TrimSpace(q) == "" {
		return "(all documents)"
	}
	return oneLine(q)
}

// Text renders r as a plain-text report: a header of query, filters, total
// and shown counts, one indented block per result, then the facet counts.
func Text(r core.SearchReport) string {
	var out lines
	out.add("query: %s", queryLabel(r.Options.Query))
	if len(r.Options.Filters) > 0 {
		out.add("filters: %s", strings.Join(fieldPairs(r.Options.Filters), ", "))
	}
	out.add("total: %d", r.Total)
	out.add("shown: %d", len(r.Results))
	out.blank()

	if len(r.Results) == 0 {
		out.add("no matching documents")
	}
	for i, res := range r.Results {
		if i > 0 {
			out.blank()
		}
		out.add("%d. %s", i+1, oneLine(resultPath(res)))
		out.add("   title: %s", oneLine(res.Title))
		out.add("   score: %s", formatScore(res.Score))
		out.add("   freq: %d", res.Freq)
		if len(res.Fields) > 0 {
			out.add("   fields: %s", strings.Join(fieldPairs(res.Fields), ", "))
		}
		if res.Snippet != "" {
			out.add("   snippet: %s", oneLine(res.Snippet))
		}
	}

	if len(r.Facets) > 0 {
		out.blank()
		out.add("facets:")
		for _, f := range sortedFacets(r.Facets) {
			out.add("  %s:", f.Field)
			for _, v := range sortedCountKeys(f.Counts) {
				out.add("    %s: %d", displayValue(v), f.Counts[v])
			}
		}
	}
	return out.String()
}

// Markdown renders r as a Markdown document: a header list, a results table
// and one table per facet. Cell contents are escaped so a title or snippet
// containing a pipe cannot break the table.
func Markdown(r core.SearchReport) string {
	var out lines
	out.add("# Search report")
	out.blank()
	out.add("- Query: %s", markdownQuery(r.Options.Query))
	if len(r.Options.Filters) > 0 {
		out.add("- Filters: %s", mdCode(strings.Join(fieldPairs(r.Options.Filters), ", ")))
	}
	out.add("- Total: %d", r.Total)
	out.add("- Shown: %d", len(r.Results))
	out.blank()

	if len(r.Results) == 0 {
		out.add("_No matching documents._")
	} else {
		out.add("| # | Score | Freq | Document | Title | Snippet |")
		out.add("| --- | --- | --- | --- | --- | --- |")
		for i, res := range r.Results {
			out.add("| %d | %s | %d | %s | %s | %s |",
				i+1,
				formatScore(res.Score),
				res.Freq,
				mdCode(resultPath(res)),
				mdCell(res.Title),
				mdCell(res.Snippet),
			)
		}
	}

	for _, f := range sortedFacets(r.Facets) {
		out.blank()
		out.add("## Facet: %s", mdCell(f.Field))
		out.blank()
		out.add("| Value | Count |")
		out.add("| --- | --- |")
		for _, v := range sortedCountKeys(f.Counts) {
			cell := noValue
			if v != "" {
				cell = mdCode(v)
			}
			out.add("| %s | %d |", cell, f.Counts[v])
		}
	}
	return out.String()
}

// markdownQuery renders the query for the Markdown header list.
func markdownQuery(q string) string {
	if strings.TrimSpace(q) == "" {
		return "_(all documents)_"
	}
	return mdCode(oneLine(q))
}

// CSV renders the results of r as RFC 4180 records with a header row and LF
// line endings. Facet counts have no place in a flat table and are omitted;
// use JSON, Text or Markdown for those.
func CSV(r core.SearchReport) string {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	// Writes to a bytes.Buffer cannot fail, so the recorded csv error stays
	// nil here and the returned errors are deliberately ignored.
	_ = w.Write([]string{"rank", "doc_id", "path", "title", "score", "freq", "fields", "snippet"})
	for i, res := range r.Results {
		_ = w.Write([]string{
			strconv.Itoa(i + 1),
			string(res.DocID),
			resultPath(res),
			oneLine(res.Title),
			formatScore(res.Score),
			strconv.Itoa(res.Freq),
			strings.Join(fieldPairs(res.Fields), ";"),
			oneLine(res.Snippet),
		})
	}
	w.Flush()
	return buf.String()
}

// jsonReport is the wire shape of a search report. Field order is fixed by
// the struct and map keys are sorted by encoding/json, so the rendered bytes
// depend only on the report.
type jsonReport struct {
	Query     string            `json:"query"`
	Filters   map[string]string `json:"filters"`
	Limit     int               `json:"limit"`
	AskFacets []string          `json:"requested_facets"`
	Total     int               `json:"total"`
	Shown     int               `json:"shown"`
	Results   []jsonResult      `json:"results"`
	Facets    []jsonFacet       `json:"facets"`
}

// jsonResult is the wire shape of one search result.
type jsonResult struct {
	Rank    int               `json:"rank"`
	DocID   string            `json:"doc_id"`
	Path    string            `json:"path"`
	Title   string            `json:"title"`
	Score   float64           `json:"score"`
	Freq    int               `json:"freq"`
	Fields  map[string]string `json:"fields"`
	Snippet string            `json:"snippet"`
}

// jsonFacet is the wire shape of one facet.
type jsonFacet struct {
	Field  string         `json:"field"`
	Counts map[string]int `json:"counts"`
}

// JSON renders r as indented JSON with a trailing newline. Absent maps and
// slices are rendered as empty objects and arrays rather than null, so
// consumers never have to special-case them.
func JSON(r core.SearchReport) (string, error) {
	doc := jsonReport{
		Query:     r.Options.Query,
		Filters:   copyFields(r.Options.Filters),
		Limit:     r.Options.Limit,
		AskFacets: append([]string{}, r.Options.Facets...),
		Total:     r.Total,
		Shown:     len(r.Results),
		Results:   make([]jsonResult, 0, len(r.Results)),
		Facets:    make([]jsonFacet, 0, len(r.Facets)),
	}
	for i, res := range r.Results {
		doc.Results = append(doc.Results, jsonResult{
			Rank:    i + 1,
			DocID:   string(res.DocID),
			Path:    resultPath(res),
			Title:   res.Title,
			Score:   res.Score,
			Freq:    res.Freq,
			Fields:  copyFields(res.Fields),
			Snippet: res.Snippet,
		})
	}
	for _, f := range sortedFacets(r.Facets) {
		counts := make(map[string]int, len(f.Counts))
		for k, v := range f.Counts {
			counts[k] = v
		}
		doc.Facets = append(doc.Facets, jsonFacet{Field: f.Field, Counts: counts})
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("report: encode json: %w", err)
	}
	return string(b) + "\n", nil
}

// copyFields returns a non-nil copy of m so JSON never renders null.
func copyFields(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Stats renders a corpus summary as plain text: the headline counts, the
// documents per kind and per language in ascending value order, and the
// largest documents in the order stats.Compute chose.
func Stats(s stats.Corpus) string {
	var out lines
	out.add("documents: %d", s.Documents)
	out.add("terms: %d", s.Terms)
	out.add("tokens: %d", s.Tokens)

	out.blank()
	out.add("by kind:")
	for _, k := range sortedCountKeys(s.ByKind) {
		out.add("  %s: %d", displayValue(k), s.ByKind[k])
	}

	out.blank()
	out.add("by language:")
	for _, k := range sortedCountKeys(s.ByLanguage) {
		out.add("  %s: %d", displayValue(k), s.ByLanguage[k])
	}

	out.blank()
	out.add("largest documents:")
	for i, d := range s.LargestDocs {
		out.add("  %d. %s: %d", i+1, oneLine(string(d.ID)), d.Length)
	}
	return out.String()
}
