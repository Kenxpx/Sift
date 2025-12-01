// Package core defines the types every Sift package shares: the documents
// produced by extraction, the postings that make up an index, the on-disk
// segment and manifest descriptions, and the query and reporting shapes.
//
// Sift indexes a local corpus and answers queries over the published
// index. Indexing is a pipeline: scan discovers files, extract turns them
// into documents, token splits documents into terms, index accumulates
// postings, segment writes them to disk, and publish makes a complete
// generation visible under the configured output directory. Every stage is
// deterministic: the same corpus and configuration always produce the same
// bytes.
package core

import "time"

// DocID identifies a document within a corpus. It is the slash-separated
// path of the source file relative to the corpus root, so it is stable
// across machines and comparable with plain string ordering.
type DocID string

// SegmentID identifies one immutable segment file within a generation.
type SegmentID string

// Generation numbers a published index. Generations increase by one and a
// reader always observes a complete generation, never a partial one.
type Generation int

// FileRef is a source file discovered by a scan.
type FileRef struct {
	// Rel is the slash-separated path relative to the corpus root.
	Rel string
	// Abs is the absolute path on disk.
	Abs string
	// Size is the file size in bytes.
	Size int64
	// ModTime is the file modification time.
	ModTime time.Time
	// Ext is the lower-case extension including the leading dot.
	Ext string
}

// Document is the extracted, indexable form of one source file.
type Document struct {
	ID DocID
	// Path is the same value as ID and is kept for reporting clarity.
	Path string
	// Title is the document title when one could be derived, else the base name.
	Title string
	// Kind classifies the document: "markdown", "text", "source" or "other".
	Kind string
	// Language is the detected source language for "source" documents.
	Language string
	// Body is the normalized text used for indexing.
	Body string
	// Fields carries filterable, facetable attributes such as "kind",
	// "language" and "dir". Values are compared exactly.
	Fields map[string]string
	// Size is the size of the source file in bytes.
	Size int64
	// ContentHash is the hex SHA-256 of the raw file contents.
	ContentHash string
}

// Token is one indexable term occurrence within a document.
type Token struct {
	Term string
	// Position is the zero-based ordinal of the token within the document.
	Position int
}

// Posting records how one term occurs in one document.
type Posting struct {
	DocID DocID
	// Freq is the number of occurrences of the term in the document.
	Freq int
	// Positions lists the token positions in ascending order.
	Positions []int
}

// TermStats summarizes one term across a whole index.
type TermStats struct {
	Term string
	// DocFreq is the number of documents containing the term.
	DocFreq int
	// TotalFreq is the number of occurrences across all documents.
	TotalFreq int
}

// DocInfo is the per-document data an index keeps for ranking and reporting.
type DocInfo struct {
	ID DocID
	// Length is the number of tokens in the document.
	Length int
	// Fields carries the document's filterable attributes.
	Fields map[string]string
	// Title is the document title.
	Title string
	// ContentHash is the hex SHA-256 of the source file contents.
	ContentHash string
}

// SegmentRef describes one segment file inside a manifest.
type SegmentRef struct {
	ID SegmentID
	// File is the segment file name relative to the output directory.
	File string
	// Digest is the hex SHA-256 of the segment file contents.
	Digest string
	// DocCount is the number of documents in the segment.
	DocCount int
	// TermCount is the number of distinct terms in the segment.
	TermCount int
}

// Manifest describes one published generation of an index.
type Manifest struct {
	Generation Generation
	// Segments are listed in ascending SegmentID order.
	Segments []SegmentRef
	// DocCount is the number of documents across all segments.
	DocCount int
	// TermCount is the number of distinct terms across all segments.
	TermCount int
	// ConfigHash is the hex SHA-256 of the canonical configuration used.
	ConfigHash string
	// CacheDigest is the hex SHA-256 of the extraction cache file that
	// belongs to this generation.
	CacheDigest string
	// CreatedAt is the publication time.
	CreatedAt time.Time
}

// Config is the corpus configuration, loaded from .sift.json when present.
type Config struct {
	// Root is the corpus root directory.
	Root string
	// OutputDir is where generations are published. Default: "sift-out".
	OutputDir string
	// Include lists glob patterns a file must match. Empty means all files.
	Include []string
	// Exclude lists glob patterns that skip a file.
	Exclude []string
	// MaxFileBytes skips files larger than this. Zero means no limit.
	MaxFileBytes int64
	// Stopwords are terms dropped during tokenization.
	Stopwords []string
	// MinTermLength drops shorter terms. Default: 2.
	MinTermLength int
	// SegmentDocs is the maximum number of documents per segment. Default: 256.
	SegmentDocs int
}

// SearchOptions describes one query.
type SearchOptions struct {
	// Query is the raw query text. An empty query matches every document.
	Query string
	// Filters restrict results to documents whose field values match exactly.
	Filters map[string]string
	// Limit caps the number of returned results. Non-positive means unlimited.
	Limit int
	// Facets names the fields to count. Counts cover every match, not the
	// limited page.
	Facets []string
}

// SearchResult is one matching document.
type SearchResult struct {
	DocID DocID
	Path  string
	Title string
	// Score is the relevance score; higher is better.
	Score float64
	// Freq is the total number of query-term occurrences in the document.
	Freq int
	// Fields carries the document's filterable attributes.
	Fields map[string]string
	// Snippet is a short excerpt around the first match, or empty.
	Snippet string
}

// Facet counts the values of one field across all matches.
type Facet struct {
	Field  string
	Counts map[string]int
}

// SearchReport is the result of one search.
type SearchReport struct {
	Options SearchOptions
	// Total is the number of matching documents before any limit.
	Total int
	// Results are ordered by score descending, then Freq descending, then
	// DocID ascending, and truncated to the limit.
	Results []SearchResult
	// Facets are keyed by field name.
	Facets map[string]Facet
}

// Clock reports the current time. Production code uses SystemClock; tests use
// a fixed clock so published manifests are byte-identical across runs.
type Clock interface {
	Now() time.Time
}

// SystemClock reports the wall-clock time.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock reports a constant time.
type FixedClock struct{ T time.Time }

// Now returns the configured time.
func (c FixedClock) Now() time.Time { return c.T }

// DefaultOutputDir is used when the configuration does not name one.
const DefaultOutputDir = "sift-out"

// ManifestFile is the manifest file name inside the output directory.
const ManifestFile = "manifest.json"

// CacheFile is the extraction-cache file name inside the output directory.
const CacheFile = "extract-cache.json"
